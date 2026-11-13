package reseed

import (
	"bufio"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// barrier stands in for guest-init's reseed socket.
func barrier(t *testing.T) (string, <-chan net.Conn) {
	t.Helper()
	// A short directory: macOS caps Unix socket paths at 104 bytes.
	dir, err := os.MkdirTemp("", "rs")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	path := filepath.Join(dir, "s")
	ln, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	conns := make(chan net.Conn, 1)
	go func() {
		c, err := ln.Accept()
		if err == nil {
			conns <- c
		}
	}()
	return path, conns
}

func resetCallbacks(t *testing.T) {
	t.Helper()
	mu.Lock()
	saved := callbacks
	callbacks = nil
	mu.Unlock()
	t.Cleanup(func() {
		mu.Lock()
		callbacks = saved
		mu.Unlock()
	})
}

func TestRegistersAndRunsCallbacks(t *testing.T) {
	resetCallbacks(t)
	var seeds [][32]byte
	OnRestore(func(seed [32]byte) error { seeds = append(seeds, seed); return nil })
	path, conns := barrier(t)
	go serve(path)
	var conn net.Conn
	select {
	case conn = <-conns:
	case <-time.After(5 * time.Second):
		t.Fatal("no registration")
	}
	defer func() { _ = conn.Close() }()
	r := bufio.NewReader(conn)
	hello, _ := r.ReadString('\n')
	if !strings.HasPrefix(hello, "hello go ") {
		t.Fatalf("hello = %q", hello)
	}
	for i := 0; i < 2; i++ {
		_, _ = conn.Write([]byte("reseed " + strings.Repeat("ab", 32) + "\n"))
		if got, _ := r.ReadString('\n'); got != "ok\n" {
			t.Fatalf("reply = %q", got)
		}
	}
	if len(seeds) != 2 || seeds[0] == seeds[1] {
		t.Fatalf("seeds = %x, want two different seeds for one nonce", seeds)
	}
}

func TestReplyFailsClosed(t *testing.T) {
	resetCallbacks(t)
	nonce := strings.Repeat("01", 32)
	if got := reply("zz"); got != "err bad_nonce" {
		t.Fatalf("bad nonce: %q", got)
	}
	OnRestore(func([32]byte) error { return errors.New("no") })
	if got := reply(nonce); got != "err callback_0" {
		t.Fatalf("callback error: %q", got)
	}
	resetCallbacks(t)
	OnRestore(func([32]byte) error { panic("boom") })
	if got := reply(nonce); got != "err panic" {
		t.Fatalf("callback panic: %q", got)
	}
}
