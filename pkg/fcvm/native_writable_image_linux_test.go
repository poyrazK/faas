//go:build linux

// adr: 532 — ordinary Linux file IO tests do not supply KVM/mount acceptance.
package fcvm

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func nativeWritableFileFixture(t *testing.T, name, body string) *os.File {
	t.Helper()
	file, err := os.OpenFile(filepath.Join(t.TempDir(), name), os.O_CREATE|os.O_EXCL|os.O_RDWR|unix.O_CLOEXEC, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = file.Close() })
	if _, err := file.WriteString(body); err != nil {
		t.Fatal(err)
	}
	if err := file.Sync(); err != nil {
		t.Fatal(err)
	}
	return file
}

func nativeWritableReadFile(t *testing.T, file *os.File) string {
	t.Helper()
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(file)
	if err != nil {
		t.Fatal(err)
	}
	return string(body)
}

func nativeWritableFilesystem(t *testing.T, path string) int64 {
	t.Helper()
	var filesystem unix.Statfs_t
	if err := unix.Statfs(path, &filesystem); err != nil {
		t.Fatal(err)
	}
	return filesystem.Type
}

func TestNativeWritableCloneFilesystemProfile(t *testing.T) {
	for _, kind := range []int64{unix.EXT4_SUPER_MAGIC, unix.XFS_SUPER_MAGIC, unix.BTRFS_SUPER_MAGIC} {
		if !nativeCloneFilesystemSupported(kind) {
			t.Fatalf("supported disk filesystem refused: %x", kind)
		}
	}
	for _, kind := range []int64{0, unix.TMPFS_MAGIC, unix.RAMFS_MAGIC, unix.OVERLAYFS_SUPER_MAGIC, unix.NFS_SUPER_MAGIC} {
		if nativeCloneFilesystemSupported(kind) {
			t.Fatalf("unqualified filesystem allowed an anonymous producer: %x", kind)
		}
	}
}

func TestNativeWritableCloneIsAnonymousPrivateAndCloseOnExec(t *testing.T) {
	source := nativeWritableFileFixture(t, "source.img", "immutable-app-drive")
	before, err := os.ReadDir(filepath.Dir(source.Name()))
	if err != nil {
		t.Fatal(err)
	}
	clone, err := cloneNativeImage(t.Context(), source, filepath.Dir(source.Name()))
	if !nativeCloneFilesystemSupported(nativeWritableFilesystem(t, source.Name())) {
		if err == nil || clone != nil {
			if clone != nil {
				_ = clone.Close()
			}
			t.Fatal("unsupported filesystem created a producer")
		}
		t.Log("filesystem profile refused anonymous cloning; no native producer ran")
		return
	}
	if err != nil {
		t.Fatal(err)
	}
	defer clone.Close()
	var original, private unix.Stat_t
	if err := errors.Join(unix.Fstat(int(source.Fd()), &original), unix.Fstat(int(clone.Fd()), &private)); err != nil {
		t.Fatal(err)
	}
	if private.Nlink != 0 || private.Dev != original.Dev || private.Ino == original.Ino || private.Mode&0o777 != 0o600 {
		t.Fatalf("anonymous copy identity: original=%+v clone=%+v", original, private)
	}
	flags, err := unix.FcntlInt(clone.Fd(), unix.F_GETFD, 0)
	if err != nil || flags&unix.FD_CLOEXEC == 0 {
		t.Fatalf("private descriptor can survive exec: flags=%d error=%v", flags, err)
	}
	if body := nativeWritableReadFile(t, clone); body != "immutable-app-drive" {
		t.Fatalf("cloned bytes = %q", body)
	}
	if _, err := clone.WriteAt([]byte("changed"), 0); err != nil {
		t.Fatal(err)
	}
	if body := nativeWritableReadFile(t, source); body != "immutable-app-drive" {
		t.Fatalf("private write changed shared source: %q", body)
	}
	if err := clone.Close(); err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadDir(filepath.Dir(source.Name()))
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatalf("clone left a named output: before=%v after=%v error=%v", before, after, err)
	}
	t.Logf("anonymous kernel clone verified: filesystem=%x links=%d close_on_exec=true", nativeWritableFilesystem(t, source.Name()), private.Nlink)
}

func TestNativeWritablePreparationLeavesSharedInputUnchanged(t *testing.T) {
	j, owner, _, _ := nativeImageJournalFixture(t)
	base := t.TempDir()
	root := filepath.Join(base, "firecracker", owner.Lease.Instance, "root")
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatal(err)
	}
	source := nativeWritableFileFixture(t, "source.img", "immutable-private-layer")
	identity, metadata, err := nativeImageFileMetadata(source)
	if err != nil {
		t.Fatal(err)
	}
	prepared, err := (linuxNativeImageSources{base: base}).PrepareWritable(t.Context(), owner, root, source.Name(), layerImageName)
	if !nativeCloneFilesystemSupported(nativeWritableFilesystem(t, source.Name())) {
		if err == nil || prepared != nil {
			if prepared != nil {
				_ = prepared.Close()
			}
			t.Fatal("unsupported filesystem created a preparation")
		}
		return
	}
	if err != nil {
		t.Fatal(err)
	}
	p := prepared.(*linuxNativeImagePreparation)
	if p.Identity() == identity || p.PreferLink() {
		t.Fatal("writable preparation borrowed shared input or hardlink")
	}
	if body := nativeWritableReadFile(t, p.source); body != "immutable-private-layer" {
		t.Fatalf("prepared bytes=%q", body)
	}
	if err := p.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := p.source.Stat(); !errors.Is(err, os.ErrClosed) {
		t.Fatalf("private descriptor escaped: %v", err)
	}
	if _, err := p.root.Stat(); !errors.Is(err, os.ErrClosed) {
		t.Fatalf("root descriptor escaped: %v", err)
	}
	actual, actualMetadata, err := nativeImageFileMetadata(source)
	if err != nil || actual != identity || actualMetadata != metadata || nativeWritableReadFile(t, source) != "immutable-private-layer" {
		t.Fatalf("shared input changed: identity=%+v metadata=%+v error=%v", actual, actualMetadata, err)
	}
	entries, err := os.ReadDir(root)
	if err != nil || len(entries) != 1 || entries[0].Name() != nativeImageRootMarker {
		t.Fatalf("preparation left an unowned file: %v %v", entries, err)
	}
	if err := checkNativeImageRoot(root, owner); err != nil {
		t.Fatal(err)
	}
	if err := j.requireUnusedTarget(owner, root, layerImageName); err != nil {
		t.Fatal(err)
	}
}

func TestNativeWritableCloneDiesWithItsProducer(t *testing.T) {
	const childKey = "GREGALE_TEST_ANONYMOUS_CLONE_SOURCE"
	if path := os.Getenv(childKey); path != "" {
		source, err := os.OpenFile(path, os.O_RDONLY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
		if err != nil {
			t.Fatal(err)
		}
		clone, err := cloneNativeImage(t.Context(), source, filepath.Dir(path))
		if !nativeCloneFilesystemSupported(nativeWritableFilesystem(t, path)) {
			if err == nil || clone != nil {
				t.Fatal("unqualified filesystem produced a clone")
			}
			os.Exit(0)
		}
		if err != nil {
			t.Fatal(err)
		}
		var stat unix.Stat_t
		if err := unix.Fstat(int(clone.Fd()), &stat); err != nil || stat.Nlink != 0 {
			t.Fatalf("crash producer output was named: %+v %v", stat, err)
		}
		// Deliberately bypass file defers: only this owned child process exits.
		os.Exit(0)
	}
	source := nativeWritableFileFixture(t, "source.img", "immutable-crash-input")
	before, err := os.ReadDir(filepath.Dir(source.Name()))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestNativeWritableCloneDiesWithItsProducer$", "-test.timeout=10s")
	cmd.Env = append(os.Environ(), childKey+"="+source.Name(), "GORACE=atexit_sleep_ms=0")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("owned anonymous producer: %v\n%s", err, out)
	}
	after, err := os.ReadDir(filepath.Dir(source.Name()))
	if err != nil || !reflect.DeepEqual(before, after) || nativeWritableReadFile(t, source) != "immutable-crash-input" {
		t.Fatalf("dead producer left named output or changed source: %v %v", after, err)
	}
}

func TestNativeWritableCloneFallbackAndFatalErrors(t *testing.T) {
	for _, cause := range []error{unix.EOPNOTSUPP, unix.ENOTTY, unix.EINVAL, unix.EXDEV, unix.ENOSYS, unix.ENOSPC, unix.EIO} {
		t.Run(cause.Error(), func(t *testing.T) {
			source := nativeWritableFileFixture(t, "source.img", "original-full-image")
			clone := nativeWritableFileFixture(t, "clone.img", "stale-output-must-be-truncated")
			if _, err := source.Seek(5, io.SeekStart); err != nil {
				t.Fatal(err)
			}
			err := populateNativeImageClone(t.Context(), source, clone, int64(len("original-full-image")), func(destination, original int) error {
				if destination != int(clone.Fd()) || original != int(source.Fd()) {
					t.Error("reflink descriptor direction changed")
				}
				return cause
			})
			fatal := errors.Is(cause, unix.ENOSPC) || errors.Is(cause, unix.EIO)
			if fatal {
				if !errors.Is(err, cause) || nativeWritableReadFile(t, clone) != "stale-output-must-be-truncated" {
					t.Fatalf("fatal error was masked by copying: %v", err)
				}
			} else if err != nil || nativeWritableReadFile(t, clone) != "original-full-image" {
				t.Fatalf("bounded copy fallback: %v", err)
			}
			if nativeWritableReadFile(t, source) != "original-full-image" {
				t.Fatal("clone producer changed its original input")
			}
		})
	}
}

func TestNativeWritableCloneRefusesCancellationAndChangedSizes(t *testing.T) {
	for _, name := range []string{"canceled_before", "canceled_reflink", "source_grew", "source_shrank", "clone_incomplete"} {
		t.Run(name, func(t *testing.T) {
			source := nativeWritableFileFixture(t, "source.img", "original")
			clone := nativeWritableFileFixture(t, "clone.img", "untouched")
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			if name == "canceled_before" {
				cancel()
			}
			err := populateNativeImageClone(ctx, source, clone, 8, func(int, int) error {
				switch name {
				case "canceled_before":
					t.Error("canceled producer reached reflink")
				case "canceled_reflink":
					cancel()
				case "source_grew":
					_, _ = source.WriteAt([]byte("grew"), 8)
				case "source_shrank":
					_ = source.Truncate(7)
				case "clone_incomplete":
					if err := clone.Truncate(7); err != nil {
						return err
					}
					return nil
				}
				return unix.EOPNOTSUPP
			})
			if err == nil {
				t.Fatal("cancelled or changed-size producer was accepted")
			}
			if strings.HasPrefix(name, "canceled") && (!errors.Is(err, context.Canceled) || nativeWritableReadFile(t, clone) != "untouched") {
				t.Fatalf("canceled producer copied bytes: %v", err)
			}
		})
	}
}

type nativeImageCancelReader struct{ cancel context.CancelFunc }

func (r nativeImageCancelReader) Read(body []byte) (int, error) {
	n := copy(body, "cancelled-read")
	r.cancel()
	return n, io.EOF
}

func TestNativeWritableCopyReaderSuppressesCanceledBytes(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	n, err := (nativeImageCopyReader{ctx: ctx, source: nativeImageCancelReader{cancel: cancel}}).Read(make([]byte, 64))
	if n != 0 || !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled input exposed %d bytes: %v", n, err)
	}
}
