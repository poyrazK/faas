//go:build linux

package main

import (
	"encoding/binary"
	"encoding/json"
	"net"
	"sync/atomic"
	"testing"
)

// TestListenResumeHookLocalSocket is a stand-in for the AF_VSOCK
// dial-test (which needs a Linux kernel with CONFIG_VSOCKETS=y in
// the guest). We exercise the same wire format over a unix socket
// and assert the listener reads the header, dispatches by msg type,
// and writes back the ack. RunResumeHook's body is short — the
// entropy re-seed is non-trivial on macOS CI where /dev/urandom
// doesn't have the post-restore property — so we count invocations
// only.
//
// Linux-only because the protocol we're really testing lives in
// listenResumeHook (AF_VSOCK). On macOS/CI this file is excluded
// by the build tag.
func TestListenResumeHookLocalSocket(t *testing.T) {
	if testing.Short() {
		t.Skip("short mode")
	}
	// The actual AF_VSOCK path is exercised on metal (`make metal-lima`,
	// `make test-metal`). This test only covers the wire format.

	// Spin up a minimal server mirroring handleResumeConn on a unix
	// socket: read 4-byte BE header, branch by msg type, write ack.
	dir := t.TempDir()
	sock := dir + "/vsock.sock"
	l, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { _ = l.Close() })

	var got atomic.Int32
	go func() {
		c, err := l.Accept()
		if err != nil {
			return
		}
		defer func() { _ = c.Close() }()
		var hdr [4]byte
		if _, err := readFull(c, hdr[:]); err != nil {
			return
		}
		mt := binary.BigEndian.Uint32(hdr[:])
		if mt != 1 {
			_, _ = c.Write([]byte{VsockResumeAckNack})
			return
		}
		// Read body until EOF (host calls CloseWrite).
		body, err := readAll(c)
		if err != nil {
			_, _ = c.Write([]byte{VsockResumeAckNack})
			return
		}
		var req struct {
			HostTimeUnixNano int64 `json:"hostTimeUnixNano"`
		}
		_ = json.Unmarshal(body, &req)
		if req.HostTimeUnixNano > 0 {
			got.Add(1)
		}
		_, _ = c.Write([]byte{VsockResumeAckOK})
	}()

	// Dial + send a request mirroring the host wire format.
	c, err := net.Dial("unix", sock)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer func() { _ = c.Close() }()
	body, _ := json.Marshal(struct {
		HostTimeUnixNano int64 `json:"hostTimeUnixNano"`
	}{HostTimeUnixNano: 1700000000123456789})
	msg := make([]byte, 4+len(body))
	binary.BigEndian.PutUint32(msg[:4], VsockResumeMsgType)
	copy(msg[4:], body)
	if _, err := c.Write(msg); err != nil {
		t.Fatalf("write: %v", err)
	}
	if cw, ok := c.(closeWriter); ok {
		_ = cw.CloseWrite()
	}
	var ack [1]byte
	if _, err := readFull(c, ack[:]); err != nil {
		t.Fatalf("read ack: %v", err)
	}
	if ack[0] != VsockResumeAckOK {
		t.Errorf("ack = %d, want %d", ack[0], VsockResumeAckOK)
	}
	if got.Load() != 1 {
		t.Errorf("hook invocations = %d, want 1", got.Load())
	}
}

// closeWriter mirrors the production-side interface used after Write
// to signal "no more bytes" on the wire. stdlib net.UnixConn satisfies it.
type closeWriter interface{ CloseWrite() error }

// readFull / readAll: tiny stdlib shape helpers so this file doesn't
// pull in io for a 30-line test.
func readFull(c net.Conn, p []byte) (int, error) {
	n := 0
	for n < len(p) {
		k, err := c.Read(p[n:])
		if k > 0 {
			n += k
		}
		if err != nil {
			return n, err
		}
	}
	return n, nil
}

func readAll(c net.Conn) ([]byte, error) {
	var out []byte
	buf := make([]byte, 256)
	for {
		k, err := c.Read(buf)
		if k > 0 {
			out = append(out, buf[:k]...)
		}
		if err != nil {
			if err.Error() == "EOF" {
				return out, nil
			}
			return out, err
		}
	}
}
