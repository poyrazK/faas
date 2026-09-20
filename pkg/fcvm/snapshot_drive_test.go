// adr: 005
package fcvm

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestFreezeSnapshotDriveUsesPrivateBackingAtPauseBoundary(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "private-layer.ext4")
	want := bytes.Repeat([]byte("snapshot-drive"), 4096)
	if err := os.WriteFile(source, want, 0o600); err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(dir, "jail")
	if err := os.Mkdir(root, 0o755); err != nil {
		t.Fatal(err)
	}
	mountpoint := filepath.Join(root, layerImageName)
	v := &JailerVMM{bindMounts: map[string][]ephemeralBind{
		"instance": {{source: source, mountpoint: mountpoint}},
	}}

	frozen, size, err := v.freezeSnapshotDrive(root, "instance")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Remove(frozen) }()
	if size != int64(len(want)) {
		t.Fatalf("size = %d, want %d", size, len(want))
	}
	if err := os.WriteFile(source, []byte("mutated-after-resume"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(frozen)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatal("frozen drive changed after the live backing was mutated")
	}
}
