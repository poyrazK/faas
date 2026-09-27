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

func TestUpdateAppWorkloadCPULimitWire(t *testing.T) {
	base := shortChrootBase(t, "cpu")
	instance := "i1"
	root := filepath.Join(base, "f", instance, "root")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("unix", filepath.Join(root, VsockUDSSocketName))
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer listener.Close()

	type received struct {
		typ uint32
		cpu int
	}
	got := make(chan received, 1)
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
		if string(connect) != "CONNECT 1024\n" {
			return
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
			CPUMillicores int `json:"cpu_millicores"`
		}
		if json.Unmarshal(body, &req) != nil {
			return
		}
		got <- received{typ: binary.BigEndian.Uint32(hdr[:4]), cpu: req.CPUMillicores}
		_, _ = conn.Write([]byte{0})
	}()

	vmm := &JailerVMM{chrootBase: base, fcName: "f"}
	if err := vmm.UpdateAppWorkloadCPULimit(context.Background(), Lease{Instance: instance}, 500); err != nil {
		t.Fatalf("UpdateAppWorkloadCPULimit: %v", err)
	}
	select {
	case request := <-got:
		if request.typ != appCPULimitHookMsgType || request.cpu != 500 {
			t.Fatalf("request = %+v, want type %d at 500m", request, appCPULimitHookMsgType)
		}
	default:
		t.Fatal("app CPU policy request was not captured")
	}
}
