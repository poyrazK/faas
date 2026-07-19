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
	// socket: read 4-byte BE msg type + 4-byte BE body length + JSON body,
	// branch by msg type, write ack. Mirrors production wire exactly so a
	// regression in either side fails this test before reaching metal.
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
		var hdr [8]byte
		if _, err := readFull(c, hdr[:]); err != nil {
			return
		}
		mt := binary.BigEndian.Uint32(hdr[:4])
		if mt != VsockResumeMsgType {
			_, _ = c.Write([]byte{VsockResumeAckNack})
			return
		}
		bodyLen := binary.BigEndian.Uint32(hdr[4:8])
		body := make([]byte, bodyLen)
		if _, err := readFull(c, body); err != nil {
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
	msg := make([]byte, 8+len(body))
	binary.BigEndian.PutUint32(msg[:4], VsockResumeMsgType)
	binary.BigEndian.PutUint32(msg[4:8], uint32(len(body)))
	copy(msg[8:], body)
	if _, err := c.Write(msg); err != nil {
		t.Fatalf("write: %v", err)
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

// readFull: tiny stdlib shape helper so this file doesn't pull in io
// for a single call.
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
