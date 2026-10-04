//go:build linux || darwin

// adr: 532 — portable clone fixtures establish ownership order, not native IO.
package fcvm

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
)

type nativeWritableImageFixture struct {
	*nativeImageBackendFixture
	clones     map[nativeLoopIdentity]*nativeImageBackendFixture
	prepares   int
	closes     int
	prepare    func(context.Context) error
	closeInput func() error
}

type nativeWritablePreparationFixture struct {
	nativeImagePreparation
	identity nativeLoopIdentity
	backend  *nativeWritableImageFixture
}

func (p *nativeWritablePreparationFixture) Identity() nativeLoopIdentity { return p.identity }
func (p *nativeWritablePreparationFixture) Close() error {
	p.backend.closes++
	var err error
	if p.backend.closeInput != nil {
		err = p.backend.closeInput()
	}
	return errors.Join(err, p.nativeImagePreparation.Close())
}

func (b *nativeWritableImageFixture) PrepareWritable(ctx context.Context, owner nativeLaunchRecord, root, source, name string) (nativeImagePreparation, error) {
	b.prepares++
	if b.prepare != nil {
		if err := b.prepare(ctx); err != nil {
			return nil, err
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	image := newNativeImageBackendFixture()
	prepared, err := image.Prepare(owner, root, source, name, false)
	if err != nil {
		return nil, err
	}
	identity := nativeLoopIdentity{Device: 17, Inode: uint64(100 + b.prepares)}
	b.clones[identity] = image
	return &nativeWritablePreparationFixture{nativeImagePreparation: prepared, identity: identity, backend: b}, nil
}

func (b *nativeWritableImageFixture) image(record nativeImageSourceRecord) *nativeImageBackendFixture {
	if clone := b.clones[record.Identity]; clone != nil {
		return clone
	}
	return b.nativeImageBackendFixture
}
func (b *nativeWritableImageFixture) ApplyMetadata(record nativeImageSourceRecord, point string) error {
	return b.image(record).ApplyMetadata(record, point)
}
func (b *nativeWritableImageFixture) CheckAnchor(record nativeImageSourceRecord, point string) error {
	return b.image(record).CheckAnchor(record, point)
}
func (b *nativeWritableImageFixture) RetireAnchor(record nativeImageSourceRecord, point string) error {
	return b.image(record).RetireAnchor(record, point)
}
func (b *nativeWritableImageFixture) CheckReference(record nativeImageSourceRecord, ref nativeImageReference) error {
	return b.image(record).CheckReference(record, ref)
}
func (b *nativeWritableImageFixture) RetireReference(record nativeImageSourceRecord, ref nativeImageReference) error {
	return b.image(record).RetireReference(record, ref)
}

func nativeWritableJournalFixture(t *testing.T) (*nativeImageSourceJournal, nativeLaunchRecord, *nativeWritableImageFixture, string) {
	t.Helper()
	j, owner, images, root := nativeImageJournalFixture(t)
	b := &nativeWritableImageFixture{nativeImageBackendFixture: images, clones: make(map[nativeLoopIdentity]*nativeImageBackendFixture)}
	j.backend, j.owner.imageSources = b, b
	return j, owner, b, root
}

func TestNativeWritableImageCopiesHaveIndependentOwnedEpochs(t *testing.T) {
	j, first, b, root := nativeWritableJournalFixture(t)
	secondLease := leaseForSlot("private-clone-second", 4)
	secondLease.Plan = first.Lease.Plan
	if err := j.owner.prepare(t.Context(), secondLease); err != nil {
		t.Fatal(err)
	}
	second, err := j.owner.read(secondLease.Instance)
	if err != nil {
		t.Fatal(err)
	}
	secondRoot := filepath.Join(filepath.Dir(filepath.Dir(root)), secondLease.Instance, "root")
	for _, item := range []struct {
		owner nativeLaunchRecord
		root  string
	}{{first, root}, {second, secondRoot}} {
		if got, err := j.stageWritable(t.Context(), item.owner, item.root, "/same-immutable.img", layerImageName); err != nil || got != layerImageName {
			t.Fatalf("private staging: name=%q error=%v", got, err)
		}
		if err := j.require(t.Context(), item.owner, false); err != nil {
			t.Fatal(err)
		}
	}
	records, err := j.records()
	if err != nil || len(records) != 2 {
		t.Fatalf("private epochs: %+v %v", records, err)
	}
	if records[0].Identity == records[1].Identity || records[0].Epoch == records[1].Epoch {
		t.Fatal("private copies shared an inode or epoch")
	}
	for _, record := range records {
		if len(record.References) != 1 || record.References[0].ReadOnly || record.References[0].Link || !record.Ready || !record.References[0].Ready {
			t.Fatalf("private copy lacks exclusive writable binding: %+v", record)
		}
	}
	if b.prepares != 2 || b.closes != 2 || b.nativeImageBackendFixture.grants != 0 {
		t.Fatalf("producer close or shared source grant: prepares=%d closes=%d grants=%d", b.prepares, b.closes, b.grants)
	}
	for _, owner := range []nativeLaunchRecord{first, second} {
		retired := retireNativeImageFixtureOwner(t, j, owner)
		if err := j.retireAll(t.Context(), retired); err != nil {
			t.Fatal(err)
		}
		if err := j.require(t.Context(), retired, true); err != nil {
			t.Fatal(err)
		}
	}
}

func TestNativeWritableImageRequiresOriginalPreparedAndDaemonOwner(t *testing.T) {
	for _, name := range []string{"original", "old_backend", "recovered", "no_daemon_lock", "closed_daemon_lock", "generation", "kernel_boot", "lease", "uid", "gid", "instance", "root", "name", "builder", "authorized", "revoked", "canceled"} {
		t.Run(name, func(t *testing.T) {
			j, owner, b, root := nativeWritableJournalFixture(t)
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
			uid, gid, instance, driveName := owner.Lease.UID, owner.Lease.GID, owner.Lease.Instance, layerImageName
			ctx := t.Context()
			switch name {
			case "old_backend":
				r.imageSources = b.nativeImageBackendFixture
			case "recovered":
				delete(r.owned, instance)
			case "no_daemon_lock":
				r.daemonLock = nil
			case "closed_daemon_lock":
				if err := daemonLock.Close(); err != nil {
					t.Fatal(err)
				}
			case "generation":
				owner.Generation = uuid.NewString()
			case "kernel_boot":
				owner.KernelBootID = uuid.NewString()
			case "lease":
				owner.Lease.MemoryMaxMiB++
			case "uid":
				uid++
			case "gid":
				gid++
			case "instance":
				instance = "foreign-clone"
			case "root":
				root = filepath.Join(t.TempDir(), instance, "root")
			case "name":
				driveName = "foreign-drive"
			case "builder":
				owner.Lease.IsBuilder = true
			case "authorized":
				owner.Authorized, owner.PID, owner.StartTime = true, 42, 101
				if err := j.owner.write(owner); err != nil {
					t.Fatal(err)
				}
			case "revoked":
				if _, err := j.owner.revoke(ctx, instance); err != nil {
					t.Fatal(err)
				}
			case "canceled":
				canceled, cancel := context.WithCancel(ctx)
				cancel()
				ctx = canceled
			}
			got, err := v.stageWritableAsForOwner(ctx, owner, root, "/immutable-layer.img", driveName, uid, gid, instance)
			want := name == "original"
			if (err == nil) != want || want && got != layerImageName {
				t.Fatalf("staging: name=%q error=%v, want allowed=%t", got, err, want)
			}
			if !want && b.prepares != 0 {
				t.Fatalf("refused caller started %d clones", b.prepares)
			}
			if len(v.materialisedTmp) != 0 || len(v.bindMounts) != 0 {
				t.Fatal("native clone borrowed legacy in-memory authority")
			}
			if err := v.checkEnvironmentQualificationSnapshotSupport(); err == nil {
				t.Fatal("writable clone enabled capture before its export adapter")
			}
		})
	}
}

func TestNativeWritableImageInterruptedAnchorRefusesRecopy(t *testing.T) {
	j, owner, b, root := nativeWritableJournalFixture(t)
	injected := errors.New("lost anchor acknowledgement")
	writes := 0
	j.writeValue = func(path string, record nativeImageSourceRecord) error {
		writes++
		if writes == 3 {
			return injected
		}
		return writeNativeJournalValue(path, record)
	}
	if _, err := j.stageWritable(t.Context(), owner, root, "/immutable.img", layerImageName); !errors.Is(err, injected) {
		t.Fatalf("lost acknowledgement: %v", err)
	}
	if b.prepares != 1 || b.closes != 1 {
		t.Fatalf("failed producer was not joined: prepares=%d closes=%d", b.prepares, b.closes)
	}
	if _, err := j.stageWritable(t.Context(), owner, root, "/immutable.img", layerImageName); err == nil {
		t.Fatal("uncertain original anchor allowed another clone")
	}
	if b.prepares != 1 {
		t.Fatalf("retry started %d copies", b.prepares)
	}
	j.writeValue = nil
	retired := retireNativeImageFixtureOwner(t, j, owner)
	if err := j.retireAll(t.Context(), retired); err != nil {
		t.Fatal(err)
	}
	if err := j.require(t.Context(), retired, true); err != nil {
		t.Fatal(err)
	}
}

func TestNativeWritableImageClosesProducerBeforeOwnershipUnlock(t *testing.T) {
	j, owner, b, root := nativeWritableJournalFixture(t)
	closing, release := make(chan struct{}), make(chan struct{})
	done := make(chan error, 1)
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	b.closeInput = func() error {
		close(closing)
		select {
		case <-release:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	go func() { _, err := j.stageWritable(ctx, owner, root, "/immutable.img", layerImageName); done <- err }()
	select {
	case <-closing:
	case <-ctx.Done():
		t.Fatal("producer never reached close")
	}
	defer func() {
		close(release)
		if err := <-done; err != nil {
			t.Error(err)
		}
	}()
	records, err := j.records()
	if err != nil || len(records) != 1 {
		t.Fatalf("owned copy: %+v %v", records, err)
	}
	blocked, stop := context.WithTimeout(ctx, 30*time.Millisecond)
	defer stop()
	if lock, err := j.lock(blocked, records[0].Identity); !errors.Is(err, context.DeadlineExceeded) {
		if lock != nil {
			_ = lock.Close()
		}
		t.Errorf("source unlocked before producer closed: %v", err)
	}
	blockedVM, stopVM := context.WithTimeout(ctx, 30*time.Millisecond)
	defer stopVM()
	if _, err := j.owner.revoke(blockedVM, owner.Lease.Instance); !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("VM retired before producer closed: %v", err)
	}
}
