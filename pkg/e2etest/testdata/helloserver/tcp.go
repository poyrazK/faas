package main

import (
	"encoding/json"
	"errors"
	"flag"
	"io"
	"log"
	"net"
	"net/http"
	"strconv"
	"time"
)

// Private TCP fixtures (ADR-576), so service-to-service TCP tests can deploy
// images instead of building sources:
//
//   - -tcp-echo PORT serves a raw TCP listener that answers every chunk it
//     reads with "tcp-echo:" followed by the chunk;
//   - GET /dial?host=&port=&payload= opens a TCP connection from inside the
//     guest, sends payload, and reports the echoed reply as JSON.
var tcpEchoPort = flag.Int("tcp-echo", 0, "also serve a TCP echo listener on this port")

const (
	tcpEchoPrefix  = "tcp-echo:"
	tcpDialTimeout = 60 * time.Second
)

func registerTCPFixture(mux *http.ServeMux) {
	mux.HandleFunc("/dial", serveTCPDial)
	if *tcpEchoPort > 0 {
		go serveTCPEcho(*tcpEchoPort)
	}
}

func serveTCPEcho(port int) {
	ln, err := net.Listen("tcp", ":"+strconv.Itoa(port))
	if err != nil {
		log.Fatalf("hello-server: TCP echo on :%d: %v", port, err)
	}
	log.Printf("hello-server: TCP echo on :%d", port)
	for {
		conn, err := ln.Accept()
		if err != nil {
			log.Printf("hello-server: TCP echo accept: %v", err)
			continue
		}
		go echoTCP(conn)
	}
}

func echoTCP(conn net.Conn) {
	defer func() { _ = conn.Close() }()
	buf := make([]byte, 32<<10)
	for {
		n, err := conn.Read(buf)
		if n > 0 {
			if _, werr := conn.Write(append([]byte(tcpEchoPrefix), buf[:n]...)); werr != nil {
				return
			}
		}
		if err != nil {
			return
		}
	}
}

// tcpDialResult matches what the Node TCP dialer fixture reports.
type tcpDialResult struct {
	Resolved string `json:"resolved"`
	Reply    string `json:"reply"`
	Error    string `json:"error,omitempty"`
}

func serveTCPDial(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	host, port, payload := q.Get("host"), q.Get("port"), q.Get("payload")
	if payload == "" {
		payload = "ping"
	}
	var res tcpDialResult
	if ips, err := net.DefaultResolver.LookupIP(r.Context(), "ip4", host); err == nil && len(ips) > 0 {
		res.Resolved = ips[0].String()
	}
	status := dialAndRead(host, port, payload, &res)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(res)
}

// dialAndRead sends payload to host:port and reads until the echo of all of
// it has arrived, the peer closes, or the deadline passes.
func dialAndRead(host, port, payload string, res *tcpDialResult) int {
	conn, err := net.DialTimeout("tcp", net.JoinHostPort(host, port), tcpDialTimeout)
	if err != nil {
		res.Error = err.Error()
		return http.StatusBadGateway
	}
	defer func() { _ = conn.Close() }()
	_ = conn.SetDeadline(time.Now().Add(tcpDialTimeout))
	if _, err := io.WriteString(conn, payload); err != nil {
		res.Error = err.Error()
		return http.StatusBadGateway
	}
	want := len(tcpEchoPrefix) + len(payload)
	reply := make([]byte, 0, want)
	buf := make([]byte, 4096)
	for len(reply) < want {
		n, err := conn.Read(buf)
		reply = append(reply, buf[:n]...)
		if err != nil {
			res.Reply = string(reply)
			var netErr net.Error
			switch {
			case errors.As(err, &netErr) && netErr.Timeout():
				res.Error = "timeout"
				return http.StatusGatewayTimeout
			case errors.Is(err, io.EOF):
				res.Error = "closed"
			default:
				res.Error = err.Error()
			}
			return http.StatusBadGateway
		}
	}
	res.Reply = string(reply)
	return http.StatusOK
}
