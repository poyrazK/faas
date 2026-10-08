//go:build linux || darwin

package globalingress

// adr: 786
import (
	"os"
	"path/filepath"
	"testing"
)

func TestAtomicPublisherProtectedHandoff(t *testing.T) {
	path := filepath.Join(t.TempDir(), "routes.json")
	if err := CheckPublishDirectory(path); err != nil {
		t.Fatal(err)
	}
	for _, body := range []string{"first complete envelope", "second complete envelope"} {
		if err := WriteManifest(path, []byte(body)); err != nil {
			t.Fatal(err)
		}
		raw, err := ReadManifest(path)
		if err != nil || string(raw) != body {
			t.Fatal(string(raw), err)
		}
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0o640 {
		t.Fatal("unsafe mode", info, err)
	}
	if err = os.Remove(path); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(filepath.Dir(path), "target")
	if err = os.WriteFile(target, []byte("untouched"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err = os.Symlink(target, path); err != nil {
		t.Fatal(err)
	}
	if err = WriteManifest(path, []byte("bad")); err == nil {
		t.Fatal("symlink accepted")
	}
	if raw, err := os.ReadFile(target); err != nil || string(raw) != "untouched" {
		t.Fatal("symlink target modified")
	}
	if err = os.Chmod(filepath.Dir(path), 0o777); err != nil {
		t.Fatal(err)
	}
	if err = CheckPublishDirectory(path); err == nil {
		t.Fatal("writable directory accepted")
	}
}
