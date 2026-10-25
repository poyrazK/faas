package vmmdgrpc

import (
	"net"
	"os"
	"path/filepath"
	"testing"
)

func TestBridgeFenceRefusesActiveOwnerAndRemovesOnlyDeadSockets(t *testing.T) {
	dir, err := os.MkdirTemp("/tmp", "gregale-fence-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	path := filepath.Join(dir, "old.sock")
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	listener.SetUnlinkOnClose(false)
	if err := fenceStaleStreamBridges(t.Context(), dir); err == nil {
		t.Fatal("active old bridge accepted as fenced")
	}
	_ = listener.Close()
	regular := filepath.Join(dir, "retain.sock")
	if err := os.WriteFile(regular, []byte("fixture"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := fenceStaleStreamBridges(t.Context(), dir); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(path); !os.IsNotExist(err) {
		t.Fatalf("dead socket retained: %v", err)
	}
	if _, err := os.Lstat(regular); err != nil {
		t.Fatalf("non-socket removed: %v", err)
	}
}
