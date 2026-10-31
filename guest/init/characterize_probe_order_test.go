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
		// Production-us rc.294: gunicorn logged the preface again when its
		// workers were still booting and the HTTP/1.1 probe timed out (Hunt 14 H14-21).
		{"a stalled HTTP/1 listener skips the preface", []func() probeResult{answer(probeResult{Stalled: true})}, probeResult{Class: classGRPC}, "", "", 0},
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

// TestRunL7ProbesRequests drives the real probes against an HTTP/1.1-only
// listener (like gunicorn) that records the request lines it receives.
// Before H11-5 the HTTP/2 preface reached it at every characterization, and
// before H12-6 a GraphQL POST / did too; a function's runner gets nothing.
func TestRunL7ProbesRequests(t *testing.T) {
	tests := []struct {
		name string
		args RunArgs
		want []string
	}{
		{"app gets only the OpenAPI GET", RunArgs{}, []string{"GET /openapi.json HTTP/1.1"}},
		{"function runner gets no probes", RunArgs{FunctionRunner: true}, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			lines := recordProbeRequestLines(t, tt.args)
			if strings.Join(lines, "|") != strings.Join(tt.want, "|") {
				t.Fatalf("request lines = %q, want %q", lines, tt.want)
			}
		})
	}
}

func recordProbeRequestLines(t *testing.T, args RunArgs) []string {
	t.Helper()
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
	if class, _, _ := runL7Probes(context.Background(), args, ln.Addr().(*net.TCPAddr).Port); class != "" {
		t.Errorf("class = %q, want none for a 404-only server", class)
	}
	// Give the listener's goroutines a moment to record every connection.
	time.Sleep(100 * time.Millisecond)
	mu.Lock()
	defer mu.Unlock()
	return append([]string(nil), lines...)
}
