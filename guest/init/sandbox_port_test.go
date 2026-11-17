// adr: 20261027090013 §11 — port_connect splices the host's data frames
// with a TCP server on the guest's loopback, half-closes it at data-end,
// and answers port_closed when nothing listens; guest-init's own ports
// cannot be named.
package main

import (
	"bufio"
	"io"
	"net"
	"strconv"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/sandboxproto"
)

func TestSandboxPortConnect(t *testing.T) {
	f := newSandboxFixture(t)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer func() { _ = conn.Close() }()
				// An HTTP/1.0-style exchange: read to EOF, then answer.
				body, _ := io.ReadAll(bufio.NewReader(conn))
				_, _ = conn.Write([]byte("echo:" + strings.ToUpper(string(body))))
			}()
		}
	}()
	port := ln.Addr().(*net.TCPAddr).Port
	r := f.call(t, sandboxproto.Request{Op: sandboxproto.OpPortConnect, Port: port}, strings.NewReader("hello"), nil)
	var res sandboxproto.PortResult
	r.decode(t, &res)
	if got := r.data.String(); got != "echo:HELLO" || res.BytesOut != int64(len("echo:HELLO")) {
		t.Fatalf("data %q, result %+v", got, res)
	}

	free, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	closed := free.Addr().(*net.TCPAddr).Port
	_ = free.Close()
	if code := f.call(t, sandboxproto.Request{Op: sandboxproto.OpPortConnect, Port: closed}, strings.NewReader(""), nil).code(); code != sandboxproto.CodePortClosed {
		t.Fatalf("closed port answered %q", code)
	}
}

func TestPreviewPortValidation(t *testing.T) {
	for _, port := range []int{0, -1, 65536, 2774, 9191} {
		if sandboxproto.ValidPreviewPort(port) == nil {
			t.Errorf("port %d accepted", port)
		}
	}
	for _, port := range []int{80, 3000, 8080, 65535} {
		if err := sandboxproto.ValidPreviewPort(port); err != nil {
			t.Errorf("port %s refused: %v", strconv.Itoa(port), err)
		}
	}
}
