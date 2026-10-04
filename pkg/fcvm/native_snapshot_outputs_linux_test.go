//go:build linux

// adr: 532 — ordinary Linux output IO supplies no VM capture/mount acceptance.
package fcvm

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/google/uuid"
	"golang.org/x/sys/unix"
)

func TestNativeSnapshotOutputsHaveAnonymousIndependentDiskInodes(t *testing.T) {
	directory := t.TempDir()
	first, err := createNativeSnapshotOutput(t.Context(), directory)
	if !nativeCloneFilesystemSupported(nativeWritableFilesystem(t, directory)) {
		if err == nil || first != nil {
			if first != nil {
				_ = first.Close()
			}
			t.Fatal("unqualified filesystem created a snapshot output")
		}
		t.Log("filesystem profile refused anonymous capture output; no producer ran")
		return
	}
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	second, err := createNativeSnapshotOutput(t.Context(), directory)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	var identities []nativeLoopIdentity
	for _, file := range []*os.File{first, second} {
		var stat unix.Stat_t
		if err := unix.Fstat(int(file.Fd()), &stat); err != nil {
			t.Fatal(err)
		}
		if stat.Nlink != 0 || stat.Size != 0 || stat.Mode&unix.S_IFMT != unix.S_IFREG || stat.Mode&0o777 != 0o600 || stat.Uid != uint32(os.Geteuid()) {
			t.Fatalf("snapshot output lacks anonymous private empty inode: %+v", stat)
		}
		flags, err := unix.FcntlInt(file.Fd(), unix.F_GETFD, 0)
		if err != nil || flags&unix.FD_CLOEXEC == 0 {
			t.Fatalf("output descriptor can survive exec: flags=%d error=%v", flags, err)
		}
		access, err := unix.FcntlInt(file.Fd(), unix.F_GETFL, 0)
		if err != nil || access&unix.O_ACCMODE != unix.O_RDWR {
			t.Fatalf("output lacks writable access: flags=%d error=%v", access, err)
		}
		identities = append(identities, nativeLoopIdentity{Device: uint64(stat.Dev), Inode: stat.Ino})
	}
	if identities[0] == identities[1] || identities[0].Device != identities[1].Device {
		t.Fatalf("capture outputs share an inode or escaped original disk: %+v", identities)
	}
	if _, err := first.WriteAt([]byte("memory"), 0); err != nil {
		t.Fatal(err)
	}
	if _, err := second.WriteAt([]byte("device-state"), 0); err != nil {
		t.Fatal(err)
	}
	if nativeWritableReadFile(t, first) != "memory" || nativeWritableReadFile(t, second) != "device-state" {
		t.Fatal("capture outputs shared writable data")
	}
	if err := errors.Join(first.Close(), second.Close()); err != nil {
		t.Fatal(err)
	}
	if entries, err := os.ReadDir(directory); err != nil || len(entries) != 0 {
		t.Fatalf("anonymous output left named materialisation: %v %v", entries, err)
	}
	t.Logf("anonymous capture inodes verified: filesystem=%x links=0 close_on_exec=true", nativeWritableFilesystem(t, directory))
}

func TestNativeSnapshotOutputPreparationKeepsOnlyOriginalRootMarker(t *testing.T) {
	_, owner, _, _ := nativeImageJournalFixture(t)
	base, directory := t.TempDir(), t.TempDir()
	root := filepath.Join(base, "firecracker", owner.Lease.Instance, "root")
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatal(err)
	}
	name, err := nativeSnapshotOutputName(uuid.NewString(), "mem")
	if err != nil {
		t.Fatal(err)
	}
	prepared, err := (linuxNativeImageSources{base: base}).PrepareSnapshotOutput(t.Context(), owner, root, directory, name)
	if !nativeCloneFilesystemSupported(nativeWritableFilesystem(t, directory)) {
		if err == nil || prepared != nil {
			if prepared != nil {
				_ = prepared.Close()
			}
			t.Fatal("unqualified filesystem created a preparation")
		}
		return
	}
	if err != nil {
		t.Fatal(err)
	}
	p := prepared.(*linuxNativeImagePreparation)
	identity, metadata, err := nativeImageFileMetadata(p.source)
	if err != nil || identity != p.Identity() || p.PreferLink() || metadata.Mode != 0o600 || p.Namespace().Inode == 0 {
		t.Fatalf("prepared output lacks private inode ownership: identity=%+v metadata=%+v error=%v", identity, metadata, err)
	}
	if err := p.Close(); err != nil {
		t.Fatal(err)
	}
	for _, file := range []*os.File{p.source, p.root} {
		if _, err := file.Stat(); !errors.Is(err, os.ErrClosed) {
			t.Fatal("prepared output descriptor escaped closure", err)
		}
	}
	if entries, err := os.ReadDir(root); err != nil || len(entries) != 1 || entries[0].Name() != nativeImageRootMarker {
		t.Fatalf("preparation created an unowned jail file: %v %v", entries, err)
	}
	if err := checkNativeImageRoot(root, owner); err != nil {
		t.Fatal(err)
	}
	if entries, err := os.ReadDir(directory); err != nil || len(entries) != 0 {
		t.Fatalf("preparation created a named disk output: %v %v", entries, err)
	}
}

func TestNativeSnapshotOutputPreparationRefusesUnownedPlacement(t *testing.T) {
	for _, change := range []string{"authorized", "revoked", "exited", "removed", "builder", "bad_name", "foreign_capture", "foreign_root", "root_permissions", "target_exists", "foreign_marker", "root_symlink", "directory_symlink", "relative_directory", "missing_directory", "canceled"} {
		t.Run(change, func(t *testing.T) {
			_, owner, _, _ := nativeImageJournalFixture(t)
			base, directory := t.TempDir(), t.TempDir()
			outputDirectory := directory
			root := filepath.Join(base, "firecracker", owner.Lease.Instance, "root")
			if err := os.MkdirAll(root, 0o700); err != nil {
				t.Fatal(err)
			}
			name, err := nativeSnapshotOutputName(uuid.NewString(), "vmstate")
			if err != nil {
				t.Fatal(err)
			}
			ctx := t.Context()
			switch change {
			case "authorized":
				owner.Authorized, owner.PID, owner.StartTime = true, 42, 101
			case "revoked":
				owner.Revoked = true
			case "exited":
				owner.ExitConfirmed = true
			case "removed":
				owner.ResourcesRemoved = true
			case "builder":
				owner.Lease.IsBuilder = true
			case "bad_name":
				name = "../vmstate"
			case "foreign_capture":
				name = "capture-foreign-vmstate"
			case "foreign_root":
				root = filepath.Join(t.TempDir(), "firecracker", owner.Lease.Instance, "root")
			case "root_permissions":
				if err := os.Chmod(root, 0o777); err != nil {
					t.Fatal(err)
				}
			case "target_exists":
				if err := os.WriteFile(filepath.Join(root, name), []byte("untouched"), 0o600); err != nil {
					t.Fatal(err)
				}
			case "foreign_marker":
				foreign := owner
				foreign.Generation = uuid.NewString()
				if err := createNativeImageRootMarker(root, foreign); err != nil {
					t.Fatal(err)
				}
			case "root_symlink":
				if err := os.Remove(root); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(t.TempDir(), root); err != nil {
					t.Fatal(err)
				}
			case "directory_symlink":
				directory = filepath.Join(t.TempDir(), "disk")
				if err := os.Symlink(outputDirectory, directory); err != nil {
					t.Fatal(err)
				}
			case "relative_directory":
				directory = "outputs"
			case "missing_directory":
				directory = filepath.Join(outputDirectory, "missing")
			case "canceled":
				canceled, cancel := context.WithCancel(ctx)
				cancel()
				ctx = canceled
			}
			before, err := os.ReadDir(outputDirectory)
			if err != nil {
				t.Fatal(err)
			}
			prepared, err := (linuxNativeImageSources{base: base}).PrepareSnapshotOutput(ctx, owner, root, directory, name)
			if err == nil || prepared != nil {
				if prepared != nil {
					_ = prepared.Close()
				}
				t.Fatal("unowned placement created an output preparation")
			}
			after, err := os.ReadDir(outputDirectory)
			if err != nil || !reflect.DeepEqual(before, after) {
				t.Fatal("rejected placement changed disk directory", err)
			}
		})
	}
}

func TestNativeSnapshotOutputDiesWithItsProducer(t *testing.T) {
	const childKey = "GREGALE_TEST_ANONYMOUS_CAPTURE_DIRECTORY"
	if directory := os.Getenv(childKey); directory != "" {
		file, err := createNativeSnapshotOutput(t.Context(), directory)
		if !nativeCloneFilesystemSupported(nativeWritableFilesystem(t, directory)) {
			if err == nil || file != nil {
				t.Fatal("unqualified filesystem created a crash producer")
			}
			os.Exit(0)
		}
		if err != nil {
			t.Fatal(err)
		}
		var stat unix.Stat_t
		if err := unix.Fstat(int(file.Fd()), &stat); err != nil || stat.Nlink != 0 {
			t.Fatalf("capture producer created a named inode: %+v %v", stat, err)
		}
		if _, err := file.WriteString("private capture output"); err != nil {
			t.Fatal(err)
		}
		// Exit only this task-owned child without running producer defers.
		os.Exit(0)
	}
	directory := t.TempDir()
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestNativeSnapshotOutputDiesWithItsProducer$", "-test.timeout=10s")
	cmd.Env = append(os.Environ(), childKey+"="+directory, "GORACE=atexit_sleep_ms=0")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("owned capture producer: %v\n%s", err, out)
	}
	if entries, err := os.ReadDir(directory); err != nil || len(entries) != 0 {
		t.Fatalf("dead capture producer left named files: %v %v", entries, err)
	}
}
