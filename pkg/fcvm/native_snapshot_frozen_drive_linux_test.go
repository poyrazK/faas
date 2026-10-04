//go:build linux

// adr: 521 — Linux file IO evidence is separate from native capture acceptance.
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

	"golang.org/x/sys/unix"
)

func TestNativeFrozenDriveLinuxIsAnonymousReadonlyAndIndependent(t *testing.T) {
	input := nativeWritableFileFixture(t, "private.img", "paused-original-drive")
	directory := filepath.Dir(input.Name())
	before, err := os.ReadDir(directory)
	if err != nil {
		t.Fatal(err)
	}
	original, metadata, err := nativeImageFileMetadata(input)
	if err != nil {
		t.Fatal(err)
	}
	output, err := (linuxNativeImageSources{}).FreezeSnapshotDrive(t.Context(), input, directory)
	if !nativeCloneFilesystemSupported(nativeWritableFilesystem(t, directory)) {
		if err == nil || output != nil {
			if output != nil {
				_ = output.Close()
			}
			t.Fatal("unqualified filesystem created a frozen producer")
		}
		t.Log("filesystem profile refused frozen output; no native producer ran")
		return
	}
	if err != nil {
		t.Fatal(err)
	}
	defer output.Close()
	if err := checkNativeFrozenSnapshotDrive(input, output); err != nil {
		t.Fatal(err)
	}
	reopened, err := os.Open(output.Name())
	if err != nil {
		t.Fatalf("frozen name refers to a closed producer FD: %v", err)
	}
	if err := errors.Join(checkNativeFrozenSnapshotDrive(input, reopened), reopened.Close()); err != nil {
		t.Fatalf("synchronous publication reopen differs from frozen output: %v", err)
	}
	pathFD, err := unix.Open(output.Name(), unix.O_PATH|unix.O_CLOEXEC, 0)
	if err != nil {
		t.Fatal(err)
	}
	pathOnly := os.NewFile(uintptr(pathFD), "owned-path-only-output")
	if err := checkNativeFrozenSnapshotDrive(input, pathOnly); err == nil {
		t.Error("path-only descriptor supplied frozen read authority")
	}
	if err := pathOnly.Close(); err != nil {
		t.Fatal(err)
	}
	if body := nativeWritableReadFile(t, output); body != "paused-original-drive" {
		t.Fatalf("frozen bytes=%q", body)
	}
	if _, err := output.WriteAt([]byte("invalid"), 0); !errors.Is(err, unix.EBADF) {
		t.Fatalf("frozen descriptor allowed writing: %v", err)
	}
	identity, actualMetadata, err := nativeImageFileMetadata(input)
	if err != nil || identity != original || actualMetadata != metadata {
		t.Fatalf("frozen producer changed private input metadata: %+v %+v %v", identity, actualMetadata, err)
	}
	// Later resumed writes must not alter the pause-boundary output.
	if _, err := input.WriteAt([]byte("resumed"), 0); err != nil {
		t.Fatal(err)
	}
	if body := nativeWritableReadFile(t, output); body != "paused-original-drive" {
		t.Fatalf("later source write changed frozen bytes: %q", body)
	}
	if err := output.Close(); err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadDir(directory)
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatalf("frozen producer left named outputs: %v %v", after, err)
	}
	t.Logf("anonymous frozen kernel output verified: filesystem=%x readonly=true", nativeWritableFilesystem(t, directory))
}

func TestNativeFrozenDriveLinuxRefusesInvalidInputsBeforeProduction(t *testing.T) {
	input := nativeWritableFileFixture(t, "private.img", "paused-original-drive")
	for _, name := range []string{"nil_input", "relative_directory", "root_directory", "canceled"} {
		t.Run(name, func(t *testing.T) {
			file, directory := input, filepath.Dir(input.Name())
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			switch name {
			case "nil_input":
				file = nil
			case "relative_directory":
				directory = "relative"
			case "root_directory":
				directory = "/"
			case "canceled":
				cancel()
			}
			output, err := (linuxNativeImageSources{}).FreezeSnapshotDrive(ctx, file, directory)
			if err == nil || output != nil {
				if output != nil {
					_ = output.Close()
				}
				t.Fatal("invalid frozen producer acquired a descriptor")
			}
			if name == "canceled" && !errors.Is(err, context.Canceled) {
				t.Fatalf("cancellation was hidden: %v", err)
			}
		})
	}
}

func TestNativeFrozenDriveLinuxDiesWithItsProducer(t *testing.T) {
	const childKey = "GREGALE_TEST_FROZEN_CLONE_INPUT"
	if path := os.Getenv(childKey); path != "" {
		input, err := os.Open(path)
		if err != nil {
			t.Fatal(err)
		}
		output, err := (linuxNativeImageSources{}).FreezeSnapshotDrive(t.Context(), input, filepath.Dir(path))
		if !nativeCloneFilesystemSupported(nativeWritableFilesystem(t, path)) {
			if err == nil || output != nil {
				t.Fatal("unqualified filesystem produced a frozen output")
			}
			os.Exit(0)
		}
		if err != nil {
			t.Fatal(err)
		}
		if err := checkNativeFrozenSnapshotDrive(input, output); err != nil {
			t.Fatal(err)
		}
		// Only this owned child exits; bypass every descriptor defer.
		os.Exit(0)
	}
	input := nativeWritableFileFixture(t, "private.img", "original-frozen-crash-input")
	directory := filepath.Dir(input.Name())
	before, err := os.ReadDir(directory)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestNativeFrozenDriveLinuxDiesWithItsProducer$", "-test.timeout=10s")
	cmd.Env = append(os.Environ(), childKey+"="+input.Name(), "GORACE=atexit_sleep_ms=0")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("owned frozen producer death: %v\n%s", err, out)
	}
	after, err := os.ReadDir(directory)
	if err != nil || !reflect.DeepEqual(before, after) || nativeWritableReadFile(t, input) != "original-frozen-crash-input" {
		t.Fatalf("dead frozen producer left a named output or changed its input: %v %v", after, err)
	}
}
