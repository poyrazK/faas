//go:build linux || darwin

package scanview

import (
	"bytes"
	"errors"
	"io/fs"
	"net"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

func TestTreeRefusesFIFOAndUnixSocket(t *testing.T) {
	for _, kind := range []string{"fifo", "socket"} {
		t.Run(kind, func(t *testing.T) {
			// Keep the Unix socket pathname below the native sockaddr limit.
			root, err := os.MkdirTemp("", "sv-")
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = os.RemoveAll(root) })
			name := filepath.Join(root, "node")
			if kind == "fifo" {
				if err := syscall.Mkfifo(name, 0o600); err != nil {
					t.Fatal(err)
				}
			} else {
				listener, err := net.Listen("unix", name)
				if err != nil {
					t.Fatal(err)
				}
				defer listener.Close()
			}
			if _, err := Snapshot(t.Context(), root); !errors.Is(err, ErrInvalid) {
				t.Fatal("special node acquired complete tree identity", err)
			}
		})
	}
}

func TestRegularReadRefusesReplacedFIFOAndSymlink(t *testing.T) {
	for _, kind := range []string{"fifo", "symlink"} {
		t.Run(kind, func(t *testing.T) {
			root := t.TempDir()
			viewFile(t, root, "file", "original")
			name := filepath.Join(root, "file")
			info, err := os.Lstat(name)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.Remove(name); err != nil {
				t.Fatal(err)
			}
			if kind == "fifo" {
				if err := syscall.Mkfifo(name, 0o600); err != nil {
					t.Fatal(err)
				}
			} else {
				host := t.TempDir()
				viewFile(t, host, "secret", "host-only bytes")
				viewLink(t, root, "file", filepath.Join(host, "secret"))
			}
			assertReplacedReadRefused(t, root, info)
		})
	}
}

func assertReplacedReadRefused(t *testing.T, dir string, info fs.FileInfo) {
	t.Helper()
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	var output bytes.Buffer
	done := make(chan error, 1)
	go func() {
		_, err := readRegular(t.Context(), root, "file", info, &output)
		done <- err
	}()
	select {
	case err := <-done:
		if err == nil || output.Len() != 0 {
			t.Fatal("replacement was read", err)
		}
	case <-time.After(time.Second):
		// Release an accidentally blocking FIFO opener before failing the test.
		if fd, err := syscall.Open(filepath.Join(dir, "file"), syscall.O_WRONLY|syscall.O_NONBLOCK, 0); err == nil {
			_ = syscall.Close(fd)
		}
		t.Fatal("replacement blocked the bounded reader")
	}
}
