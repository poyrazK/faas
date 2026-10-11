//go:build linux

package main

// adr: 981 — vmmd splices relay bytes without interpreting them.

import (
	"io"
	"net"
	"path/filepath"
	"testing"
	"time"
)

func unixConnPair(t *testing.T) (net.Conn, net.Conn) {
	t.Helper()
	ln, err := net.Listen("unix", filepath.Join(shortSocketDir(t), "pair.sock"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ln.Close() }()
	accepted := make(chan net.Conn, 1)
	go func() {
		conn, _ := ln.Accept()
		accepted <- conn
	}()
	client, err := net.Dial("unix", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	server := <-accepted
	t.Cleanup(func() { _ = client.Close(); _ = server.Close() })
	return client, server
}

func TestSpliceOutboundRelayPropagatesBytesAndHalfClose(t *testing.T) {
	guestApp, guestHost := unixConnPair(t)
	outboundd, upstream := unixConnPair(t)
	done := make(chan struct{})
	go func() {
		spliceOutboundRelay(guestHost, upstream)
		close(done)
	}()
	request := "POST /i/llm/v1/messages HTTP/1.1\r\n\r\n"
	if _, err := io.WriteString(guestApp, request); err != nil {
		t.Fatal(err)
	}
	_ = guestApp.(*net.UnixConn).CloseWrite()
	got, err := io.ReadAll(outboundd)
	if err != nil || string(got) != request {
		t.Fatalf("outboundd read %q, %v", got, err)
	}
	if _, err := io.WriteString(outboundd, "HTTP/1.1 200 OK\r\n\r\nstream"); err != nil {
		t.Fatal(err)
	}
	_ = outboundd.Close()
	response, err := io.ReadAll(guestApp)
	if err != nil || string(response) != "HTTP/1.1 200 OK\r\n\r\nstream" {
		t.Fatalf("guest read %q, %v", response, err)
	}
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("splice did not finish after both directions closed")
	}
}
