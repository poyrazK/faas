package main

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// TestReadResponseHead_HappyPath pins the byte-exact head split
// the bridge uses. The HTTP/1.1 response head terminator is "\n\n"
// (RFC 7230 §3); readResponseHead must return the head UP TO BUT
// NOT INCLUDING the terminator and any body bytes that arrived in
// the same read.
func TestReadResponseHead_HappyPath(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Test", "ok")
		w.Header().Set("Content-Length", "10")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("bodybytes"))
	}))
	defer srv.Close()

	conn, err := net.Dial("tcp", strings.TrimPrefix(srv.URL, "http://"))
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	// Issue a real HTTP/1.1 request with a Content-Length so the
	// server emits a matching response.
	if _, err := conn.Write([]byte("GET / HTTP/1.1\r\nHost: x\r\nConnection: close\r\n\r\n")); err != nil {
		t.Fatalf("write request: %v", err)
	}

	// Use a single bufio.Reader for both the head read and the
	// body read after — readResponseHead leaves any post-head
	// bytes in the bufio.Reader's internal buffer.
	breader := bufio.NewReader(conn)
	head, initialBody, err := readResponseHead(breader)
	if err != nil {
		t.Fatalf("readResponseHead: %v", err)
	}
	// Drain the rest of the body via the same bufio reader so
	// any bytes still buffered after the head are visible.
	rest, err := io.ReadAll(breader)
	if err != nil && !errors.Is(err, io.EOF) {
		t.Fatalf("read body: %v", err)
	}
	body := append(initialBody, rest...)
	// Head must NOT contain the "\n\n" terminator.
	if bytes.Contains(head, []byte("\n\n")) {
		t.Errorf("head contains terminator: %q", string(head))
	}
	// Head must begin with the status line.
	if !strings.HasPrefix(string(head), "HTTP/1.1 200 OK") {
		t.Errorf("head status line malformed: %q", string(head))
	}
	if !bytes.Contains(head, []byte("X-Test: ok")) {
		t.Errorf("missing X-Test header in head: %q", string(head))
	}
	// Body bytes should round-trip (the test pins the head/body
	// split).
	if !bytes.Contains(body, []byte("bodybytes")) {
		t.Errorf("body bytes lost in head split: %q", string(body))
	}
}

// TestReadResponseHead_BodyAcrossMultipleReads covers the case
// where the response head and body are split across multiple
// TCP reads. readResponseHead must buffer head bytes internally
// and only return once the \n\n terminator is found.
func TestReadResponseHead_BodyAcrossMultipleReads(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Multi", "yes")
		w.Header().Set("Content-Length", "8192")
		w.WriteHeader(http.StatusOK)
		// Long body to force multiple TCP segments on the wire.
		_, _ = w.Write(bytes.Repeat([]byte("Z"), 8192))
	}))
	defer srv.Close()

	conn, err := net.Dial("tcp", strings.TrimPrefix(srv.URL, "http://"))
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	if _, err := conn.Write([]byte("GET / HTTP/1.1\r\nHost: x\r\nConnection: close\r\n\r\n")); err != nil {
		t.Fatalf("write: %v", err)
	}

	breader := bufio.NewReader(conn)
	head, initialBody, err := readResponseHead(breader)
	if err != nil {
		t.Fatalf("readResponseHead: %v", err)
	}
	if !strings.HasPrefix(string(head), "HTTP/1.1 200 OK") {
		t.Errorf("head status line malformed: %q", string(head))
	}
	if !bytes.Contains(head, []byte("X-Multi: yes")) {
		t.Errorf("missing X-Multi header in head: %q", string(head))
	}
	// The body prefix returned by readResponseHead plus what we
	// drain via the same bufio.Reader should match what the server
	// sent (8192 Z's).
	rest, err := io.ReadAll(breader)
	if err != nil && !errors.Is(err, io.EOF) {
		t.Fatalf("read body: %v", err)
	}
	body := append(initialBody, rest...)
	if len(body) != 8192 {
		t.Errorf("body length = %d, want 8192", len(body))
	}
	for i, b := range body {
		if b != 'Z' {
			t.Errorf("body[%d] = %q, want 'Z'", i, b)
			break
		}
	}
}

// TestIndexDoubleLF pins the byte-exact search the bridge uses
// for the HTTP/1.1 head terminator. Faster than bytes.Index for
// the const "\n\n" but the difference is academic; the test pins
// the boundary so a future refactor can't shift behaviour.
func TestIndexDoubleLF(t *testing.T) {
	for _, tc := range []struct {
		name string
		in   string
		want int
	}{
		{"empty", "", -1},
		{"single newline", "foo\n", -1},
		{"first \n\n at start", "\n\nrest", 0},
		{"first \n\n mid", "abc\n\ndef", 3},
		{"multiple \n\n returns first", "\n\nx\n\ny", 0},
		{"triple \n (not double)", "\n\n\n", 0},
	} {
		got := indexDoubleLF([]byte(tc.in))
		if got != tc.want {
			t.Errorf("%s: indexDoubleLF(%q) = %d, want %d", tc.name, tc.in, got, tc.want)
		}
	}
}

// TestBridge_UsageError pins the bridge's argv validation. Bad
// IP/port must exit with code 2 (usage).
func TestBridge_UsageError(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("vmmd-raw-bridge uses net.JoinHostPort; skipping on Windows")
	}
	tmp := t.TempDir()
	binPath := filepath.Join(tmp, "vmmd-raw-bridge")
	if err := buildBridgeForTest(binPath); err != nil {
		t.Fatalf("build bridge: %v", err)
	}
	for _, argv := range [][]string{{}, {"only-one-arg"}, {"bad-ip", "99999"}} {
		cmd := exec.Command(binPath, argv...)
		out, err := cmd.CombinedOutput()
		if err == nil {
			t.Errorf("argv=%v: expected exit error, got %q", argv, string(out))
		}
		// Bridge exits 2 on usage errors. errors.As walks the
		// wrap chain so a future call site that adds context via
		// fmt.Errorf("%w: ...", err) still classifies correctly
		// (errorlint lint rule).
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) && exitErr.ExitCode() != 2 {
			t.Errorf("argv=%v: exit code %d, want 2", argv, exitErr.ExitCode())
		}
	}
}

// TestBridge_DialRefused pins the bridge's exit code on a
// refused dial. Bind a listener, capture its port, close the
// listener, and verify the bridge exits 3 within the dial
// timeout.
func TestBridge_DialRefused(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("vmmd-raw-bridge uses net.JoinHostPort; skipping on Windows")
	}
	tmp := t.TempDir()
	binPath := filepath.Join(tmp, "vmmd-raw-bridge")
	if err := buildBridgeForTest(binPath); err != nil {
		t.Fatalf("build bridge: %v", err)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()

	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		t.Fatalf("split: %v", err)
	}
	cmd := exec.Command(binPath, host, port)
	if err := cmd.Start(); err != nil {
		t.Fatalf("bridge start: %v", err)
	}
	// 30 s dial timeout (the bridge's hard cap) — exit 3
	// should land well before that.
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		// errors.As walks the wrap chain (errorlint lint rule).
		var exitErr *exec.ExitError
		if !errors.As(err, &exitErr) || exitErr.ExitCode() != 3 {
			t.Errorf("bridge exit: %v, want exit code 3", err)
		}
	case <-time.After(5 * time.Second):
		_ = cmd.Process.Kill()
		t.Fatalf("bridge did not exit on dial refused")
	}
}

// buildBridgeForTest compiles the vmmd-raw-bridge binary to the
// given path. Uses the standard `go build` from the cmd's parent
// dir so the test suite doesn't depend on a pre-installed binary.
func buildBridgeForTest(out string) error {
	// go test runs in this package's directory.
	cmd := exec.Command("go", "build", "-o", out, ".")
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// TestIsCleanConnClose pins the bridge's exit-0 classification
// (issue #676 review fix): a guest-side close of the conn while
// the bridge is mid-body-write produces io.ErrClosedPipe /
// "use of closed network connection" / "broken pipe" — all of
// which isCleanConnClose must classify as clean (exit 0 path).
// A real write error (timeout, EPERM, etc.) must NOT be
// classified as clean so the bridge surfaces exit 4.
//
// The classification is purely string + errors.Is based;
// string.Contains catches the net pkg's "use of closed network
// connection" because that error is not exported.
func TestIsCleanConnClose(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, true},
		{"io_eof", io.EOF, true},
		{"wrapped_eof", io.EOF, true},
		{"err_closed_pipe", io.ErrClosedPipe, true},
		{"use_of_closed_network_connection", errors.New("write tcp 127.0.0.1:1->127.0.0.1:2: use of closed network connection"), true},
		{"broken_pipe", errors.New("write tcp 127.0.0.1:1->127.0.0.1:2: broken pipe"), true},
		{"real_write_error", errors.New("write tcp 127.0.0.1:1->127.0.0.1:2: i/o timeout"), false},
		{"unexpected_string", errors.New("some other failure"), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := isCleanConnClose(tc.err); got != tc.want {
				t.Errorf("isCleanConnClose(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}

// TestIsCleanConnClose_WrappedClosedPipe pins that the bridge
// sees through fmt.Errorf("%w: ...", io.ErrClosedPipe) — errors.Is
// unwraps the chain, so the helper classifies the wrapped error
// as clean even when the caller adds context. Mirrors the real
// net pkg pattern where conn.Write errors carry
// io.ErrClosedPipe inside an *net.OpError wrapper.
func TestIsCleanConnClose_WrappedClosedPipe(t *testing.T) {
	wrapped := fmt.Errorf("bridge write: %w", io.ErrClosedPipe)
	if !isCleanConnClose(wrapped) {
		t.Errorf("wrapped io.ErrClosedPipe should classify as clean")
	}
}

// The per-connection raw bridge shares the session bridge's bounded head
// reader: one endless header line is refused near the 64 KiB cap.
func TestReadResponseHeadBoundsOneLongLine(t *testing.T) {
	src := io.MultiReader(strings.NewReader("HTTP/1.1 101 Switching Protocols\nX-Filler: "),
		strings.NewReader(strings.Repeat("a", 8<<20)), strings.NewReader("\n\n"))
	r := bufio.NewReader(src)
	_, _, err := readResponseHead(r)
	if err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("err = %v, want the head cap", err)
	}
	if rest, _ := io.Copy(io.Discard, r); rest < 8<<20-64*1024-2*4096 {
		t.Fatalf("the head reader consumed %d bytes of one header line", 8<<20+2-rest)
	}
}
