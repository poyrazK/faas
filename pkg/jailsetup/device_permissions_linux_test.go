//go:build linux

package jailsetup

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"golang.org/x/sys/unix"
)

func TestDeviceNetTraversableWithPrivateUmask(t *testing.T) {
	// Umask is process-wide. Exercise the actual setup helper in a separate
	// process so parallel tests and the race detector retain their own mask.
	if os.Getenv("FAAS_TEST_PRIVATE_DEVICE_UMASK") == "1" {
		unix.Umask(0o077)
		dev := t.TempDir()
		if err := prepareDeviceNet(dev); err != nil {
			t.Fatal(err)
		}
		info, err := os.Stat(filepath.Join(dev, "net"))
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != 0o755 {
			t.Fatalf("device directory mode = %04o, jail UID cannot traverse", info.Mode().Perm())
		}
		// Setup must not adopt an existing directory or a symlink.
		if err := prepareDeviceNet(dev); err == nil {
			t.Fatal("adopted an existing device directory")
		}
		return
	}
	cmd := exec.Command(os.Args[0], "-test.run=^TestDeviceNetTraversableWithPrivateUmask$")
	cmd.Env = append(os.Environ(), "FAAS_TEST_PRIVATE_DEVICE_UMASK=1")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("private-mask device setup: %v\n%s", err, out)
	}
}
