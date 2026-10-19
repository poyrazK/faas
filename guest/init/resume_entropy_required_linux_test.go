//go:build linux

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

// ADR-022: host entropy is the input that makes two restores of one snapshot
// diverge. A resume without it must NACK, so vmmd cold-boots, instead of
// serving with the snapshot's random state.
func TestResumeWithoutEntropyNacks(t *testing.T) {
	ran := false
	orig := runResumeHookFn
	runResumeHookFn = func(int64, []byte) error { ran = true; return nil }
	t.Cleanup(func() { runResumeHookFn = orig })

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
	if frame[0] != VsockResumeAckEntropyLength {
		t.Fatalf("ack = %d, want %d (entropy NACK)", frame[0], VsockResumeAckEntropyLength)
	}
	if ran {
		t.Fatal("resume hook ran without host entropy")
	}
}

func TestAddHostEntropyRejectsEmpty(t *testing.T) {
	if err := addHostEntropy(nil); err == nil {
		t.Fatal("addHostEntropy(nil) succeeded; a restore would keep the snapshot's random state")
	}
}
