package fcvm

// adr: 20261025150000 — a guest without an extension endpoint answers with a
// distinct ack, which TriggerExtensionHook reports as extension.ErrUnavailable.

import (
	"context"
	"encoding/binary"
	"io"
	"net"
	"os"
	"path/filepath"
	"testing"

	"github.com/onebox-faas/faas/pkg/extension"
)

// serveOneExtensionAck accepts one extension hook on the instance's vsock
// socket, completes the CONNECT handshake and answers with ack.
func serveOneExtensionAck(t *testing.T, base, instance string, ack byte) {
	t.Helper()
	root := filepath.Join(base, "f", instance, "root")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("unix", filepath.Join(root, VsockUDSSocketName))
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		one := []byte{0}
		for one[0] != '\n' {
			if _, err := conn.Read(one); err != nil {
				return
			}
		}
		if _, err := conn.Write([]byte("OK 1024\n")); err != nil {
			return
		}
		var hdr [8]byte
		if _, err := io.ReadFull(conn, hdr[:]); err != nil {
			return
		}
		if _, err := io.CopyN(io.Discard, conn, int64(binary.BigEndian.Uint32(hdr[4:8]))); err != nil {
			return
		}
		_, _ = conn.Write([]byte{ack})
	}()
}

func TestTriggerExtensionHookAck(t *testing.T) {
	cases := []struct {
		name            string
		ack             byte
		wantErr         bool
		wantUnavailable bool
	}{
		{name: "extension accepted", ack: 0},
		{name: "no extension endpoint", ack: extensionHookAckUnavailable, wantErr: true, wantUnavailable: true},
		{name: "malformed event", ack: 10, wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			base := shortChrootBase(t, "extack")
			serveOneExtensionAck(t, base, "i-ext", tc.ack)
			v := &JailerVMM{chrootBase: base, fcName: "f"}
			err := v.TriggerExtensionHook(context.Background(), Lease{Instance: "i-ext"}, string(extension.PhaseInvoke),
				map[string]string{"invocation_id": "inv-1"})
			if (err != nil) != tc.wantErr {
				t.Fatalf("err = %v, want error %v", err, tc.wantErr)
			}
			if got := extension.IsUnavailable(err); got != tc.wantUnavailable {
				t.Fatalf("IsUnavailable(%v) = %v, want %v", err, got, tc.wantUnavailable)
			}
		})
	}
}
