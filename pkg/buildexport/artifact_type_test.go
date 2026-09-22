package buildexport

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

// ADR-145's recoverable image handoff must not hang a consumer or cleanup
// worker when an incomplete/malformed export is not a regular OCI archive.
func TestArtifactOperationsRejectNonRegularFiles(t *testing.T) {
	for _, kind := range []string{"fifo", "directory", "symlink", "dangling symlink"} {
		for _, operation := range []string{"acquire", "sweep"} {
			t.Run(kind+"/"+operation, func(t *testing.T) {
				root := t.TempDir()
				artifact := filepath.Join(root, "bad-export", "build", "out", "image.tar")
				if err := os.MkdirAll(filepath.Dir(artifact), 0o700); err != nil {
					t.Fatal(err)
				}
				switch kind {
				case "fifo":
					if err := syscall.Mkfifo(artifact, 0o600); err != nil {
						t.Fatal(err)
					}
				case "directory":
					if err := os.Mkdir(artifact, 0o700); err != nil {
						t.Fatal(err)
					}
				default:
					target := filepath.Join(t.TempDir(), "target")
					if kind == "symlink" {
						if err := os.WriteFile(target, []byte("not an export"), 0o600); err != nil {
							t.Fatal(err)
						}
					}
					if err := os.Symlink(target, artifact); err != nil {
						t.Fatal(err)
					}
				}
				done := make(chan string, 1)
				go func() {
					if operation == "acquire" {
						lease, ok, err := AcquireArtifact(artifact)
						_ = lease.Close()
						if lease != nil || !ok || err == nil {
							done <- fmt.Sprintf("AcquireArtifact = (%v, %v, %v), want canonical-path rejection", lease, ok, err)
							return
						}
					} else {
						result, err := Sweep(context.Background(), SweepOptions{
							Root: root,
							Resolve: func(context.Context, string, string) (ReferenceState, error) {
								return ReferenceReleased, nil
							},
						})
						if err != nil || result.Errors != 1 || result.Removed != 0 {
							done <- fmt.Sprintf("Sweep = (%+v, %v), want error and retention", result, err)
							return
						}
					}
					done <- ""
				}()
				select {
				case failure := <-done:
					if failure != "" {
						t.Fatal(failure)
					}
				case <-time.After(time.Second):
					// Release a regressed blocking FIFO open before failing, so
					// the test never leaves a blocked worker behind.
					if kind == "fifo" {
						writer, err := os.OpenFile(artifact, os.O_RDWR|syscall.O_NONBLOCK, 0)
						if err != nil {
							t.Fatal(err)
						}
						defer writer.Close()
						select {
						case <-done:
						case <-time.After(time.Second):
							t.Fatal("artifact operation did not finish after FIFO rescue")
						}
					}
					t.Fatal("artifact operation blocked before acquiring its non-blocking lease")
				}
				if _, err := os.Lstat(artifact); err != nil {
					t.Fatalf("malformed export was removed: %v", err)
				}
			})
		}
	}
}

func TestSweepStillRemovesReleasedExportWithoutArtifact(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "empty-export", "build", "out")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	result, err := Sweep(context.Background(), SweepOptions{
		Root: root,
		Resolve: func(context.Context, string, string) (ReferenceState, error) {
			return ReferenceReleased, nil
		},
	})
	if err != nil || result.Errors != 0 || result.Removed != 1 {
		t.Fatalf("empty released export: Sweep = (%+v, %v)", result, err)
	}
}
