package fcvm

// adr: 022

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"io"
	"net"
	"os"
	"path/filepath"
	"testing"

	"github.com/onebox-faas/faas/pkg/extension"
)

func TestTriggerExtensionHookWire(t *testing.T) {
	base := shortChrootBase(t, "extension")
	instance := "i-extension"
	root := filepath.Join(base, "f", instance, "root")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	sockPath := filepath.Join(root, VsockUDSSocketName)
	listener, err := net.Listen("unix", sockPath)
	if err != nil {
		t.Fatalf("listen %s: %v", sockPath, err)
	}
	defer listener.Close()

	got := make(chan struct {
		typ      uint32
		phase    extension.Phase
		metadata map[string]string
	}, 1)
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		connect := make([]byte, 0, 32)
		one := []byte{0}
		for {
			if _, err := conn.Read(one); err != nil {
				return
			}
			connect = append(connect, one[0])
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
		body := make([]byte, binary.BigEndian.Uint32(hdr[4:8]))
		if _, err := io.ReadFull(conn, body); err != nil {
			return
		}
		var req struct {
			Phase    extension.Phase   `json:"phase"`
			Metadata map[string]string `json:"metadata"`
		}
		if json.Unmarshal(body, &req) != nil {
			return
		}
		got <- struct {
			typ      uint32
			phase    extension.Phase
			metadata map[string]string
		}{binary.BigEndian.Uint32(hdr[:4]), req.Phase, req.Metadata}
		_, _ = conn.Write([]byte{0})
	}()

	v := &JailerVMM{chrootBase: base, fcName: "f"}
	lease := Lease{Instance: instance}
	if err := v.TriggerExtensionHook(context.Background(), lease, string(extension.PhaseInvoke), map[string]string{
		"invocation_id": "inv-123",
		"source":        "gateway",
	}); err != nil {
		t.Fatalf("TriggerExtensionHook: %v", err)
	}
	select {
	case req := <-got:
		if req.typ != extensionHookMsgEvent {
			t.Fatalf("message type = %d, want %d", req.typ, extensionHookMsgEvent)
		}
		if req.phase != extension.PhaseInvoke || req.metadata["invocation_id"] != "inv-123" {
			t.Fatalf("request = %+v", req)
		}
	default:
		t.Fatal("extension request was not captured")
	}
}
