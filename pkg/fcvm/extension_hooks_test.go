package fcvm

import (
	"encoding/binary"
	"io"
	"net"
	"os"
	"path/filepath"
	"testing"
)

func TestTriggerPreSnapshotHookUsesLifecycleMessage(t *testing.T) {
	base := shortChrootBase(t, "presnapshot")
	instance := "pre-snapshot"
	root := filepath.Join(base, "f", instance, "root")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	sockPath := filepath.Join(root, VsockUDSSocketName)
	listener, err := net.Listen("unix", sockPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	seen := make(chan uint32, 1)
	go func() {
		conn, acceptErr := listener.Accept()
		if acceptErr != nil {
			return
		}
		defer func() { _ = conn.Close() }()
		buf := make([]byte, 0, 32)
		one := []byte{0}
		for {
			if _, readErr := conn.Read(one); readErr != nil {
				return
			}
			buf = append(buf, one[0])
			if one[0] == '\n' {
				break
			}
		}
		if _, writeErr := conn.Write([]byte("OK 1073741824\n")); writeErr != nil {
			return
		}
		var header [8]byte
		if _, readErr := io.ReadFull(conn, header[:]); readErr != nil {
			return
		}
		seen <- binary.BigEndian.Uint32(header[:4])
		if binary.BigEndian.Uint32(header[4:8]) != 0 {
			return
		}
		_, _ = conn.Write([]byte{0})
	}()

	v := &JailerVMM{chrootBase: base, fcName: "f"}
	if err := v.TriggerPreSnapshotHook(t.Context(), Lease{Instance: instance}); err != nil {
		t.Fatalf("TriggerPreSnapshotHook: %v", err)
	}
	if got := <-seen; got != resumeHookMsgPreSnapshot {
		t.Fatalf("message type = %d, want %d", got, resumeHookMsgPreSnapshot)
	}
}
