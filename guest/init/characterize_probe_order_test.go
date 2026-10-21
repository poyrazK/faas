//go:build linux

package main

import (
	"bufio"
	"context"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// The HTTP/2-preface gRPC probe must not reach an app that answers
// HTTP/1.1: gunicorn logged "Invalid HTTP Version: (2, 0)" from 127.0.0.1
// at every cold boot of every Python app (Hunt 11 H11-5).
func TestCollectL7ProbesSendsPrefaceOnlyToSilentListeners(t *testing.T) {
	answer := func(r probeResult) func() probeResult { return func() probeResult { return r } }
	tests := []struct {
		name       string
		http1      []func() probeResult
		h2         probeResult
		wantClass  string
		wantDoc    string
		wantH2Runs int32
	}{
		{"HTTP/1 404s skip the preface", []func() probeResult{answer(probeResult{HTTP1: true}), answer(probeResult{HTTP1: true})}, probeResult{Class: classGRPC}, "", "", 0},
		{"an HTTP class wins without the preface", []func() probeResult{answer(probeResult{}), answer(probeResult{Class: classHTTP, HTTP1: true, OpenAPIDoc: []byte(`{"openapi":"3.1.0"}`)})}, probeResult{Class: classGRPC}, classHTTP, `{"openapi":"3.1.0"}`, 0},
		{"a silent listener gets the preface", []func() probeResult{answer(probeResult{}), answer(probeResult{})}, probeResult{Class: classGRPC}, classGRPC, "", 1},
		{"a silent non-gRPC listener has no class", []func() probeResult{answer(probeResult{}), answer(probeResult{})}, probeResult{}, "", "", 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var h2Runs atomic.Int32
			h2 := func() probeResult { h2Runs.Add(1); return tt.h2 }
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			class, doc, _ := collectL7Probes(ctx, tt.http1, h2)
			if class != tt.wantClass || string(doc) != tt.wantDoc || h2Runs.Load() != tt.wantH2Runs {
				t.Fatalf("got class %q doc %q preface probes %d; want %q %q %d", class, doc, h2Runs.Load(), tt.wantClass, tt.wantDoc, tt.wantH2Runs)
			}
		})
	}
}

// TestRunL7ProbesKeepsPrefaceOffHTTP1Servers drives the real probes against
// an HTTP/1.1-only listener (like gunicorn) that records the request lines it
// receives. Before H11-5 the preface reached it at every characterization.
func TestRunL7ProbesKeepsPrefaceOffHTTP1Servers(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	var mu sync.Mutex
	var lines []string
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer func() { _ = c.Close() }()
				_ = c.SetDeadline(time.Now().Add(2 * time.Second))
				line, _ := bufio.NewReader(c).ReadString('\n')
				mu.Lock()
				lines = append(lines, strings.TrimSpace(line))
				mu.Unlock()
				if strings.Contains(line, "HTTP/1.") {
					_, _ = c.Write([]byte("HTTP/1.1 404 Not Found\r\nContent-Length: 0\r\nConnection: close\r\n\r\n"))
				}
			}(c)
		}
	}()
	class, _, _ := runL7Probes(context.Background(), RunArgs{}, ln.Addr().(*net.TCPAddr).Port)
	// Give the listener's goroutines a moment to record every connection.
	time.Sleep(100 * time.Millisecond)
	mu.Lock()
	defer mu.Unlock()
	if class != "" {
		t.Errorf("class = %q, want none for a 404-only server", class)
	}
	for _, line := range lines {
		if strings.HasPrefix(line, "PRI * HTTP/2.0") {
			t.Fatalf("HTTP/1 server received the HTTP/2 preface; request lines: %q", lines)
		}
	}
	if len(lines) != 2 {
		t.Fatalf("request lines = %q, want the GraphQL POST and the OpenAPI GET", lines)
	}
}
