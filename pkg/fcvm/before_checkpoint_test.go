package fcvm

// adr: 343 — the guest callback must acknowledge before a terminal init capture.

import (
	"context"
	"encoding/binary"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestTriggerBeforeCheckpointWire(t *testing.T) {
	for _, tc := range []struct {
		name string
		ack  byte
		want string
	}{
		{name: "success"},
		{name: "callback failed", ack: beforeCheckpointHookAckFailed, want: "application callback failed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			base := shortChrootBase(t, "chk")
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
				if binary.BigEndian.Uint32(hdr[4:]) != 0 {
					return
				}
				got <- binary.BigEndian.Uint32(hdr[:4])
				_, _ = conn.Write([]byte{tc.ack})
			}()
			v := &JailerVMM{chrootBase: base, fcName: "f"}
			err = v.TriggerBeforeCheckpoint(context.Background(), Lease{Instance: instance})
			if tc.want == "" && err != nil || tc.want != "" && (err == nil || !strings.Contains(err.Error(), tc.want)) {
				t.Fatalf("TriggerBeforeCheckpoint() = %v, want %q", err, tc.want)
			}
			if typ := <-got; typ != beforeCheckpointHookMsg {
				t.Fatalf("message type = %d", typ)
			}
		})
	}
}
