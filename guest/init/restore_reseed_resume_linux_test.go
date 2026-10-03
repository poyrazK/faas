//go:build linux

// adr: 481
package main

import (
	"encoding/binary"
	"encoding/json"
	"io"
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
			frame, err := io.ReadAll(host)
			if err != nil || len(frame) == 0 {
				t.Fatalf("read ack frame: %v (%d bytes)", err, len(frame))
			}
			if frame[0] != tc.wantAck {
				t.Fatalf("ack = %d, want %d", frame[0], tc.wantAck)
			}
			wantCap := tc.wantAck == VsockResumeAckOK
			if gotCap := len(frame) == 2 && frame[1] == VsockResumeCapUserspaceReseed; gotCap != wantCap {
				t.Fatalf("ack frame %v: reseed capability = %v, want %v", frame, gotCap, wantCap)
			}
		})
	}
}

func TestResumeCapabilityAdvertisement(t *testing.T) {
	cases := []struct {
		name        string
		barrier     bool
		warmBuilder bool
		want        bool
	}{
		{name: "app guest with barrier", barrier: true, want: true},
		{name: "app guest whose barrier never started", want: false},
		{name: "warm builder", warmBuilder: true, want: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			origBuilder := warmBuilderEnabled.Load()
			t.Cleanup(func() {
				activeRestoreReseedBarrier.Store(nil)
				warmBuilderEnabled.Store(origBuilder)
			})
			activeRestoreReseedBarrier.Store(nil)
			if tc.barrier {
				activeRestoreReseedBarrier.Store(newRestoreReseedBarrier(nil))
			}
			warmBuilderEnabled.Store(tc.warmBuilder)
			if got := restoreReseedContractHolds(); got != tc.want {
				t.Fatalf("restoreReseedContractHolds() = %v, want %v", got, tc.want)
			}
		})
	}
}
