package main_test

import (
	"archive/tar"
	"bytes"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/onebox-faas/faas/cmd/gregale"
)

// Ctrl-C during `app cp` used to be swallowed until the whole transfer had
// arrived: nothing watched the interrupt context while the copy read the
// WebSocket. The fake guest streams without end and raises SIGINT once the
// copy is under way; the command must close the session and exit 130.
func TestAppCopyCtrlCStopsTheTransfer(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("process interruption is unsupported on Windows")
	}
	chunk := make([]byte, 32<<10)
	cases := []struct {
		name   string
		local  func(t *testing.T) string
		stream func(stdout io.Writer) (io.Writer, error)
	}{
		{
			name:   "stdout",
			local:  func(*testing.T) string { return "-" },
			stream: func(stdout io.Writer) (io.Writer, error) { return stdout, nil },
		},
		{
			name:  "archive",
			local: func(t *testing.T) string { return filepath.Join(t.TempDir(), "copy") },
			stream: func(stdout io.Writer) (io.Writer, error) {
				archive := tar.NewWriter(stdout)
				return archive, archive.WriteHeader(&tar.Header{Name: "big.bin", Mode: 0o644, Size: 1 << 40, Typeflag: tar.TypeReg})
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var interrupt sync.Once
			server, _ := fakeSessionGateway(t, func(_ io.Reader, stdout io.Writer) {
				body, err := tc.stream(stdout)
				if err != nil {
					t.Error(err)
					return
				}
				for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); {
					if _, err := body.Write(chunk); err != nil {
						return
					}
					interrupt.Do(func() {
						if err := raiseInterrupt(); err != nil {
							t.Error(err)
						}
					})
				}
			})
			t.Setenv("FAAS_API", server.URL)
			t.Setenv("FAAS_TOKEN", "fp_test_copy")
			var stderr bytes.Buffer
			oldOut, oldErr := *main.OsStdout, *main.OsStderr
			*main.OsStdout, *main.OsStderr = io.Discard, &stderr
			t.Cleanup(func() { *main.OsStdout, *main.OsStderr = oldOut, oldErr })

			done := make(chan int, 1)
			go func() { done <- main.CmdAppCopy("api", []string{"/app/big", tc.local(t)}) }()
			select {
			case code := <-done:
				if code != 130 || !strings.Contains(stderr.String(), "Copy interrupted") {
					t.Fatalf("exit = %d, stderr = %q; want 130 and the interrupt notice", code, stderr.String())
				}
			case <-time.After(8 * time.Second):
				t.Fatal("app cp kept copying after Ctrl-C")
			}
		})
	}
}

func raiseInterrupt() error {
	process, err := os.FindProcess(os.Getpid())
	if err != nil {
		return err
	}
	return process.Signal(os.Interrupt)
}
