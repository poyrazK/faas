//go:build linux || darwin

// adr: 568 — portable input fixtures do not establish native capture acceptance.
package fcvm

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
)

type nativeSnapshotInputFixture struct {
	*nativeImageBackendFixture
	open  func(nativeImageSourceRecord, nativeImageReference, string) (*os.File, error)
	file  *os.File
	opens int
}

func (b *nativeSnapshotInputFixture) OpenSnapshotInput(record nativeImageSourceRecord, ref nativeImageReference, point string) (*os.File, error) {
	b.opens++
	if err := errors.Join(b.CheckReference(record, ref), b.CheckAnchor(record, point)); err != nil {
		return nil, err
	}
	var err error
	if b.open != nil {
		b.file, err = b.open(record, ref, point)
	} else {
		b.file, err = os.Open(point)
	}
	return b.file, err
}

func nativeSnapshotInputJournalFixture(t *testing.T) (*nativeImageSourceJournal, nativeLaunchRecord, *nativeSnapshotInputFixture, string, string) {
	t.Helper()
	j, owner, images, root := nativeImageJournalFixture(t)
	if _, err := j.stage(t.Context(), owner, root, "/modeled-original.img", layerImageName, false, 0, false); err != nil {
		t.Fatal(err)
	}
	records, err := j.records()
	if err != nil || len(records) != 1 {
		t.Fatalf("staged image records: %+v %v", records, err)
	}
	record := records[0]
	previousPath := j.path(record)
	point := j.anchor(record)
	if err := os.WriteFile(point, []byte("original-private-drive"), 0o600); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(point)
	if err != nil {
		t.Fatal(err)
	}
	identity, err := resourceFileID(info)
	if err != nil {
		t.Fatal(err)
	}
	// The existing fixture models mounts and grants. Bind its modeled source
	// identity to a real file for descriptor/replacement checks, without
	// claiming that a hardlink models native mount acceptance.
	record.Identity = nativeLoopIdentity{Device: identity.Device, Inode: identity.Inode}
	if err := j.write(record); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(previousPath); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(t.TempDir(), "original.img")
	if err := os.Link(point, source); err != nil {
		t.Fatal(err)
	}
	owner.Authorized, owner.PID, owner.StartTime = true, 42, 101
	if err := j.owner.write(owner); err != nil {
		t.Fatal(err)
	}
	b := &nativeSnapshotInputFixture{nativeImageBackendFixture: images}
	j.backend = b
	return j, owner, b, root, source
}

func TestNativeSnapshotInputPinsOriginalInodeAfterPathReplacement(t *testing.T) {
	j, owner, b, root, source := nativeSnapshotInputJournalFixture(t)
	if err := os.Remove(source); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(source, []byte("replacement-must-not-export"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := j.withSnapshotDrive(t.Context(), owner, root, func(file *os.File) error {
		body, err := io.ReadAll(file)
		if string(body) != "original-private-drive" {
			t.Errorf("export read replacement bytes: %q", body)
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if b.opens != 1 {
		t.Fatalf("input opens = %d", b.opens)
	}
	if _, err := b.file.Stat(); !errors.Is(err, os.ErrClosed) {
		t.Fatalf("descriptor escaped ownership boundary: %v", err)
	}
	body, err := os.ReadFile(source)
	if err != nil || string(body) != "replacement-must-not-export" {
		t.Fatalf("input boundary changed replacement: %q %v", body, err)
	}
}

func TestNativeSnapshotInputRequiresOriginalDaemonProducer(t *testing.T) {
	for _, name := range []string{"original", "recovered", "changed_generation", "changed_lease", "no_daemon_lock", "closed_daemon_lock", "legacy"} {
		t.Run(name, func(t *testing.T) {
			j, owner, b, root, _ := nativeSnapshotInputJournalFixture(t)
			r := &nativeProcessRecoveryRuntime{journal: j.owner, imageSources: b, owned: make(map[string]string)}
			if err := r.acquireDaemonOwnership(t.Context()); err != nil {
				t.Fatal(err)
			}
			daemonLock := r.daemonLock
			t.Cleanup(func() {
				if err := daemonLock.Close(); err != nil && !errors.Is(err, os.ErrClosed) {
					t.Error(err)
				}
			})
			r.remember(owner)
			v := &JailerVMM{chrootBase: filepath.Dir(filepath.Dir(filepath.Dir(root))), fcName: "firecracker", nativeRecovery: r}
			lease := owner.Lease
			switch name {
			case "recovered":
				delete(r.owned, lease.Instance)
			case "changed_generation":
				r.owned[lease.Instance] = uuid.NewString()
			case "changed_lease":
				lease.MemoryMaxMiB++
			case "no_daemon_lock":
				r.daemonLock = nil
			case "closed_daemon_lock":
				if err := daemonLock.Close(); err != nil {
					t.Fatal(err)
				}
			case "legacy":
				v.nativeRecovery = nil
			}
			consumed := false
			err := v.withNativeSnapshotDriveInput(t.Context(), lease, func(*os.File) error {
				consumed = true
				return nil
			})
			want := name == "original"
			if consumed != want || (err == nil) != want {
				t.Fatalf("local input authority: consumed=%t error=%v, want allowed=%t", consumed, err, want)
			}
			if err := v.checkEnvironmentQualificationSnapshotSupport(); err == nil {
				t.Fatal("input capability enabled native capture without its output producer")
			}
		})
	}
}

func TestNativeSnapshotInputRejectsChangedPhysicalAuthority(t *testing.T) {
	for _, name := range []string{"generation", "kernel_boot", "pid", "start_time", "lease", "prepared", "revoked", "exited", "removed", "builder", "other_root", "missing_owner", "old_backend", "nil_consumer", "canceled"} {
		t.Run(name, func(t *testing.T) {
			j, owner, b, root, _ := nativeSnapshotInputJournalFixture(t)
			current := owner
			ctx := t.Context()
			consume := func(*os.File) error { t.Error("unowned input reached consumer"); return nil }
			switch name {
			case "generation":
				current.Generation = uuid.NewString()
			case "kernel_boot":
				current.KernelBootID = uuid.NewString()
			case "pid":
				current.PID++
			case "start_time":
				current.StartTime++
			case "lease":
				current.Lease.Networkless = !current.Lease.Networkless
			case "prepared":
				current.Authorized, current.PID, current.StartTime = false, 0, 0
			case "revoked":
				current.Revoked = true
			case "exited":
				current.Revoked, current.ExitConfirmed = true, true
			case "removed":
				current.Revoked, current.ExitConfirmed, current.ResourcesRemoved = true, true, true
			case "builder":
				owner.Lease.IsBuilder = true
			case "other_root":
				root = filepath.Join(t.TempDir(), "foreign", "root")
			case "missing_owner":
				if err := os.Remove(j.owner.path(owner.Lease.Instance)); err != nil {
					t.Fatal(err)
				}
			case "old_backend":
				j.backend = b.nativeImageBackendFixture
			case "nil_consumer":
				consume = nil
			case "canceled":
				canceled, cancel := context.WithCancel(ctx)
				cancel()
				ctx = canceled
			}
			if name != "missing_owner" {
				if err := j.owner.write(current); err != nil {
					t.Fatal(err)
				}
			}
			if err := j.withSnapshotDrive(ctx, owner, root, consume); err == nil {
				t.Fatal("changed authority allowed an input producer")
			}
			if b.opens != 0 {
				t.Fatalf("changed authority opened %d descriptors", b.opens)
			}
		})
	}
}

func TestNativeSnapshotInputRejectsIncompleteOrAmbiguousBinding(t *testing.T) {
	for _, name := range []string{"missing", "unfinished_reference", "unfinished_metadata", "readonly", "hardlink", "removed", "changed_root", "changed_owner", "ambiguous", "missing_anchor", "missing_binding"} {
		t.Run(name, func(t *testing.T) {
			j, owner, b, root, _ := nativeSnapshotInputJournalFixture(t)
			records, err := j.records()
			if err != nil {
				t.Fatal(err)
			}
			record := records[0]
			switch name {
			case "missing":
				record.References[0].Name = "other-drive"
			case "unfinished_reference":
				record.References[0].Ready = false
			case "unfinished_metadata":
				record.Applied = record.Original
			case "readonly":
				record.References[0].ReadOnly, record.References[0].AddPerms = true, 0o044
				record.Desired, err = desiredNativeImageMetadata(record)
				record.Applied = record.Desired
			case "hardlink":
				record.References[0].Link, record.References[0].MountID = true, 0
			case "removed":
				record.References[0].Removed, record.References[0].TargetRemoved = true, true
				record.Desired, record.Applied = record.Original, record.Original
			case "changed_root":
				record.References[0].Root = filepath.Join(t.TempDir(), owner.Lease.Instance, "root")
			case "changed_owner":
				record.References[0].Owner.Generation = uuid.NewString()
			case "ambiguous":
				duplicate := record
				duplicate.Epoch, duplicate.Identity.Inode = uuid.NewString(), record.Identity.Inode+1
				if err := j.write(duplicate); err != nil {
					t.Fatal(err)
				}
			case "missing_anchor":
				delete(b.anchors, j.anchor(record))
			case "missing_binding":
				delete(b.references, record.References[0].ID)
			}
			if err != nil {
				t.Fatal(err)
			}
			if err := j.write(record); err != nil {
				t.Fatal(err)
			}
			if err := j.withSnapshotDrive(t.Context(), owner, root, func(*os.File) error {
				t.Error("incomplete binding reached input consumer")
				return nil
			}); err == nil {
				t.Fatal("incomplete binding opened an export input")
			}
		})
	}
}

func TestNativeSnapshotInputClosesDescriptorsOnEveryConsumerOutcome(t *testing.T) {
	injected := errors.New("injected input failure")
	for _, name := range []string{"consumer_error", "canceled_after_open", "canceled_by_consumer", "wrong_inode", "error_with_descriptor", "nil_descriptor", "closed_descriptor"} {
		t.Run(name, func(t *testing.T) {
			j, owner, b, root, _ := nativeSnapshotInputJournalFixture(t)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			consumed := false
			b.open = func(_ nativeImageSourceRecord, _ nativeImageReference, point string) (*os.File, error) {
				if name == "nil_descriptor" {
					return nil, nil
				}
				if name == "wrong_inode" {
					point = filepath.Join(t.TempDir(), "replacement.img")
					if err := os.WriteFile(point, []byte("wrong"), 0o600); err != nil {
						return nil, err
					}
				}
				file, err := os.Open(point)
				if err != nil {
					return nil, err
				}
				switch name {
				case "canceled_after_open":
					cancel()
				case "error_with_descriptor":
					return file, injected
				case "closed_descriptor":
					return file, file.Close()
				}
				return file, nil
			}
			err := j.withSnapshotDrive(ctx, owner, root, func(*os.File) error {
				consumed = true
				if name == "canceled_by_consumer" {
					cancel()
					return nil
				}
				return injected
			})
			if err == nil {
				t.Fatal("failed input producer returned success")
			}
			wantConsumed := name == "consumer_error" || name == "canceled_by_consumer"
			if consumed != wantConsumed {
				t.Fatalf("consumed = %t, want %t", consumed, wantConsumed)
			}
			if b.file != nil {
				if _, err := b.file.Stat(); !errors.Is(err, os.ErrClosed) {
					t.Fatalf("descriptor remains open: %v", err)
				}
			}
			lockCtx, stop := context.WithTimeout(t.Context(), time.Second)
			defer stop()
			lock, err := j.owner.lock(lockCtx, owner.Lease.Instance)
			if err != nil {
				t.Fatalf("consumer retained physical lock: %v", err)
			}
			if err := lock.Close(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestNativeSnapshotInputRetainsOwnershipUntilDescriptorClose(t *testing.T) {
	j, owner, b, root, _ := nativeSnapshotInputJournalFixture(t)
	entered, release := make(chan struct{}), make(chan struct{})
	done := make(chan error, 1)
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	go func() {
		done <- j.withSnapshotDrive(ctx, owner, root, func(*os.File) error {
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
		t.Fatal("input consumer did not start")
	}
	// Always release and join the owned consumer before reporting a failure.
	defer func() {
		close(release)
		if err := <-done; err != nil {
			t.Error(err)
		}
	}()
	blocked, stop := context.WithTimeout(ctx, 30*time.Millisecond)
	defer stop()
	if _, err := j.owner.revoke(blocked, owner.Lease.Instance); !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("retirement passed active input producer: %v", err)
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
		t.Errorf("source epoch passed active input producer: %v", err)
	}
	if _, err := b.file.Stat(); err != nil {
		t.Errorf("descriptor closed while consumer retained ownership: %v", err)
	}
}
