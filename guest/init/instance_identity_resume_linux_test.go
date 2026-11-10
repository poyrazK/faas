//go:build linux

// adr: 20261107160000
package main

import (
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/resumetimingproto"
	"golang.org/x/sys/unix"
)

// The resume hook publishes the identity it carries before acknowledging, so
// the first request after a restore already sees the serving instance.
func TestResumeHookPublishesInstanceIdentity(t *testing.T) {
	path := useInstanceIdentityPathForTest(t)
	if err := publishBootInstanceIdentity(map[string]string{api.PlatformInstanceIDEnv: "ins-capture", api.PlatformNodeIDEnv: "node-a"}, time.Now()); err != nil {
		t.Fatal(err)
	}
	origHook := runResumeHookFn
	runResumeHookFn = func(int64, []byte, *resumetimingproto.Timings) error { return nil }
	t.Cleanup(func() { runResumeHookFn = origHook })

	fds, err := unix.Socketpair(unix.AF_UNIX, unix.SOCK_STREAM, 0)
	if err != nil {
		t.Fatalf("socketpair: %v", err)
	}
	guest := os.NewFile(uintptr(fds[0]), "guest-resume")
	host := os.NewFile(uintptr(fds[1]), "host-resume")
	defer func() { _ = host.Close() }()
	body, _ := json.Marshal(map[string]any{
		"hostTimeUnixNano": 1,
		"entropy":          base64.StdEncoding.EncodeToString(make([]byte, 32)),
		"identity":         map[string]string{"instanceId": "ins-restored", "nodeId": "node-b", "region": "hel"},
	})
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
	if frame[0] != VsockResumeAckOK {
		t.Fatalf("ack = %d, want OK", frame[0])
	}
	got := readInstanceIdentityForTest(t, path)
	if got.InstanceID != "ins-restored" || got.NodeID != "node-b" || got.Region != "hel" || got.Source != "resume" {
		t.Fatalf("identity after resume = %+v, want ins-restored on node-b in hel", got)
	}
}
