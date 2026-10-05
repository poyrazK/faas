//go:build linux

// adr: 590
package fcvm

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// This tests real procfs descriptors without claiming Firecracker acceptance.
func TestRuntimeDriveObserverLinuxProcessHandles(t *testing.T) {
	for _, mode := range []string{"exact", "wrong access", "wrong inode"} {
		t.Run(mode, func(t *testing.T) {
			f := newRuntimeDriveFixture(t)
			f.pin(t)
			handoff := f.measure(t)
			main := openRuntimeDriveProcessMain(t, f, mode)
			defer main.Close()
			cmd, uid := startRuntimeDriveProcess(t, handoff.drives[0].file, main)
			start, err := observeRuntimeDriveHandles(t.Context(), "/proc", cmd.Process.Pid, uid, handoff.drives)
			if (err == nil) != (mode == "exact") || mode == "exact" && start == "" {
				t.Fatalf("actual descriptor mode %q: start=%q err=%v", mode, start, err)
			}
			if err := cmd.Process.Kill(); err != nil {
				t.Fatal(err)
			}
			_ = cmd.Wait()
			if _, err := observeRuntimeDriveHandles(t.Context(), "/proc", cmd.Process.Pid, uid, handoff.drives); err == nil {
				t.Fatal("an exited process retained descriptor authority")
			}
		})
	}
}

func openRuntimeDriveProcessMain(t *testing.T, f runtimeDriveFixture, mode string) *os.File {
	t.Helper()
	path := filepath.Join(f.root, f.config.Drives[1].PathOnHost)
	flags := os.O_RDWR
	if mode == "wrong access" {
		flags = os.O_RDONLY
	} else if mode == "wrong inode" {
		path = filepath.Join(f.root, "unapproved.ext4")
		if err := os.WriteFile(path, []byte("main"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	file, err := os.OpenFile(path, flags, 0)
	if err != nil {
		t.Fatal(err)
	}
	return file
}

func startRuntimeDriveProcess(t *testing.T, base, main *os.File) (*exec.Cmd, int) {
	t.Helper()
	cmd := exec.Command("sleep", "30")
	cmd.ExtraFiles = []*os.File{base, main}
	uid := os.Getuid()
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if cmd.ProcessState == nil {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
		}
	})
	return cmd, uid
}
