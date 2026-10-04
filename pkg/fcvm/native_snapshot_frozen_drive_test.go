//go:build linux || darwin

// adr: 567 — portable descriptor fixtures prove ownership, not native capture.
package fcvm

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

type nativeFrozenDriveFixture struct {
	*nativeSnapshotInputFixture
	freeze  func(context.Context, *os.File, string) (*os.File, error)
	output  *os.File
	freezes int
}

func (b *nativeFrozenDriveFixture) FreezeSnapshotDrive(ctx context.Context, input *os.File, directory string) (*os.File, error) {
	b.freezes++
	var err error
	b.output, err = b.freeze(ctx, input, directory)
	return b.output, err
}

// Named fixture creation is deliberate: this models a backend result after
// production. It supplies no evidence for the Linux anonymous birth window.
func nativeFrozenDescriptorFixture(t *testing.T, input *os.File, directory string) *os.File {
	t.Helper()
	file, err := os.CreateTemp(directory, "modeled-frozen-")
	if err != nil {
		t.Fatal(err)
	}
	name := file.Name()
	t.Cleanup(func() { _ = os.Remove(name) })
	_, copyErr := io.Copy(file, input)
	if err := errors.Join(copyErr, file.Chmod(0o400), file.Sync(), file.Close()); err != nil {
		t.Fatal(err)
	}
	output, err := os.Open(name)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = output.Close() })
	if err := os.Remove(name); err != nil {
		t.Fatal(err)
	}
	return output
}

func nativeFrozenDriveOwnerFixture(t *testing.T) (*JailerVMM, *nativeImageSourceJournal, nativeLaunchRecord, *nativeFrozenDriveFixture, string) {
	t.Helper()
	j, owner, input, root, source := nativeSnapshotInputJournalFixture(t)
	b := &nativeFrozenDriveFixture{nativeSnapshotInputFixture: input}
	b.freeze = func(_ context.Context, input *os.File, directory string) (*os.File, error) {
		return nativeFrozenDescriptorFixture(t, input, directory), nil
	}
	r := &nativeProcessRecoveryRuntime{journal: j.owner, imageSources: b, owned: make(map[string]string)}
	if err := r.acquireDaemonOwnership(t.Context()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = r.daemonLock.Close() })
	r.remember(owner)
	v := &JailerVMM{chrootBase: filepath.Dir(filepath.Dir(filepath.Dir(root))), fcName: "firecracker", nativeRecovery: r}
	return v, j, owner, b, source
}

func TestNativeFrozenDriveUsesOriginalInputAndDoesNotEnableCapture(t *testing.T) {
	v, _, owner, b, source := nativeFrozenDriveOwnerFixture(t)
	if err := os.Remove(source); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(source, []byte("replacement-image"), 0o600); err != nil {
		t.Fatal(err)
	}
	err := v.withNativeFrozenSnapshotDrive(t.Context(), owner.Lease, t.TempDir(), func(output *os.File) error {
		body, err := io.ReadAll(output)
		if string(body) != "original-private-drive" {
			t.Errorf("frozen copy followed replacement: %q", body)
		}
		if _, writeErr := output.WriteAt([]byte("changed"), 0); !errors.Is(writeErr, unix.EBADF) {
			t.Errorf("consumer obtained writable output: %v", writeErr)
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, file := range []*os.File{b.file, b.output} {
		if _, err := file.Stat(); !errors.Is(err, os.ErrClosed) {
			t.Errorf("producer descriptor escaped: %v", err)
		}
	}
	if b.freezes != 1 || len(v.materialisedTmp) != 0 || len(v.bindMounts) != 0 {
		t.Fatal("frozen producer borrowed legacy paths or repeated output")
	}
	if err := v.checkEnvironmentQualificationSnapshotSupport(); err == nil {
		t.Fatal("frozen output enabled capture before memory/state and publication adapters")
	}
}

func TestNativeFrozenDriveRefusesUnownedOrUnconfiguredProducer(t *testing.T) {
	for _, name := range []string{"recovered", "closed_daemon", "old_backend", "relative_directory", "root_directory", "nil_consumer", "canceled"} {
		t.Run(name, func(t *testing.T) {
			v, _, owner, b, _ := nativeFrozenDriveOwnerFixture(t)
			directory := t.TempDir()
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			consume := func(*os.File) error { t.Error("unowned frozen output reached consumer"); return nil }
			switch name {
			case "recovered":
				delete(v.nativeRecovery.owned, owner.Lease.Instance)
			case "closed_daemon":
				if err := v.nativeRecovery.daemonLock.Close(); err != nil {
					t.Fatal(err)
				}
			case "old_backend":
				v.nativeRecovery.imageSources = b.nativeSnapshotInputFixture
			case "relative_directory":
				directory = "relative"
			case "root_directory":
				directory = "/"
			case "nil_consumer":
				consume = nil
			case "canceled":
				cancel()
			}
			if err := v.withNativeFrozenSnapshotDrive(ctx, owner.Lease, directory, consume); err == nil || b.freezes != 0 {
				t.Fatalf("unowned producer started: freezes=%d error=%v", b.freezes, err)
			}
		})
	}
}

func TestNativeFrozenDriveClosesOutputOnErrorsAndCancellation(t *testing.T) {
	injected := errors.New("injected frozen output error")
	for _, name := range []string{"consumer_error", "consumer_canceled", "producer_error_with_descriptor", "producer_canceled", "nil_descriptor", "alias", "named", "wrong_size", "writable", "wrong_mode", "inheritable"} {
		t.Run(name, func(t *testing.T) {
			v, _, owner, b, _ := nativeFrozenDriveOwnerFixture(t)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			b.freeze = func(_ context.Context, input *os.File, directory string) (*os.File, error) {
				if name == "nil_descriptor" {
					return nil, nil
				}
				if name == "alias" {
					return input, nil
				}
				output := nativeFrozenDescriptorFixture(t, input, directory)
				if name == "producer_error_with_descriptor" {
					return output, injected
				}
				if name == "producer_canceled" {
					cancel()
				}
				if name == "wrong_mode" {
					if err := output.Chmod(0o600); err != nil {
						t.Fatal(err)
					}
				}
				if name == "inheritable" {
					if _, err := unix.FcntlInt(output.Fd(), unix.F_SETFD, 0); err != nil {
						t.Fatal(err)
					}
				}
				if name == "named" || name == "wrong_size" || name == "writable" {
					if err := output.Close(); err != nil {
						t.Fatal(err)
					}
					path := filepath.Join(directory, "invalid-output")
					if err := os.WriteFile(path, []byte("original-private-drive"), 0o400); err != nil {
						t.Fatal(err)
					}
					var err error
					if name == "writable" || name == "wrong_size" {
						if err := os.Chmod(path, 0o600); err != nil {
							t.Fatal(err)
						}
						output, err = os.OpenFile(path, os.O_RDWR, 0)
						if err == nil && name == "wrong_size" {
							err = output.Truncate(3)
						}
						if err == nil {
							err = output.Chmod(0o400)
						}
					} else {
						output, err = os.Open(path)
					}
					if err != nil {
						t.Fatal(err)
					}
					t.Cleanup(func() { _ = output.Close() })
					if name != "named" {
						if err := os.Remove(path); err != nil {
							t.Fatal(err)
						}
					}
				}
				return output, nil
			}
			consumed := false
			err := v.withNativeFrozenSnapshotDrive(ctx, owner.Lease, t.TempDir(), func(*os.File) error {
				consumed = true
				if name == "consumer_canceled" {
					cancel()
					return nil
				}
				return injected
			})
			wantConsumed := name == "consumer_error" || name == "consumer_canceled"
			if err == nil || consumed != wantConsumed {
				t.Fatalf("invalid producer result: consumed=%t want=%t error=%v", consumed, wantConsumed, err)
			}
			for _, file := range []*os.File{b.file, b.output} {
				if file != nil {
					if _, err := file.Stat(); !errors.Is(err, os.ErrClosed) {
						t.Errorf("failed producer retained descriptor: %v", err)
					}
				}
			}
		})
	}
}

func TestNativeFrozenDriveRetainsOriginalLocksThroughOutputConsumption(t *testing.T) {
	v, j, owner, b, _ := nativeFrozenDriveOwnerFixture(t)
	entered, release := make(chan struct{}), make(chan struct{})
	done := make(chan error, 1)
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	directory := t.TempDir()
	go func() {
		done <- v.withNativeFrozenSnapshotDrive(ctx, owner.Lease, directory, func(*os.File) error {
			close(entered)
			select {
			case <-release:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		})
	}()
	select {
	case <-entered:
	case <-ctx.Done():
		t.Fatal("frozen consumer did not start")
	}
	defer func() {
		close(release)
		if err := <-done; err != nil {
			t.Error(err)
		}
	}()
	blocked, stop := context.WithTimeout(ctx, 30*time.Millisecond)
	defer stop()
	if _, err := j.owner.revoke(blocked, owner.Lease.Instance); !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("physical ownership released before output consumption: %v", err)
	}
	records, err := j.records()
	if err != nil {
		t.Fatal(err)
	}
	blockedSource, stopSource := context.WithTimeout(ctx, 30*time.Millisecond)
	defer stopSource()
	if lock, err := j.lock(blockedSource, records[0].Identity); !errors.Is(err, context.DeadlineExceeded) {
		if lock != nil {
			_ = lock.Close()
		}
		t.Errorf("source ownership released before output consumption: %v", err)
	}
	for _, file := range []*os.File{b.file, b.output} {
		if _, err := file.Stat(); err != nil {
			t.Errorf("producer descriptor closed during consumption: %v", err)
		}
	}
}
