//go:build linux || darwin

// adr: 493 — drive writers remain fenced until mount and loop retirement is confirmed.
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

// This is a filesystem model, without loop devices or native acceptance proof.
type nativeLoopBackendFixture struct {
	mu          sync.Mutex
	attachments map[string]bool
	prepares    int
	configured  int
	mounted     int
	retireErr   error
	configure   func() error
}

type nativeLoopReservationFixture struct {
	backend *nativeLoopBackendFixture
	id      string
}

func (b *nativeLoopBackendFixture) Prepare(_, id string) (nativeLoopReservation, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.prepares++
	return &nativeLoopReservationFixture{backend: b, id: id}, nil
}

func (*nativeLoopReservationFixture) Device() nativeLoopDevice {
	return nativeLoopDevice{Number: 7, Rdev: 99, Source: nativeLoopIdentity{Device: 17, Inode: 42}, Namespace: nativeLoopIdentity{Device: 19, Inode: 43}}
}

func (r *nativeLoopReservationFixture) Configure() error {
	b := r.backend
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.configure != nil {
		if err := b.configure(); err != nil {
			return err
		}
	}
	b.configured++
	b.attachments[r.id] = true
	return nil
}

func (r *nativeLoopReservationFixture) Mount(point string) (uint64, error) {
	b := r.backend
	b.mu.Lock()
	defer b.mu.Unlock()
	b.mounted++
	return 123, os.MkdirAll(filepath.Join(point, "upper", "etc", "faas"), 0o755)
}

func (*nativeLoopReservationFixture) Close() error { return nil }

func (b *nativeLoopBackendFixture) Retire(_ context.Context, record nativeLoopMountRecord, point string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.retireErr != nil {
		return b.retireErr
	}
	delete(b.attachments, record.ID)
	// Model the hidden mount contents disappearing after a real unmount.
	return os.RemoveAll(filepath.Join(point, "upper"))
}

func (b *nativeLoopBackendFixture) Removed(record nativeLoopMountRecord, point string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.attachments[record.ID] {
		return errors.New("fixture: loop token still attached")
	}
	if _, err := os.Lstat(point); !errors.Is(err, os.ErrNotExist) {
		return errors.New("fixture: mountpoint remains")
	}
	return nil
}

func (*nativeLoopBackendFixture) Inventory([]nativeLoopMountRecord) error { return nil }

func nativeLoopJournalFixture(t *testing.T) (*nativeLoopMountJournal, nativeLaunchRecord, *nativeLoopBackendFixture) {
	t.Helper()
	j, lease := preparedNativeJournal(t)
	owner, err := j.read(lease.Instance)
	if err != nil {
		t.Fatal(err)
	}
	b := &nativeLoopBackendFixture{attachments: make(map[string]bool)}
	j.loopMounts = b
	return &nativeLoopMountJournal{owner: j, backend: b}, owner, b
}

// ADR-192 diagnostics retain ADR-493's original-owner staging fence.
func TestNativeLoopMountTimingsPreservePreparationAndRetirementBoundaries(t *testing.T) {
	j, owner, b := nativeLoopJournalFixture(t)
	const delay = 3 * time.Millisecond
	b.configure = func() error { time.Sleep(delay); return nil }
	timings := loopMountTimings{Mount: time.Hour, Unmount: time.Hour}
	var mounted time.Duration
	if err := j.session(t.Context(), owner, "/fixture.img", func(string) error {
		if timings.Mount < delay || timings.Unmount != 0 {
			t.Fatalf("writer lacks completed mount timing: %+v", timings)
		}
		mounted = timings.Mount
		time.Sleep(delay)
		return nil
	}, &timings); err != nil {
		t.Fatal(err)
	}
	if timings.Mount != mounted || timings.Unmount <= 0 {
		t.Fatalf("writer or cleanup altered mount attribution: %+v", timings)
	}
	retireLoopFixtureOwner(t, j, owner)
	timings = loopMountTimings{Mount: time.Hour, Unmount: time.Hour}
	if err := j.session(t.Context(), owner, "/fixture.img", func(string) error { t.Fatal("revoked timing request invoked writer"); return nil }, &timings); err == nil {
		t.Fatal("diagnostic request bypassed original-owner revocation")
	}
	if timings.Unmount != 0 || timings.Mount == time.Hour {
		t.Fatalf("rejected request retained stale command timing: %+v", timings)
	}
}

func TestNativeLoopMountPublishesOwnershipBeforeAttachmentAndWrite(t *testing.T) {
	j, owner, b := nativeLoopJournalFixture(t)
	b.configure = func() error {
		records, err := j.records(owner)
		if err != nil || len(records) != 1 || records[0].Directory.Inode == 0 || records[0].MountID != 0 || records[0].Removed {
			return errors.New("attachment preceded durable planned directory ownership")
		}
		return nil
	}
	if err := j.session(t.Context(), owner, "/fixture.img", func(point string) error {
		records, err := j.records(owner)
		if err != nil || len(records) != 1 || records[0].MountID != 123 || records[0].Removed || j.point(records[0]) != point {
			t.Fatalf("writer preceded durable mount identity: %+v %v", records, err)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if b.configured != 1 || b.mounted != 1 {
		t.Fatalf("configure=%d mount=%d", b.configured, b.mounted)
	}
	if err := j.requireRemoved(owner); err != nil {
		t.Fatal(err)
	}
	retired := retireLoopFixtureOwner(t, j, owner)
	if err := j.owner.confirmResourcesRemoved(t.Context(), retired); err != nil {
		t.Fatal(err)
	}
	if _, err := j.owner.replace(t.Context(), owner.Lease, owner.Generation, true); err != nil {
		t.Fatal(err)
	}
	current, err := j.owner.records(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	all, err := j.allRecords(t.Context(), current)
	if err != nil || len(all) != 1 || !all[0].Removed {
		t.Fatalf("immutable archive lost mount evidence: %+v %v", all, err)
	}
}

func retireLoopFixtureOwner(t *testing.T, j *nativeLoopMountJournal, owner nativeLaunchRecord) nativeLaunchRecord {
	t.Helper()
	owner, err := j.owner.revoke(t.Context(), owner.Lease.Instance)
	if err != nil {
		t.Fatal(err)
	}
	if err := j.owner.confirmExit(t.Context(), owner); err != nil {
		t.Fatal(err)
	}
	owner, err = j.owner.read(owner.Lease.Instance)
	if err != nil {
		t.Fatal(err)
	}
	return owner
}

func TestNativeLoopMountPublicationFailureNeverInvokesWriter(t *testing.T) {
	for _, failAt := range []int{1, 2, 3} {
		t.Run(string(rune('0'+failAt)), func(t *testing.T) {
			j, owner, b := nativeLoopJournalFixture(t)
			writes := 0
			cause := errors.New("mount frame fsync failure")
			j.writeValue = func(path string, record nativeLoopMountRecord) error {
				writes++
				if writes == failAt {
					return cause
				}
				return writeNativeJournalValue(path, record)
			}
			called := false
			if err := j.session(t.Context(), owner, "/fixture.img", func(string) error { called = true; return nil }); !errors.Is(err, cause) || called {
				t.Fatalf("publication failure err=%v wrote=%v", err, called)
			}
			if failAt < 3 && b.configured != 0 {
				t.Fatal("attachment preceded its complete ownership frame")
			}
			if err := j.requireRemoved(owner); err != nil {
				t.Fatal("verified failure cleanup was not retained:", err)
			}
		})
	}
}

func TestNativeLoopMountUncertainCleanupBlocksEffectsAcknowledgementAndFallback(t *testing.T) {
	j, owner, b := nativeLoopJournalFixture(t)
	cause := errors.New("busy mount")
	b.retireErr = cause
	if err := j.session(t.Context(), owner, "/fixture.img", func(string) error { return nil }); !errors.Is(err, cause) {
		t.Fatal(err)
	}
	if err := j.session(t.Context(), owner, "/fixture.img", func(string) error { t.Fatal("uncertain mount admitted another writer"); return nil }); err == nil || b.prepares != 1 {
		t.Fatal("uncertain cleanup admitted another attachment")
	}
	if ticket, err := j.owner.beginLaunch(t.Context(), owner.Lease); err == nil {
		_ = ticket.close()
		t.Fatal("unfinished staging authorized a Firecracker launch")
	}
	helpers := &nativeHostHelperJournal{owner: j.owner, groups: newNativeHelperGroupsFixture()}
	cmd, _ := nativeHelperFixtureCommand(t)
	if _, started, err := helpers.launch(t.Context(), owner, cmd); err == nil || started {
		t.Fatal("unfinished staging authorized another helper")
	}
	retired := retireLoopFixtureOwner(t, j, owner)
	if err := j.owner.confirmResourcesRemoved(t.Context(), retired); err == nil {
		t.Fatal("unfinished staging released VM ownership")
	}
	if _, err := j.owner.replace(t.Context(), owner.Lease, owner.Generation, false); err == nil {
		t.Fatal("restore fallback abandoned the old mount")
	}
	restarted := &nativeLoopMountJournal{owner: j.owner, backend: b}
	if err := restarted.retireAll(t.Context(), retired); !errors.Is(err, cause) {
		t.Fatal("restart forgot uncertain mount:", err)
	}
	b.retireErr = nil
	if err := restarted.retireAll(t.Context(), retired); err != nil {
		t.Fatal(err)
	}
	if err := restarted.requireRemoved(retired); err != nil {
		t.Fatal(err)
	}
	if err := j.owner.confirmResourcesRemoved(t.Context(), retired); err != nil {
		t.Fatal(err)
	}
}

func TestNativeLoopMountWriterLockPreventsRetirement(t *testing.T) {
	j, owner, _ := nativeLoopJournalFixture(t)
	entered, release, done := make(chan struct{}), make(chan struct{}), make(chan error, 1)
	go func() {
		done <- j.session(t.Context(), owner, "/fixture.img", func(string) error { close(entered); <-release; return nil })
	}()
	<-entered
	ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
	defer cancel()
	_, revokeErr := j.owner.revoke(ctx, owner.Lease.Instance)
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if !errors.Is(revokeErr, context.DeadlineExceeded) {
		t.Fatal("stop overtook the live Go writer:", revokeErr)
	}
	current, err := j.owner.read(owner.Lease.Instance)
	if err != nil || current.Revoked {
		t.Fatalf("timed-out retirement mutated owner: %+v %v", current, err)
	}
}

func TestNativeLoopMountRejectsStaleAuthorizedAndMissingNativeAuthority(t *testing.T) {
	for _, kind := range []string{"stale", "authorized", "revoked", "no_backend"} {
		t.Run(kind, func(t *testing.T) {
			j, owner, b := nativeLoopJournalFixture(t)
			switch kind {
			case "stale":
				retired := retireLoopFixtureOwner(t, j, owner)
				if err := j.owner.confirmResourcesRemoved(t.Context(), retired); err != nil {
					t.Fatal(err)
				}
				if _, err := j.owner.replace(t.Context(), owner.Lease, owner.Generation, true); err != nil {
					t.Fatal(err)
				}
			case "authorized":
				current := owner
				current.Authorized, current.PID, current.StartTime = true, 123, 456
				if err := j.owner.write(current); err != nil {
					t.Fatal(err)
				}
			case "revoked":
				retireLoopFixtureOwner(t, j, owner)
			case "no_backend":
				j.backend = nil
			}
			if err := j.session(t.Context(), owner, "/fixture.img", func(string) error { t.Fatal("invalid owner reached writer"); return nil }); err == nil || b.prepares != 0 {
				t.Fatal("invalid staging authority reached host effects")
			}
		})
	}
}

func TestNativeLoopMountCorruptAndUnknownFramesRefuseInventory(t *testing.T) {
	j, owner, _ := nativeLoopJournalFixture(t)
	if err := j.session(t.Context(), owner, "/fixture.img", func(string) error { return nil }); err != nil {
		t.Fatal(err)
	}
	records, err := j.records(owner)
	if err != nil {
		t.Fatal(err)
	}
	good, err := json.Marshal(records[0])
	if err != nil {
		t.Fatal(err)
	}
	for _, malformed := range []string{
		strings.Replace(string(good), `"removed":true`, `"removed":null`, 1),
		strings.Replace(string(good), `"mount_id":123,`, "", 1),
		strings.Replace(string(good), `"removed":true`, `"removed":true,"removed":true`, 1),
		strings.Replace(string(good), `"source":{"device":17,"inode":42}`, `"source":{"device":17}`, 1),
		strings.TrimSuffix(string(good), "}") + `,"extra":false}`,
	} {
		var record nativeLoopMountRecord
		if err := json.Unmarshal([]byte(malformed), &record); err == nil {
			t.Fatal("incomplete or ambiguous ownership decoded")
		}
	}
	unknown := filepath.Join(j.root(owner.Generation), "points", "unknown")
	if err := os.Mkdir(unknown, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := j.owner.records(t.Context()); err == nil {
		t.Fatal("unowned mountpoint disappeared from startup inventory")
	}
}

func TestNativeStagingMethodsUseNativeBackendAndRetainOriginalOwner(t *testing.T) {
	_, v, _ := nativeManagerFixture(t)
	// This test exercises fsync ordering under -race, rather than the
	// manager fixture's deliberately short readiness deadline.
	v.readyTimeout = 30 * time.Second
	l := leaseForSlot("native-staging", 0)
	if err := v.prepareNativeLease(t.Context(), l); err != nil {
		t.Fatal(err)
	}
	owner, err := v.nativeDriveStagingOwner(t.Context(), l.Instance)
	if err != nil {
		t.Fatal(err)
	}
	root := v.chrootRoot(l.Instance)
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, layerImageName), []byte("modeled image"), 0o600); err != nil {
		t.Fatal(err)
	}
	b := &nativeLoopBackendFixture{attachments: make(map[string]bool)}
	v.nativeRecovery.loopMounts = b
	original := loopMountSession
	loopMountSession = func(string, string, func(string) error, ...*loopMountTimings) error {
		t.Fatal("native staging borrowed legacy mount runner")
		return nil
	}
	t.Cleanup(func() { loopMountSession = original })
	for _, stage := range []func() error{
		func() error { return v.StageSecretsEnv(l.Instance, []byte(`{"TOKEN":"test"}`)) },
		func() error { return v.StageAPIEnv(l.Instance, []byte(`{"MODE":"test"}`)) },
		func() error { return v.StageWorkloadEnv(l.Instance, "helper", []byte(`{"MODE":"test"}`)) },
		func() error { return v.StageWorkloadManifest(l.Instance, -1, WorkloadSpec{Name: "main"}) },
		func() error { return v.StageWorkloadRoster(l.Instance, WorkloadSpec{Name: "main"}, nil) },
		func() error { return v.stagePreBootFiles(l.Instance, nil, nil, []byte(`{"MODE":"test"}`), "", false) },
		func() error { return v.stageJobManifest(l.Instance, JobManifest{}) },
	} {
		if err := stage(); err != nil {
			t.Fatal(err)
		}
	}
	if b.mounted != 7 {
		t.Fatalf("staging sessions=%d", b.mounted)
	}
	if err := v.StageWorkloadManifest(l.Instance, 0, WorkloadSpec{Name: "helper"}); err == nil || b.mounted != 7 {
		t.Fatal("native staging wrote a shared read-only image")
	}
	if err := v.exportBuildArtifacts(l.Instance, filepath.Join(t.TempDir(), "exports")); err == nil {
		t.Fatal("native export borrowed unjournaled mount/copy producers")
	}
	j := &nativeLoopMountJournal{owner: v.nativeRecovery.journal, backend: b}
	retired := retireLoopFixtureOwner(t, j, owner)
	if err := j.owner.confirmResourcesRemoved(t.Context(), retired); err != nil {
		t.Fatal(err)
	}
	if _, err := j.owner.replace(t.Context(), l, owner.Generation, true); err != nil {
		t.Fatal(err)
	}
	if err := v.stagePreBootFilesForOwner(t.Context(), owner, l.Instance, nil, nil, []byte(`{"MODE":"late"}`), "", false); err == nil || b.mounted != 7 {
		t.Fatal("old boot staged the replacement drive")
	}
}
