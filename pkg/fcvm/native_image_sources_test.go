//go:build linux || darwin

// adr: 568 — image access remains owned across daemon death and shared inode aliases.
package fcvm

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// Models kernel identities and metadata; this supplies no native mount evidence.
type nativeImageBackendFixture struct {
	mu               sync.Mutex
	metadata         nativeImageMetadata
	anchors          map[string]bool
	references       map[string]bool
	prepares, grants int
	apply            func(nativeImageSourceRecord) error
}

func newNativeImageBackendFixture() *nativeImageBackendFixture {
	return &nativeImageBackendFixture{metadata: nativeImageMetadata{Mode: 0o600, UID: uint32(os.Geteuid()), GID: uint32(os.Getegid())}, anchors: make(map[string]bool), references: make(map[string]bool)}
}

type nativeImagePreparationFixture struct {
	backend *nativeImageBackendFixture
	link    bool
}

func (*nativeImagePreparationFixture) Identity() nativeLoopIdentity {
	return nativeLoopIdentity{Device: 17, Inode: 42}
}
func (p *nativeImagePreparationFixture) Metadata() (nativeImageMetadata, error) {
	p.backend.mu.Lock()
	defer p.backend.mu.Unlock()
	return p.backend.metadata, nil
}
func (*nativeImagePreparationFixture) Namespace() nativeLoopIdentity {
	return nativeLoopIdentity{Device: 19, Inode: 43}
}
func (p *nativeImagePreparationFixture) PreferLink() bool { return p.link }
func (*nativeImagePreparationFixture) Close() error       { return nil }

func (b *nativeImageBackendFixture) Prepare(_ nativeLaunchRecord, root, _, name string, preferLink bool) (nativeImagePreparation, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.prepares++
	if err := os.MkdirAll(root, 0o700); err != nil {
		return nil, err
	}
	if _, err := os.Lstat(filepath.Join(root, name)); !errors.Is(err, os.ErrNotExist) {
		return nil, errors.New("fixture: target already exists")
	}
	return &nativeImagePreparationFixture{backend: b, link: preferLink}, nil
}

func (p *nativeImagePreparationFixture) CreateAnchor(point string, publish func(nativeLoopIdentity) error) (uint64, error) {
	if err := os.WriteFile(point, nil, 0o600); err != nil {
		return 0, err
	}
	if err := publish(nativeLoopIdentity{Device: 20, Inode: 44}); err != nil {
		return 0, err
	}
	p.backend.mu.Lock()
	p.backend.anchors[point] = true
	p.backend.mu.Unlock()
	return 123, nil
}

func (p *nativeImagePreparationFixture) CreateReference(ref nativeImageReference, publish func(nativeLoopIdentity) error) (nativeImageReference, error) {
	if err := os.WriteFile(filepath.Join(ref.Root, ref.Name), nil, 0o600); err != nil {
		return ref, err
	}
	ref.Target = nativeLoopIdentity{Device: 20, Inode: 45}
	if ref.Link {
		ref.Target = p.Identity()
	} else {
		if err := publish(ref.Target); err != nil {
			return ref, err
		}
		ref.MountID = 124
	}
	p.backend.mu.Lock()
	p.backend.references[ref.ID] = true
	p.backend.mu.Unlock()
	return ref, nil
}

func (b *nativeImageBackendFixture) ApplyMetadata(record nativeImageSourceRecord, point string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if !b.anchors[point] {
		return errors.New("fixture: anchor is absent")
	}
	if b.apply != nil {
		if err := b.apply(record); err != nil {
			return err
		}
	}
	if b.metadata != record.Applied && b.metadata != record.Desired {
		return errors.New("fixture: source metadata changed")
	}
	b.metadata = record.Desired
	b.grants++
	return nil
}

func (b *nativeImageBackendFixture) RetireAnchor(record nativeImageSourceRecord, point string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.anchors[point] && b.metadata != record.Original {
		return errors.New("fixture: original metadata not restored")
	}
	delete(b.anchors, point)
	if err := os.Remove(point); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

func (b *nativeImageBackendFixture) CheckAnchor(record nativeImageSourceRecord, point string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if record.Removed {
		if b.anchors[point] {
			return errors.New("fixture: retired anchor reappeared")
		}
		return nil
	}
	if !b.anchors[point] || b.metadata != record.Applied || record.Applied != record.Desired {
		return errors.New("fixture: anchor lacks metadata proof")
	}
	return nil
}

func (b *nativeImageBackendFixture) RetireReference(_ nativeImageSourceRecord, ref nativeImageReference) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if ref.TargetRemoved && b.references[ref.ID] {
		return errors.New("fixture: retired reference reappeared")
	}
	delete(b.references, ref.ID)
	if err := os.Remove(filepath.Join(ref.Root, ref.Name)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

func (b *nativeImageBackendFixture) CheckReference(_ nativeImageSourceRecord, ref nativeImageReference) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.references[ref.ID] == ref.Removed {
		return errors.New("fixture: reference state differs")
	}
	return nil
}
func (*nativeImageBackendFixture) Inventory(string, []nativeImageSourceRecord) error { return nil }

func nativeImageJournalFixture(t *testing.T) (*nativeImageSourceJournal, nativeLaunchRecord, *nativeImageBackendFixture, string) {
	t.Helper()
	j, lease := preparedNativeJournal(t)
	owner, err := j.read(lease.Instance)
	if err != nil {
		t.Fatal(err)
	}
	b := newNativeImageBackendFixture()
	j.imageSources = b
	root := filepath.Join(t.TempDir(), "firecracker", lease.Instance, "root")
	return &nativeImageSourceJournal{owner: j, backend: b}, owner, b, root
}

func retireNativeImageFixtureOwner(t *testing.T, j *nativeImageSourceJournal, owner nativeLaunchRecord) nativeLaunchRecord {
	t.Helper()
	retired, err := j.owner.revoke(t.Context(), owner.Lease.Instance)
	if err != nil {
		t.Fatal(err)
	}
	if err := j.owner.confirmExit(t.Context(), retired); err != nil {
		t.Fatal(err)
	}
	retired, err = j.owner.read(owner.Lease.Instance)
	if err != nil {
		t.Fatal(err)
	}
	return retired
}

func TestNativeImageSharedAliasesRestoreOnlyAfterLastOwner(t *testing.T) {
	j, first, b, root := nativeImageJournalFixture(t)
	secondLease := leaseForSlot("qualification-image-second", 4)
	secondLease.Plan = first.Lease.Plan
	if err := j.owner.prepare(t.Context(), secondLease); err != nil {
		t.Fatal(err)
	}
	second, err := j.owner.read(secondLease.Instance)
	if err != nil {
		t.Fatal(err)
	}
	secondRoot := filepath.Join(filepath.Dir(filepath.Dir(root)), secondLease.Instance, "root")
	if _, err := j.stage(t.Context(), first, root, "/source.img", "kernel", true, 0o044, true); err != nil {
		t.Fatal(err)
	}
	if _, err := j.stage(t.Context(), second, secondRoot, "/alias.img", "base", true, 0o044, false); err != nil {
		t.Fatal(err)
	}
	records, err := j.records()
	if err != nil || len(records) != 1 || len(records[0].References) != 2 {
		t.Fatalf("aliases split ownership: %+v %v", records, err)
	}
	first = retireNativeImageFixtureOwner(t, j, first)
	if err := j.owner.confirmResourcesRemoved(t.Context(), first); err == nil {
		t.Fatal("unretired reference released its owner")
	}
	if err := j.retireAll(t.Context(), first); err != nil {
		t.Fatal(err)
	}
	if b.metadata.Mode != 0o644 {
		t.Fatal("first owner removed the surviving owner's grant")
	}
	if err := j.owner.confirmResourcesRemoved(t.Context(), first); err != nil {
		t.Fatal(err)
	}
	if _, err := j.owner.replace(t.Context(), first.Lease, first.Generation, true); err != nil {
		t.Fatal(err)
	}
	if _, err := j.owner.records(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := j.require(t.Context(), second, false); err != nil {
		t.Fatal(err)
	}
	second = retireNativeImageFixtureOwner(t, j, second)
	restarted := &nativeImageSourceJournal{owner: j.owner, backend: b}
	if err := restarted.retireAll(t.Context(), second); err != nil {
		t.Fatal(err)
	}
	if b.metadata.Mode != 0o600 || len(b.anchors) != 0 || len(b.references) != 0 {
		t.Fatalf("last retirement lost baseline: %+v", b)
	}
	if err := restarted.require(t.Context(), second, true); err != nil {
		t.Fatal(err)
	}
}

func TestNativeImageInterruptedPublicationRecoversWithoutGrantLoss(t *testing.T) {
	for failAt := 1; failAt <= 6; failAt++ {
		t.Run(string(rune('0'+failAt)), func(t *testing.T) {
			j, owner, b, root := nativeImageJournalFixture(t)
			calls := 0
			j.writeValue = func(path string, record nativeImageSourceRecord) error {
				calls++
				if calls == failAt {
					return errors.New("fixture: fsync failed")
				}
				return writeNativeJournalValue(path, record)
			}
			if _, err := j.stage(t.Context(), owner, root, "/source.img", "base", true, 0o044, false); err == nil {
				t.Fatal("failed ownership write accepted staging")
			}
			if failAt > 1 {
				if _, err := j.owner.beginLaunch(t.Context(), owner.Lease); err == nil {
					t.Fatal("unfinished image reference permitted launch")
				}
			}
			owner = retireNativeImageFixtureOwner(t, j, owner)
			restarted := &nativeImageSourceJournal{owner: j.owner, backend: b}
			if err := restarted.retireAll(t.Context(), owner); err != nil {
				t.Fatal(err)
			}
			if b.metadata.Mode != 0o600 || len(b.anchors) != 0 || len(b.references) != 0 {
				t.Fatalf("failure %d leaked an access grant", failAt)
			}
			if err := j.owner.confirmResourcesRemoved(t.Context(), owner); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestNativeImageAnchorRemovalAcknowledgementIsRetryable(t *testing.T) {
	j, owner, b, root := nativeImageJournalFixture(t)
	if _, err := j.stage(t.Context(), owner, root, "/source.img", "base", true, 0o044, false); err != nil {
		t.Fatal(err)
	}
	owner = retireNativeImageFixtureOwner(t, j, owner)
	j.writeValue = func(path string, record nativeImageSourceRecord) error {
		if record.Removed {
			return errors.New("fixture: final acknowledgement failed")
		}
		return writeNativeJournalValue(path, record)
	}
	if err := j.retireAll(t.Context(), owner); err == nil {
		t.Fatal("failed acknowledgement released ownership")
	}
	if len(b.anchors) != 0 {
		t.Fatal("fixture did not reach anchor removal")
	}
	restarted := &nativeImageSourceJournal{owner: j.owner, backend: b}
	if err := restarted.retireAll(t.Context(), owner); err != nil {
		t.Fatal(err)
	}
	if err := restarted.require(t.Context(), owner, true); err != nil {
		t.Fatal(err)
	}
}

func TestNativeImageExclusiveWritableAndStaleGeneration(t *testing.T) {
	j, owner, b, root := nativeImageJournalFixture(t)
	if _, err := j.stage(t.Context(), owner, root, "/source.img", "layer", false, 0, true); err != nil {
		t.Fatal(err)
	}
	if b.metadata.UID != uint32(owner.Lease.UID) || b.metadata.Mode != 0o600 {
		t.Fatal("writable grant differs from original lease")
	}
	if _, err := j.stage(t.Context(), owner, root, "/alias.img", "other", true, 0o044, true); err == nil {
		t.Fatal("writable inode shared a second alias")
	}
	owner = retireNativeImageFixtureOwner(t, j, owner)
	if err := j.retireAll(t.Context(), owner); err != nil {
		t.Fatal(err)
	}
	if err := j.owner.confirmResourcesRemoved(t.Context(), owner); err != nil {
		t.Fatal(err)
	}
	next, err := j.owner.replace(t.Context(), owner.Lease, owner.Generation, true)
	if err != nil {
		t.Fatal(err)
	}
	prepares := b.prepares
	if _, err := j.stage(t.Context(), owner, root, "/source.img", "layer", false, 0, true); err == nil || b.prepares != prepares {
		t.Fatal("stale owner reached source preparation")
	}
	if _, err := j.stage(t.Context(), next, root, "/source.img", "layer", false, 0, true); err != nil {
		t.Fatal(err)
	}
	records, err := j.records()
	if err != nil || len(records) != 2 || records[0].Epoch == records[1].Epoch {
		t.Fatalf("new use reused original anchor epoch: %+v %v", records, err)
	}
}

func TestNativeImagePublishesPermissionIntentBeforeEffects(t *testing.T) {
	j, owner, b, root := nativeImageJournalFixture(t)
	b.apply = func(record nativeImageSourceRecord) error {
		records, err := j.records()
		if err != nil || len(records) != 1 || !records[0].Ready || records[0].Desired != record.Desired || records[0].Applied != b.metadata || records[0].References[0].Ready {
			return errors.New("permission effect preceded durable ownership")
		}
		return nil
	}
	if _, err := j.stage(t.Context(), owner, root, "/source.img", "base", true, 0o044, true); err != nil {
		t.Fatal(err)
	}
}

func TestNativeImageRecordsRejectMissingAndDuplicateAuthority(t *testing.T) {
	j, owner, _, root := nativeImageJournalFixture(t)
	if _, err := j.stage(t.Context(), owner, root, "/source.img", "base", true, 0o044, true); err != nil {
		t.Fatal(err)
	}
	records, err := j.records()
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(records[0])
	if err != nil {
		t.Fatal(err)
	}
	for _, changed := range []string{
		strings.Replace(string(data), `"removed":false,`, "", 1),
		strings.Replace(string(data), `"removed":false`, `"removed":null`, 1),
		strings.Replace(string(data), `"mode":384`, `"mode":384,"mode":384`, 1),
		strings.Replace(string(data), `"references":[`, `"references":[{"unknown":true},`, 1),
	} {
		var record nativeImageSourceRecord
		if err := json.Unmarshal([]byte(changed), &record); err == nil {
			t.Fatal("damaged image ownership was decoded")
		}
	}
}

func TestNativeImageManagerStagingUsesOriginalOwnerAndRefusesUnjournaledCopies(t *testing.T) {
	_, v, _ := nativeManagerFixture(t)
	v.readyTimeout = 30 * time.Second
	lease := leaseForSlot("qualification-image-manager", 3)
	if err := v.nativeRecovery.journal.prepare(t.Context(), lease); err != nil {
		t.Fatal(err)
	}
	owner, err := v.nativeRecovery.journal.read(lease.Instance)
	if err != nil {
		t.Fatal(err)
	}
	v.nativeRecovery.remember(owner)
	root := v.chrootRoot(lease.Instance)
	if got, err := v.mkChrootForOwner(t.Context(), owner, lease.Instance); err != nil || got != root {
		t.Fatalf("prepared jail creation: %s %v", got, err)
	}
	if _, err := v.stageReadOnlyAs(root, "/source.img", "base", lease.Instance); err != nil {
		t.Fatal(err)
	}
	if len(v.bindMounts[lease.Instance]) != 0 {
		t.Fatal("native staging used in-memory bind authority")
	}
	images := nativeImageSourceJournal{owner: v.nativeRecovery.journal, backend: v.nativeRecovery.imageSources}
	if err := images.require(t.Context(), owner, false); err != nil {
		t.Fatal(err)
	}
	canary := filepath.Join(root, "retained")
	if err := os.WriteFile(canary, []byte("original"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := v.mkChrootForOwner(t.Context(), owner, lease.Instance); err == nil {
		t.Fatal("native retry wiped an existing image root")
	}
	if data, err := os.ReadFile(canary); err != nil || string(data) != "original" {
		t.Fatalf("refused root replacement altered owned files: %s %v", data, err)
	}
	if _, err := v.stageWritableAs(root, "/source.img", "layer", lease.UID, lease.GID, lease.Instance); err == nil {
		t.Fatal("native writable copy used legacy producer")
	}
	if _, _, err := v.freezeSnapshotDrive(root, lease.Instance); err == nil {
		t.Fatal("native snapshot export used legacy producer")
	}
	if _, err := v.stageEphemeralWritableAsForOwner(t.Context(), nativeLaunchRecord{}, root, "/source.img", "scratch", lease.UID, lease.GID, lease.Instance); err == nil {
		t.Fatal("stale prepared owner borrowed current staging authority")
	}
}

func TestNativeImageSourceTransitionBlocksConcurrentRevocation(t *testing.T) {
	j, owner, b, root := nativeImageJournalFixture(t)
	entered, release := make(chan struct{}), make(chan struct{})
	defer func() {
		select {
		case <-release:
		default:
			close(release)
		}
	}()
	b.apply = func(nativeImageSourceRecord) error { close(entered); <-release; return nil }
	done := make(chan error, 1)
	go func() {
		_, err := j.stage(t.Context(), owner, root, "/source.img", "base", true, 0o044, true)
		done <- err
	}()
	select {
	case <-entered:
	case <-time.After(10 * time.Second):
		t.Fatal("source producer did not begin")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 25*time.Millisecond)
	_, err := j.owner.revoke(ctx, owner.Lease.Instance)
	cancel()
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("revocation overlapped image permission producer: %v", err)
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	owner = retireNativeImageFixtureOwner(t, j, owner)
	b.apply = nil
	if err := j.retireAll(t.Context(), owner); err != nil {
		t.Fatal(err)
	}
	prepares := b.prepares
	if _, err := j.stage(t.Context(), owner, root, "/source.img", "base", true, 0o044, true); err == nil || b.prepares != prepares {
		t.Fatal("revoked producer gained another source effect")
	}
}

type pausedNativeImageBackendFixture struct {
	*nativeImageBackendFixture
	prepared, release chan struct{}
}

func (b *pausedNativeImageBackendFixture) Prepare(owner nativeLaunchRecord, root, source, name string, link bool) (nativeImagePreparation, error) {
	p, err := b.nativeImageBackendFixture.Prepare(owner, root, source, name, link)
	close(b.prepared)
	<-b.release
	return p, err
}

func TestNativeImageNewEpochReadsBaselineAfterPreviousEpochRetirement(t *testing.T) {
	j, first, b, firstRoot := nativeImageJournalFixture(t)
	if _, err := j.stage(t.Context(), first, firstRoot, "/source.img", "base", true, 0o044, true); err != nil {
		t.Fatal(err)
	}
	lease := leaseForSlot("qualification-image-baseline", 4)
	lease.Plan = first.Lease.Plan
	if err := j.owner.prepare(t.Context(), lease); err != nil {
		t.Fatal(err)
	}
	second, err := j.owner.read(lease.Instance)
	if err != nil {
		t.Fatal(err)
	}
	paused := &pausedNativeImageBackendFixture{nativeImageBackendFixture: b, prepared: make(chan struct{}), release: make(chan struct{})}
	defer func() {
		select {
		case <-paused.release:
		default:
			close(paused.release)
		}
	}()
	secondJournal := &nativeImageSourceJournal{owner: j.owner, backend: paused}
	secondRoot := filepath.Join(filepath.Dir(filepath.Dir(firstRoot)), lease.Instance, "root")
	done := make(chan error, 1)
	go func() {
		_, err := secondJournal.stage(t.Context(), second, secondRoot, "/alias.img", "base", true, 0o044, false)
		done <- err
	}()
	select {
	case <-paused.prepared:
	case <-time.After(10 * time.Second):
		t.Fatal("second source preparation did not begin")
	}
	first = retireNativeImageFixtureOwner(t, j, first)
	if err := j.retireAll(t.Context(), first); err != nil {
		t.Fatal(err)
	}
	if b.metadata.Mode != 0o600 {
		t.Fatal("first epoch did not restore original permissions")
	}
	close(paused.release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	records, err := j.records()
	if err != nil || len(records) != 2 {
		t.Fatalf("new epoch frames=%+v %v", records, err)
	}
	for _, record := range records {
		if record.Original.Mode != 0o600 {
			t.Fatal("new epoch saved the prior temporary read grant as its baseline")
		}
	}
	second = retireNativeImageFixtureOwner(t, j, second)
	if err := j.retireAll(t.Context(), second); err != nil {
		t.Fatal(err)
	}
	if b.metadata.Mode != 0o600 {
		t.Fatal("new epoch did not restore the original source baseline")
	}
}
