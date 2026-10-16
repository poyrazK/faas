//go:build linux

package cirunner

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func TestRunnerCannotExecuteOnHost(t *testing.T) {
	t.Setenv("FAAS_JOB", "0")
	if code, err := RunGuest(t.Context(), []string{"v1", "invalid"}); err == nil || code != 125 {
		t.Fatal("guest entrypoint accepted host invocation")
	}
}

func TestGuestArtifactOpenRejectsSpecialFiles(t *testing.T) {
	dir := t.TempDir()
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = root.Close() }()
	if err := root.WriteFile("regular", []byte("report"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := root.Symlink("regular", "link"); err != nil {
		t.Fatal(err)
	}
	if err := root.Mkdir("reports", 0o700); err != nil {
		t.Fatal(err)
	}
	if err := root.Symlink("reports", "linked-reports"); err != nil {
		t.Fatal(err)
	}
	if err := root.WriteFile("reports/result", []byte("report"), 0o600); err != nil {
		t.Fatal(err)
	}
	fifo := filepath.Join(dir, "fifo")
	if err := unix.Mkfifo(fifo, 0o600); err != nil {
		t.Fatal(err)
	}
	file, err := openArtifact(root, "regular")
	if err != nil {
		t.Fatal(err)
	}
	_ = file.Close()
	for _, name := range []string{"link", "linked-reports/result", "fifo", ".", "../regular"} {
		t.Run(name, func(t *testing.T) {
			done := make(chan error, 1)
			go func() {
				file, err := openArtifact(root, name)
				if file != nil {
					_ = file.Close()
				}
				done <- err
			}()
			select {
			case err := <-done:
				if err == nil {
					t.Fatal("unsafe artifact accepted")
				}
			case <-time.After(time.Second):
				// Unblock a regressed FIFO opener before reporting the failure.
				fd, err := unix.Open(fifo, unix.O_RDWR|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
				if err == nil {
					_ = unix.Close(fd)
				}
				t.Fatal("artifact open blocked finalization")
			}
		})
	}
}
