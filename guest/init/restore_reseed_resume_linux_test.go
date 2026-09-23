//go:build linux

// adr: 222
package main

import (
	"encoding/binary"
	"encoding/json"
	"log/slog"
	"os"
	"testing"

	"golang.org/x/sys/unix"
)

// The resume ACK is the gate before an instance serves traffic, so a process
// that fails its userspace reseed must turn it into NACK 13, which makes vmmd
// cold-boot instead (ADR-005).
func TestResumeAckWaitsForUserspaceReseed(t *testing.T) {
	cases := []struct {
		name    string
		reply   string
		wantAck byte
	}{
		{name: "every process confirms", reply: "ok", wantAck: VsockResumeAckOK},
		{name: "a process cannot reseed", reply: "err openssl_reseed_unavailable", wantAck: VsockResumeAckUserspaceReseed},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			origHook := runResumeHookFn
			runResumeHookFn = func(int64, []byte) error { return nil }
			t.Cleanup(func() { runResumeHookFn = origHook })

			b, sock := startTestBarrier(t)
			(&scriptedClient{}).dial(t, sock, "hello node 42", func(int) string { return tc.reply })
			waitRegistered(t, b, 1)
			activeRestoreReseedBarrier.Store(b)
			t.Cleanup(func() { activeRestoreReseedBarrier.Store(nil) })

			fds, err := unix.Socketpair(unix.AF_UNIX, unix.SOCK_STREAM, 0)
			if err != nil {
				t.Fatalf("socketpair: %v", err)
			}
			guest := os.NewFile(uintptr(fds[0]), "guest-resume")
			host := os.NewFile(uintptr(fds[1]), "host-resume")
			defer func() { _ = host.Close() }()
			body, _ := json.Marshal(map[string]any{"hostTimeUnixNano": 1})
			msg := make([]byte, 8+len(body))
			binary.BigEndian.PutUint32(msg[:4], VsockResumeMsgType)
			binary.BigEndian.PutUint32(msg[4:8], uint32(len(body)))
			copy(msg[8:], body)
			go func() { _, _ = host.Write(msg) }()

			handleResumeConn(guest, slog.Default())
			ack := make([]byte, 1)
			if _, err := host.Read(ack); err != nil {
				t.Fatalf("read ack: %v", err)
			}
			if ack[0] != tc.wantAck {
				t.Fatalf("ack = %d, want %d", ack[0], tc.wantAck)
			}
		})
	}
}
