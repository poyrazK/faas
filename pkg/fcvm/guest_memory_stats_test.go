package fcvm

// spec: §4.6 — the capture log explains snapshot content for the fleet snapshot-size target.

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/guestmemproto"
)

func TestGuestMemoryStatsWire(t *testing.T) {
	want := guestmemproto.Stats{MemTotal: 1 << 30, Cached: 120 << 20, AnonPages: 60 << 20}
	body, err := json.Marshal(want)
	if err != nil {
		t.Fatal(err)
	}
	ok := append([]byte{0, 0, 0, 0, 0}, body...)
	binary.BigEndian.PutUint32(ok[1:5], uint32(len(body)))
	for _, tc := range []struct {
		name  string
		reply []byte
		err   string
	}{
		{name: "current guest", reply: ok},
		{name: "older guest", reply: []byte{7}, err: "unsupported by guest"},
		{name: "oversized body", reply: []byte{0, 0xff, 0xff, 0xff, 0xff}, err: "out of range"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			base := shortChrootBase(t, "mem")
			instance := "i1"
			root := filepath.Join(base, "f", instance, "root")
			if err := os.MkdirAll(root, 0o755); err != nil {
				t.Fatal(err)
			}
			listener, err := net.Listen("unix", filepath.Join(root, VsockUDSSocketName))
			if err != nil {
				t.Fatal(err)
			}
			defer listener.Close()
			got := make(chan uint32, 1)
			go func() {
				conn, err := listener.Accept()
				if err != nil {
					return
				}
				defer conn.Close()
				for {
					var one [1]byte
					if _, err := io.ReadFull(conn, one[:]); err != nil {
						return
					}
					if one[0] == '\n' {
						break
					}
				}
				if _, err := conn.Write([]byte("OK 1024\n")); err != nil {
					return
				}
				var hdr [8]byte
				if _, err := io.ReadFull(conn, hdr[:]); err != nil {
					return
				}
				got <- binary.BigEndian.Uint32(hdr[:4])
				_, _ = conn.Write(tc.reply)
			}()
			v := &JailerVMM{chrootBase: base, fcName: "f"}
			stats, err := v.guestMemoryStats(context.Background(), Lease{Instance: instance})
			if tc.err == "" {
				if err != nil || stats != want {
					t.Fatalf("guestMemoryStats() = %+v, %v; want %+v", stats, err, want)
				}
			} else if err == nil || !strings.Contains(err.Error(), tc.err) {
				t.Fatalf("guestMemoryStats() error = %v, want %q", err, tc.err)
			}
			if typ := <-got; typ != guestmemproto.MessageType {
				t.Fatalf("message type = %d", typ)
			}
		})
	}
}
