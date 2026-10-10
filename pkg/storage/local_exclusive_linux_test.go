//go:build linux

// adr: 568 — real disk publication tests do not complete native VM capture.
package storage

import (
	"bytes"
	"context"
	"errors"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"

	"golang.org/x/sys/unix"
)

func exclusiveLocalFixture(t *testing.T) *LocalStorageBackend {
	t.Helper()
	backend, err := NewLocalStorageBackend(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(backend.root, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := backend.CheckExclusivePut(t.Context(), "probe/artifact"); errors.Is(err, ErrExclusivePutUnsupported) {
		t.Skip("real exclusive disk publication requires Linux openat2 and ext4/XFS/Btrfs", err)
	} else if err != nil {
		t.Fatal(err)
	}
	return backend
}

func assertNoExclusiveArtifacts(t *testing.T, root string) {
	t.Helper()
	if err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !entry.IsDir() {
			t.Error("uncommitted exclusive producer left a named artifact", path)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

type exclusiveObservedReader struct {
	io.Reader
	observe func()
}

func (r exclusiveObservedReader) Read(p []byte) (int, error) {
	r.observe()
	return r.Reader.Read(p)
}

func (exclusiveObservedReader) Name() string {
	panic("exclusive publication must not reopen the source name")
}

func TestLocalExclusivePublicationNamesOnlyCompleteSparseArtifact(t *testing.T) {
	backend := exclusiveLocalFixture(t)
	key := exclusiveCapturePrefix + "drive"
	if entries, err := os.ReadDir(backend.root); err != nil || len(entries) != 0 {
		t.Fatal("capability preflight created destination paths", entries, err)
	}
	body := make([]byte, 4<<20)
	copy(body, "original-private-drive")
	body[len(body)-1] = 42
	source := exclusiveObservedReader{Reader: bytes.NewReader(body), observe: func() { assertNoExclusiveArtifacts(t, backend.root) }}
	if err := PutExclusive(t.Context(), backend, key, source, int64(len(body))); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(backend.root, key)
	actual, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(actual, body) {
		t.Fatal("exclusive publication changed its original bytes", err)
	}
	var stat unix.Stat_t
	if err := unix.Lstat(path, &stat); err != nil || stat.Mode&0o7777 != 0o644 || stat.Nlink != 1 || stat.Blocks*512 > int64(len(body))/10 {
		t.Fatal("exclusive publication lost its immutable file/sparse contract", stat, err)
	}
	if err := PutExclusive(t.Context(), backend, key, bytes.NewReader([]byte("replacement")), 11); !errors.Is(err, ErrArtifactExists) {
		t.Fatal("exclusive publication overwrote an existing artifact", err)
	}
	unchanged, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(unchanged, body) {
		t.Fatal("failed replacement changed the original artifact", err)
	}
}

func TestLocalExclusivePublicationFailuresLeaveNoTemporaryNames(t *testing.T) {
	for _, outcome := range []string{"short", "long", "last_bytes_error", "cancelled"} {
		t.Run(outcome, func(t *testing.T) {
			backend := exclusiveLocalFixture(t)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			var source io.Reader = bytes.NewReader([]byte("abc"))
			var size int64 = 3
			switch outcome {
			case "short":
				size++
			case "long":
				size--
			case "last_bytes_error":
				source = exclusiveSourceError{errors.New("pinned read failed")}
			case "cancelled":
				source = exclusiveObservedReader{Reader: source, observe: cancel}
			}
			if err := PutExclusive(ctx, backend, exclusiveCapturePrefix+"mem", source, size); err == nil {
				t.Fatal("failed source acquired publication success")
			}
			assertNoExclusiveArtifacts(t, backend.root)
		})
	}
}

func TestLocalExclusivePublicationRefusesChangedDirectoryAndSymlinks(t *testing.T) {
	for _, outcome := range []string{"parent_replaced", "root_symlink", "parent_symlink", "shared_write", "destination_symlink"} {
		t.Run(outcome, func(t *testing.T) {
			backend := exclusiveLocalFixture(t)
			parent := filepath.Join(backend.root, "capture")
			if err := os.Mkdir(parent, 0o700); err != nil {
				t.Fatal(err)
			}
			outside := t.TempDir()
			var reader io.Reader = bytes.NewReader([]byte("original"))
			key := "capture/mem"
			switch outcome {
			case "root_symlink":
				link := filepath.Join(outside, "root-link")
				if err := os.Symlink(backend.root, link); err != nil {
					t.Fatal(err)
				}
				backend.root = link
			case "parent_symlink":
				if err := os.Remove(parent); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(outside, parent); err != nil {
					t.Fatal(err)
				}
			case "shared_write":
				if err := os.Chmod(parent, 0o777); err != nil {
					t.Fatal(err)
				}
			case "destination_symlink":
				if err := os.Symlink(filepath.Join(outside, "unowned"), filepath.Join(parent, "mem")); err != nil {
					t.Fatal(err)
				}
			case "parent_replaced":
				didReplace := false
				reader = exclusiveObservedReader{Reader: reader, observe: func() {
					if didReplace {
						return
					}
					didReplace = true
					if err := os.Rename(parent, filepath.Join(backend.root, "retained")); err != nil {
						t.Fatal(err)
					}
					if err := os.Mkdir(parent, 0o700); err != nil {
						t.Fatal(err)
					}
				}}
			}
			if err := PutExclusive(t.Context(), backend, key, reader, 8); err == nil {
				t.Fatal("changed placement or symlink acquired publication authority")
			}
			if outcome == "parent_replaced" || outcome == "shared_write" {
				assertNoExclusiveArtifacts(t, backend.root)
			}
			if outcome != "root_symlink" {
				assertNoExclusiveArtifacts(t, outside)
			}
			if outcome == "destination_symlink" {
				if info, err := os.Lstat(filepath.Join(parent, "mem")); err != nil || info.Mode()&os.ModeSymlink == 0 {
					t.Fatal("exclusive writer removed an unowned destination", err)
				}
			}
		})
	}
}

func TestLocalExclusivePublicationConcurrentCommitCannotReplace(t *testing.T) {
	backend := exclusiveLocalFixture(t)
	key := exclusiveCapturePrefix + "vmstate"
	done := make(chan error, 2)
	for _, body := range []string{"original-a", "original-b"} {
		go func() {
			done <- PutExclusive(t.Context(), backend, key, bytes.NewReader([]byte(body)), int64(len(body)))
		}()
	}
	first, second := <-done, <-done
	if !((first == nil && errors.Is(second, ErrArtifactExists)) || (second == nil && errors.Is(first, ErrArtifactExists))) {
		t.Fatal("concurrent exclusive producers did not preserve exactly one original", first, second)
	}
	body, err := os.ReadFile(filepath.Join(backend.root, key))
	if err != nil || (string(body) != "original-a" && string(body) != "original-b") {
		t.Fatal("exclusive commit was torn or replaced", err)
	}
}

func TestLocalExclusivePublicationProducerDeathLeavesNoScratchName(t *testing.T) {
	const childRootEnv = "GREGALE_EXCLUSIVE_PUBLICATION_CHILD_ROOT"
	if root := os.Getenv(childRootEnv); root != "" {
		backend, err := NewLocalStorageBackend(root)
		if err != nil {
			t.Fatal(err)
		}
		reads := 0
		source := exclusiveObservedReader{Reader: bytes.NewReader(bytes.Repeat([]byte{42}, 512<<10)), observe: func() {
			reads++
			if reads == 2 {
				_ = syscall.Kill(os.Getpid(), syscall.SIGKILL)
			}
		}}
		if err := PutExclusive(t.Context(), backend, exclusiveCapturePrefix+"drive", source, 512<<10); err != nil {
			t.Fatal(err)
		}
		t.Fatal("crash child returned instead of terminating during its anonymous copy")
	}
	backend := exclusiveLocalFixture(t)
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	child := exec.CommandContext(t.Context(), executable, "-test.run=^TestLocalExclusivePublicationProducerDeathLeavesNoScratchName$")
	child.Env = append(os.Environ(), childRootEnv+"="+backend.root)
	err = child.Run()
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ProcessState.Sys().(syscall.WaitStatus).Signal() != syscall.SIGKILL {
		t.Fatal("child did not terminate at its original disk effect", err)
	}
	assertNoExclusiveArtifacts(t, backend.root)
	if err := PutExclusive(t.Context(), backend, exclusiveCapturePrefix+"drive", bytes.NewReader([]byte("new-attempt")), 11); err != nil {
		t.Fatal("anonymous crash left an unowned name behind", err)
	}
}
