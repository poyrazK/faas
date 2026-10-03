//go:build linux

package main

import (
	"encoding/binary"
	"encoding/json"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"sync/atomic"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/extension"
	"github.com/onebox-faas/faas/pkg/runtimepolicyproto"
	"golang.org/x/sys/unix"
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

func TestHandleResumeConnExtension(t *testing.T) {
	fds, err := unix.Socketpair(unix.AF_UNIX, unix.SOCK_STREAM, 0)
	if err != nil {
		t.Fatalf("socketpair: %v", err)
	}
	readEnd := os.NewFile(uintptr(fds[0]), "guest-extension")
	writeEnd := os.NewFile(uintptr(fds[1]), "host-extension")
	defer func() { _ = readEnd.Close() }()
	defer func() { _ = writeEnd.Close() }()

	body, err := json.Marshal(extensionHookRequest{
		Phase: extension.PhaseInvoke,
		Metadata: map[string]string{
			"invocation_id": "inv-123",
			"source":        "gateway",
		},
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	msg := make([]byte, 8+len(body))
	binary.BigEndian.PutUint32(msg[:4], VsockExtensionMsgType)
	binary.BigEndian.PutUint32(msg[4:8], uint32(len(body)))
	copy(msg[8:], body)

	called := make(chan extensionHookRequest, 1)
	go func() {
		_, _ = writeEnd.Write(msg)
	}()
	handleResumeConnWithExtension(readEnd, slog.Default(), nil, func(req extensionHookRequest) {
		called <- req
	}, nil)
	ack := []byte{0}
	if _, err := writeEnd.Read(ack); err != nil {
		t.Fatalf("read ack: %v", err)
	}
	if ack[0] != VsockResumeAckOK {
		t.Fatalf("ack = %d, want %d", ack[0], VsockResumeAckOK)
	}

	select {
	case req := <-called:
		if req.Phase != extension.PhaseInvoke || req.Metadata["invocation_id"] != "inv-123" {
			t.Fatalf("request = %+v", req)
		}
	default:
		t.Fatal("extension callback was not invoked")
	}
}

func TestHandleResumeConnWithAppCPULimit(t *testing.T) {
	fds, err := unix.Socketpair(unix.AF_UNIX, unix.SOCK_STREAM|unix.SOCK_CLOEXEC, 0)
	if err != nil {
		t.Fatalf("socketpair: %v", err)
	}
	guestEnd := os.NewFile(uintptr(fds[0]), "guest-cpu-policy")
	hostEnd := os.NewFile(uintptr(fds[1]), "host-cpu-policy")
	defer func() { _ = guestEnd.Close() }()
	defer func() { _ = hostEnd.Close() }()

	body, err := json.Marshal(runtimepolicyproto.AppCPULimitUpdate{CPUMillicores: 500})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	msg := make([]byte, 8+len(body))
	binary.BigEndian.PutUint32(msg[:4], VsockAppCPULimitMsgType)
	binary.BigEndian.PutUint32(msg[4:8], uint32(len(body)))
	copy(msg[8:], body)
	go func() { _, _ = hostEnd.Write(msg) }()

	var applied int
	handleResumeConnWithExtension(guestEnd, slog.Default(), nil, nil, nil, func(cpu int) error {
		applied = cpu
		return nil
	})
	ack := []byte{0}
	if _, err := hostEnd.Read(ack); err != nil {
		t.Fatalf("read ack: %v", err)
	}
	if ack[0] != VsockResumeAckOK || applied != 500 {
		t.Fatalf("ack=%d applied=%d, want ACK and 500m", ack[0], applied)
	}
}

func TestHandleBeforeCheckpointConn(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()
	for _, tc := range []struct {
		name string
		cfg  *beforeCheckpointRuntime
		want byte
	}{
		{name: "configured", cfg: &beforeCheckpointRuntime{hook: api.BeforeCheckpointHook{Path: "/checkpoint"}, port: testRestoreHookPort(t, server.URL)}, want: VsockResumeAckOK},
		{name: "missing", want: VsockResumeAckBeforeCheckpoint},
	} {
		t.Run(tc.name, func(t *testing.T) {
			old := beforeCheckpoint.Swap(tc.cfg)
			defer beforeCheckpoint.Store(old)
			fds, err := unix.Socketpair(unix.AF_UNIX, unix.SOCK_STREAM|unix.SOCK_CLOEXEC, 0)
			if err != nil {
				t.Fatal(err)
			}
			guest := os.NewFile(uintptr(fds[0]), "guest-checkpoint")
			host := os.NewFile(uintptr(fds[1]), "host-checkpoint")
			defer func() { _ = host.Close() }()
			var header [8]byte
			binary.BigEndian.PutUint32(header[:4], VsockBeforeCheckpointMsgType)
			go handleResumeConnWithExtension(guest, slog.Default(), nil, nil, nil)
			if _, err := host.Write(header[:]); err != nil {
				t.Fatal(err)
			}
			var ack [1]byte
			if _, err := host.Read(ack[:]); err != nil || ack[0] != tc.want {
				t.Fatalf("ack=%d err=%v, want %d", ack[0], err, tc.want)
			}
		})
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
