//go:build linux

package main

import (
	"encoding/binary"
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/onebox-faas/faas/pkg/guestmemproto"
	"golang.org/x/sys/unix"
)

func TestMemoryStatsReplyFraming(t *testing.T) {
	path := filepath.Join(t.TempDir(), "meminfo")
	if err := os.WriteFile(path, []byte("MemTotal: 1024 kB\nCached: 512 kB\nAnonPages: 128 kB\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	reply, err := memoryStatsReply(path)
	if err != nil {
		t.Fatal(err)
	}
	if reply[0] != VsockResumeAckOK || int(binary.BigEndian.Uint32(reply[1:5])) != len(reply)-5 {
		t.Fatalf("bad framing: % x", reply[:5])
	}
	var got guestmemproto.Stats
	if err := json.Unmarshal(reply[5:], &got); err != nil {
		t.Fatal(err)
	}
	if got.MemTotal != 1024<<10 || got.Cached != 512<<10 || got.AnonPages != 128<<10 {
		t.Fatalf("stats = %+v", got)
	}
}

// The live handler reads the real /proc/meminfo and answers over the shared
// resume listener dispatch.
func TestHandleResumeConnMemoryStats(t *testing.T) {
	fds, err := unix.Socketpair(unix.AF_UNIX, unix.SOCK_STREAM|unix.SOCK_CLOEXEC, 0)
	if err != nil {
		t.Fatalf("socketpair: %v", err)
	}
	guestEnd := os.NewFile(uintptr(fds[0]), "guest-memory-stats")
	hostEnd := os.NewFile(uintptr(fds[1]), "host-memory-stats")
	defer func() { _ = hostEnd.Close() }()

	var msg [8]byte
	binary.BigEndian.PutUint32(msg[:4], guestmemproto.MessageType)
	go func() { _, _ = hostEnd.Write(msg[:]) }()
	handleResumeConnWithExtension(guestEnd, slog.Default(), nil, nil, nil)

	reply, err := io.ReadAll(hostEnd)
	if err != nil {
		t.Fatal(err)
	}
	if len(reply) < 5 || reply[0] != VsockResumeAckOK {
		t.Fatalf("reply = % x", reply)
	}
	var got guestmemproto.Stats
	if err := json.Unmarshal(reply[5:], &got); err != nil || got.MemTotal <= 0 {
		t.Fatalf("stats = %+v, err = %v", got, err)
	}
}
