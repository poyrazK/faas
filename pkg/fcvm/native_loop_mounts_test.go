//go:build linux || darwin

// adr: 568 — drive writers remain fenced until mount and loop retirement is confirmed.
// adr: 20261025090000 — staging no longer mounts; recovery still retires inherited mounts.
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

	"github.com/google/uuid"
)

// This is a filesystem model, without loop devices or native acceptance proof.
type nativeLoopBackendFixture struct {
	mu          sync.Mutex
	attachments map[string]bool
	retireErr   error
}

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

// legacyLoopMount leaves the durable frame and mountpoint an older vmmd's
// staging session published for a live loop mount: what an upgraded node
// inherits when that vmmd died mid-session.
func legacyLoopMount(t *testing.T, j *nativeLoopMountJournal, owner nativeLaunchRecord, b *nativeLoopBackendFixture) nativeLoopMountRecord {
	t.Helper()
	record := nativeLoopMountRecord{ID: uuid.NewString(), Owner: owner,
		Device: nativeLoopDevice{Number: 7, Rdev: 99, Source: nativeLoopIdentity{Device: 17, Inode: 42}, Namespace: nativeLoopIdentity{Device: 19, Inode: 43}}}
	for _, path := range []string{filepath.Dir(j.root(owner.Generation)), j.root(owner.Generation), filepath.Dir(j.point(record))} {
		if err := os.MkdirAll(path, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := j.write(owner, record); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(j.point(record), 0o700); err != nil {
		t.Fatal(err)
	}
	identity, err := nativeLoopDirectoryIdentity(j.point(record))
	if err != nil {
		t.Fatal(err)
	}
	record.Directory, record.MountID = identity, 123
	if err := j.write(owner, record); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(j.point(record), "upper", "etc", "faas"), 0o755); err != nil {
		t.Fatal(err)
	}
	b.mu.Lock()
	b.attachments[record.ID] = true
	b.mu.Unlock()
	return record
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

// A drive edit publishes no loop frame: there is nothing to recover after a
// crash, and the owner can be released straight away.
func TestNativeDriveEditLeavesNoLoopFrame(t *testing.T) {
	j, owner, _ := nativeLoopJournalFixture(t)
	if err := j.editSession(t.Context(), owner, func() error {
		records, err := j.records(owner)
		if err != nil || len(records) != 0 {
			t.Fatalf("edit published loop frames: %+v %v", records, err)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	retired := retireLoopFixtureOwner(t, j, owner)
	if err := j.owner.confirmResourcesRemoved(t.Context(), retired); err != nil {
		t.Fatal(err)
	}
}

// An inherited mount fences every other host effect until recovery retires
// it, exactly as when the old staging session itself was interrupted.
func TestNativeLoopMountInheritedFrameBlocksEditsAndReleaseUntilRetired(t *testing.T) {
	j, owner, b := nativeLoopJournalFixture(t)
	legacyLoopMount(t, j, owner, b)
	if err := j.editSession(t.Context(), owner, func() error { t.Fatal("inherited mount admitted a drive edit"); return nil }); err == nil {
		t.Fatal("inherited mount admitted a drive edit")
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
	cause := errors.New("busy mount")
	b.retireErr = cause
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

func TestNativeDriveEditLockPreventsRetirement(t *testing.T) {
	j, owner, _ := nativeLoopJournalFixture(t)
	entered, release, done := make(chan struct{}), make(chan struct{}), make(chan error, 1)
	go func() {
		done <- j.editSession(t.Context(), owner, func() error { close(entered); <-release; return nil })
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
		t.Fatal("stop overtook the live drive edit:", revokeErr)
	}
	current, err := j.owner.read(owner.Lease.Instance)
	if err != nil || current.Revoked {
		t.Fatalf("timed-out retirement mutated owner: %+v %v", current, err)
	}
}

func TestNativeDriveEditRejectsStaleAuthorizedAndMissingNativeAuthority(t *testing.T) {
	for _, kind := range []string{"stale", "authorized", "revoked", "no_backend"} {
		t.Run(kind, func(t *testing.T) {
			j, owner, _ := nativeLoopJournalFixture(t)
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
			if err := j.editSession(t.Context(), owner, func() error { t.Fatal("invalid owner reached the editor"); return nil }); err == nil {
				t.Fatal("invalid staging authority reached host effects")
			}
		})
	}
}

func TestNativeLoopMountCorruptAndUnknownFramesRefuseInventory(t *testing.T) {
	j, owner, b := nativeLoopJournalFixture(t)
	legacyLoopMount(t, j, owner, b)
	retired := retireLoopFixtureOwner(t, j, owner)
	if err := j.retireAll(t.Context(), retired); err != nil {
		t.Fatal(err)
	}
	records, err := j.records(retired)
	if err != nil || len(records) != 1 {
		t.Fatalf("records = %+v %v", records, err)
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

func TestNativeStagingMethodsEditUnderTheOriginalOwner(t *testing.T) {
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
	sessions, _ := fakeDriveImages(t)
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
	if *sessions != 7 {
		t.Fatalf("staging sessions=%d", *sessions)
	}
	if err := v.StageWorkloadManifest(l.Instance, 0, WorkloadSpec{Name: "helper"}); err == nil || *sessions != 7 {
		t.Fatal("native staging wrote a shared read-only image")
	}
	if err := v.exportBuildArtifacts(l.Instance, filepath.Join(t.TempDir(), "exports")); err == nil {
		t.Fatal("native export borrowed unjournaled producers")
	}
	j := &nativeLoopMountJournal{owner: v.nativeRecovery.journal, backend: b}
	retired := retireLoopFixtureOwner(t, j, owner)
	if err := j.owner.confirmResourcesRemoved(t.Context(), retired); err != nil {
		t.Fatal(err)
	}
	if _, err := j.owner.replace(t.Context(), l, owner.Generation, true); err != nil {
		t.Fatal(err)
	}
	if err := v.stagePreBootFilesForOwner(t.Context(), owner, l.Instance, nil, nil, []byte(`{"MODE":"late"}`), "", false); err == nil || *sessions != 7 {
		t.Fatal("old boot staged the replacement drive")
	}
}
