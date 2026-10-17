// adr: 1027 — an in-place major upgrade parks the database, boots it with
// pg_upgrade under a lease any replica resumes, backs it up on the new
// major and records until when it ran the old one; a refusal leaves it on
// its major, a broken upgrade fails it, and restores before the upgrade
// recover on the old major.
package dbsched

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/onebox-faas/faas/pkg/dbguest"
)

func provisionMajor(t *testing.T, s *Scheduler, major int) VM {
	t.Helper()
	vm, err := s.Provision(context.Background(), ProvisionRequest{DatabaseID: uuid.NewString(), Major: major, VcpuCount: 1, MemMiB: 384, VolumeBytes: 1 << 30})
	if err != nil {
		t.Fatal(err)
	}
	return vm
}

func TestUpgradeParkedDatabase(t *testing.T) {
	s, store, vmm := newTestScheduler(t, 4096)
	vmm.archive = true
	ctx := context.Background()
	vm := provisionMajor(t, s, 14)
	before := time.Now()
	got, err := s.Upgrade(ctx, vm.DatabaseID, 17)
	if err != nil {
		t.Fatal(err)
	}
	if got.State != StateParked || got.Major != 17 || got.LastError != "" || len(got.MajorHistory) != 1 || got.MajorHistory[0].Major != 14 ||
		got.MajorHistory[0].Until.Before(before) || got.LastBackupAt.IsZero() || got.LastBackupAt.After(got.MajorHistory[0].Until) {
		t.Fatalf("after the upgrade = %+v", got)
	}
	if vmm.running[vm.DatabaseID] || vmm.majors[vm.DatabaseID] != 17 || vmm.callCount("upgraded") != 1 {
		t.Fatalf("volume on %d, running %v", vmm.majors[vm.DatabaseID], vmm.running[vm.DatabaseID])
	}
	if !slices.Contains(vmm.upgradeFroms, 14) {
		t.Fatalf("no boot upgraded from 14: %v", vmm.upgradeFroms)
	}
	// The next wake boots the new major.
	woke, err := s.Wake(ctx, vm.DatabaseID)
	if err != nil || woke.State != StateRunning || vmm.runMajor[vm.DatabaseID] != 17 {
		t.Fatalf("wake after the upgrade = %+v, %v (booted %d)", woke, err, vmm.runMajor[vm.DatabaseID])
	}
	if loads, _ := store.Loads(ctx); loads["db-a"].ActiveMB != billableMB(384) {
		t.Fatalf("node memory after the upgrade and a wake = %d", loads["db-a"].ActiveMB)
	}
}

func TestUpgradeRunningDatabaseRunsOn(t *testing.T) {
	s, _, vmm := newTestScheduler(t, 4096)
	ctx := context.Background()
	vm := provisionMajor(t, s, 15)
	if _, err := s.Wake(ctx, vm.DatabaseID); err != nil {
		t.Fatal(err)
	}
	stops := vmm.callCount("stop")
	got, err := s.Upgrade(ctx, vm.DatabaseID, 16)
	if err != nil || got.State != StateRunning || got.Major != 16 || !vmm.running[vm.DatabaseID] || vmm.runMajor[vm.DatabaseID] != 16 {
		t.Fatalf("upgrade of a database in use = %+v, %v", got, err)
	}
	// Parked once for the upgrade; the upgraded VM serves on.
	if vmm.callCount("stop") != stops+1 {
		t.Fatalf("stops during the upgrade = %d, want 1", vmm.callCount("stop")-stops)
	}
	// No archive: no backup, none recorded.
	if !got.LastBackupAt.IsZero() {
		t.Fatalf("a backup was recorded without an archive: %v", got.LastBackupAt)
	}
}

func TestUpgradeRefusedStaysOnItsMajor(t *testing.T) {
	s, _, vmm := newTestScheduler(t, 4096)
	ctx := context.Background()
	withMetrics(s)
	vm := provisionMajor(t, s, 14)
	vmm.upgradeCode = dbguest.ErrCodeUpgradeRefused
	got, err := s.Upgrade(ctx, vm.DatabaseID, 17)
	if !errors.Is(err, ErrUpgradeRefused) || got.State != StateParked || got.Major != 14 || got.LastError != dbguest.ErrCodeUpgradeRefused ||
		got.MajorHistory != nil || vmm.running[vm.DatabaseID] {
		t.Fatalf("refused upgrade = %+v, %v", got, err)
	}
	if n := testutil.ToFloat64(s.cfg.Metrics.upgrades.WithLabelValues(upgradeRefused)); n != 1 {
		t.Fatalf("refused upgrades counted %v", n)
	}
	vmm.upgradeCode = ""
	if woke, err := s.Wake(ctx, vm.DatabaseID); err != nil || vmm.runMajor[vm.DatabaseID] != 14 {
		t.Fatalf("wake on the old major = %+v, %v", woke, err)
	}
}

func TestUpgradeFailedNeedsRestore(t *testing.T) {
	s, _, vmm := newTestScheduler(t, 4096)
	ctx := context.Background()
	vm := provisionMajor(t, s, 14)
	vmm.upgradeCode = dbguest.ErrCodeUpgradeFailed
	got, err := s.Upgrade(ctx, vm.DatabaseID, 17)
	if !errors.Is(err, ErrUpgradeFailed) || got.State != StateFailed || got.Major != 14 || got.LastError != dbguest.ErrCodeUpgradeFailed || vmm.running[vm.DatabaseID] {
		t.Fatalf("broken upgrade = %+v, %v", got, err)
	}
	if _, err := s.Wake(ctx, vm.DatabaseID); !errors.Is(err, ErrConflict) {
		t.Fatalf("wake of a failed upgrade = %v, want ErrConflict", err)
	}
}

func TestUpgradeValidation(t *testing.T) {
	s, store, vmm := newTestScheduler(t, 4096)
	ctx := context.Background()
	seedNodes(t, store, Node{ID: "db-b", TargetURL: "unix:///b", AdmissionCeilingMB: 4096, VolumeCapacityBytes: 1, Majors: []int{14, 16}, Lifecycle: NodeActive})
	vm := provisionMajor(t, s, 16)
	if _, err := s.StartUpgrade(ctx, vm.DatabaseID, 16); !errors.Is(err, ErrInvalid) {
		t.Fatalf("upgrade to the same major = %v", err)
	}
	if _, err := s.StartUpgrade(ctx, vm.DatabaseID, 15); !errors.Is(err, ErrInvalid) {
		t.Fatalf("downgrade = %v", err)
	}
	if _, err := s.StartUpgrade(ctx, vm.DatabaseID, 19); !errors.Is(err, ErrInvalid) {
		t.Fatalf("upgrade to an unknown major = %v", err)
	}
	if _, err := s.StartUpgrade(ctx, uuid.NewString(), 17); !errors.Is(err, ErrNotFound) {
		t.Fatalf("upgrade of a missing database = %v", err)
	}
	// db-b offers 14 and 16 only.
	on14 := VM{DatabaseID: uuid.NewString(), NodeID: "db-b", Major: 14, VcpuCount: 1, MemMiB: 384, VolumeBytes: 1}
	if _, err := store.Place(ctx, on14); err != nil {
		t.Fatal(err)
	}
	if err := store.Transition(ctx, on14.DatabaseID, []State{StateProvisioning}, StateParked, TransitionOptions{Bootstrapped: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.StartUpgrade(ctx, on14.DatabaseID, 17); !errors.Is(err, ErrInvalid) {
		t.Fatalf("upgrade to a major the node lacks = %v", err)
	}

	// While it upgrades it cannot wake, move or resize; asking again for
	// the same major returns it, for another is ErrConflict.
	vmm.upgradeHold = make(chan struct{})
	upgrading, err := s.StartUpgrade(ctx, vm.DatabaseID, 17)
	if err != nil || upgrading.State != StateUpgrading || upgrading.UpgradeMajor != 17 {
		t.Fatalf("start = %+v, %v", upgrading, err)
	}
	if again, err := s.StartUpgrade(ctx, vm.DatabaseID, 17); err != nil || again.State != StateUpgrading {
		t.Fatalf("start again = %+v, %v", again, err)
	}
	if _, err := s.StartUpgrade(ctx, vm.DatabaseID, 18); !errors.Is(err, ErrConflict) {
		t.Fatalf("start to another major = %v", err)
	}
	if _, err := s.Wake(ctx, vm.DatabaseID); !errors.Is(err, ErrConflict) {
		t.Fatalf("wake while upgrading = %v", err)
	}
	if _, err := s.Resize(ctx, vm.DatabaseID, Shape{VcpuCount: 2, MemMiB: 512, VolumeBytes: 1 << 30}); !errors.Is(err, ErrConflict) {
		t.Fatalf("resize while upgrading = %v", err)
	}
	if _, err := s.Restart(ctx, vm.DatabaseID); !errors.Is(err, ErrConflict) {
		t.Fatalf("restart while upgrading = %v", err)
	}
	close(vmm.upgradeHold)
	s.upgradeWG.Wait()
	if got, _ := store.Get(ctx, vm.DatabaseID); got.State != StateParked || got.Major != 17 {
		t.Fatalf("after the held upgrade = %+v", got)
	}
}

func TestDeleteDuringUpgrade(t *testing.T) {
	s, store, vmm := newTestScheduler(t, 4096)
	ctx := context.Background()
	vm := provisionMajor(t, s, 14)
	vmm.upgradeHold = make(chan struct{})
	if _, err := s.StartUpgrade(ctx, vm.DatabaseID, 17); err != nil {
		t.Fatal(err)
	}
	if err := s.Delete(ctx, vm.DatabaseID); err != nil {
		t.Fatalf("delete while upgrading = %v", err)
	}
	close(vmm.upgradeHold)
	s.upgradeWG.Wait()
	if _, err := store.Get(ctx, vm.DatabaseID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("the deleted database came back: %v", err)
	}
}

// TestResumeUpgrade: a replica that stopped mid-upgrade leaves its lease
// to lapse; another resumes without stopping the VM, which may be inside
// pg_upgrade.
func TestResumeUpgrade(t *testing.T) {
	s, store, vmm := newTestScheduler(t, 4096)
	ctx := context.Background()
	vm := provisionMajor(t, s, 14)
	// The stopped replica's upgrade: marked, booted, then abandoned.
	upgrading, err := store.BeginUpgrade(ctx, vm.DatabaseID, 17, time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	boot := upgrading
	boot.Major, boot.UpgradeFrom = 17, 14
	vmm.upgradeHold = make(chan struct{})
	node, _ := s.node(ctx, vm.NodeID)
	if err := vmm.Start(ctx, node, boot); err != nil {
		t.Fatal(err)
	}
	time.Sleep(20 * time.Millisecond)
	stops := vmm.callCount("stop")
	if err := s.ResumeUpgrades(ctx); err != nil {
		t.Fatal(err)
	}
	time.Sleep(50 * time.Millisecond)
	if vmm.callCount("stop") != stops || !vmm.running[vm.DatabaseID] {
		t.Fatal("the resume stopped a VM that may be inside pg_upgrade")
	}
	// A second sweep while this process runs it starts nothing more.
	starts := vmm.callCount("start")
	if err := s.ResumeUpgrades(ctx); err != nil {
		t.Fatal(err)
	}
	close(vmm.upgradeHold)
	s.upgradeWG.Wait()
	if vmm.callCount("start") != starts {
		t.Fatalf("a second sweep booted again: %d starts, want %d", vmm.callCount("start"), starts)
	}
	if got, _ := store.Get(ctx, vm.DatabaseID); got.State != StateParked || got.Major != 17 || vmm.callCount("upgraded") != 1 {
		t.Fatalf("after the resumed upgrade = %+v", got)
	}
}

// TestUpgradeRetriesThenGivesUp: an attempt that cannot boot leaves the
// upgrade for a later one; past UpgradeTimeout it is failed.
func TestUpgradeRetriesThenGivesUp(t *testing.T) {
	s, store, vmm := newTestScheduler(t, 4096)
	ctx := context.Background()
	s.cfg.UpgradeLease, s.cfg.UpgradeTimeout = 30*time.Millisecond, time.Hour
	vm := provisionMajor(t, s, 14)
	vmm.startErr = errors.New("jailer exploded")
	if _, err := s.StartUpgrade(ctx, vm.DatabaseID, 17); err != nil {
		t.Fatal(err)
	}
	s.upgradeWG.Wait()
	if got, _ := store.Get(ctx, vm.DatabaseID); got.State != StateUpgrading {
		t.Fatalf("after a failed attempt = %+v; want still upgrading", got)
	}
	// A retry within the timeout boots again.
	vmm.startErr = nil
	vmm.upgradeCode = dbguest.ErrCodeStartFailed
	time.Sleep(40 * time.Millisecond)
	starts := vmm.callCount("start")
	if err := s.ResumeUpgrades(ctx); err != nil {
		t.Fatal(err)
	}
	s.upgradeWG.Wait()
	if vmm.callCount("start") != starts+1 || vmm.running[vm.DatabaseID] {
		t.Fatalf("retry: %d boots, running %v; want one boot, stopped after its guest failed", vmm.callCount("start")-starts, vmm.running[vm.DatabaseID])
	}
	// Past the timeout the next sweep gives up.
	s.now = func() time.Time { return time.Now().Add(2 * time.Hour) }
	time.Sleep(40 * time.Millisecond)
	if err := s.ResumeUpgrades(ctx); err != nil {
		t.Fatal(err)
	}
	s.upgradeWG.Wait()
	if got, _ := store.Get(ctx, vm.DatabaseID); got.State != StateFailed || got.LastError != dbguest.ErrCodeUpgradeFailed || got.Major != 14 {
		t.Fatalf("after the timeout = %+v", got)
	}
}

// TestUpgradeReplicatedDatabase: the standby stays on the old major and so
// must not take over during the upgrade.
func TestUpgradeReplicatedDatabase(t *testing.T) {
	s, store, vmm := newTestScheduler(t, 4096)
	ctx := context.Background()
	seedNodes(t, store, Node{ID: "db-b", TargetURL: "unix:///b", AdmissionCeilingMB: 4096, VolumeCapacityBytes: 100 << 30, Lifecycle: NodeActive})
	vm, err := s.Provision(ctx, ProvisionRequest{DatabaseID: uuid.NewString(), Major: 14, VcpuCount: 1, MemMiB: 384, VolumeBytes: 1 << 30, Replicated: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Wake(ctx, vm.DatabaseID); err != nil {
		t.Fatal(err)
	}
	s.standbyWG.Wait()
	if got, _ := store.Get(ctx, vm.DatabaseID); !got.StandbySynced {
		t.Fatalf("standby never synchronized: %+v", got)
	}
	vmm.upgradeHold = make(chan struct{})
	upgrading, err := s.StartUpgrade(ctx, vm.DatabaseID, 17)
	if err != nil || upgrading.StandbySynced || upgrading.StandbyRunning {
		t.Fatalf("upgrading = %+v, %v; want the standby stopped and not synchronized", upgrading, err)
	}
	if _, err := store.Failover(ctx, vm.DatabaseID); !errors.Is(err, ErrConflict) {
		t.Fatalf("failover during the upgrade = %v, want ErrConflict", err)
	}
	close(vmm.upgradeHold)
	s.upgradeWG.Wait()
	s.standbyWG.Wait()
	if got, _ := store.Get(ctx, vm.DatabaseID); got.State != StateRunning || got.Major != 17 || !got.StandbySynced {
		t.Fatalf("after the upgrade = %+v; want running on 17 with its standby attached again", got)
	}
}

// TestRestoreAcrossAnUpgrade: a restore before the upgrade recovers on the
// old major from the archive as it stands; one after, on the new major,
// flushing the source as usual.
func TestRestoreAcrossAnUpgrade(t *testing.T) {
	s, store, vmm := newTestScheduler(t, 4096)
	vmm.archive = true
	ctx := context.Background()
	source := provisionMajor(t, s, 14)
	// The source's history began well before its upgrade.
	time.Sleep(20 * time.Millisecond)
	beforeUpgrade := time.Now()
	time.Sleep(20 * time.Millisecond)
	upgraded, err := s.Upgrade(ctx, source.DatabaseID, 17)
	if err != nil {
		t.Fatal(err)
	}
	until := upgraded.MajorHistory[0].Until
	restore := func(major int, target time.Time) (VM, error) {
		return s.Restore(ctx, RestoreRequest{ProvisionRequest: ProvisionRequest{DatabaseID: uuid.NewString(), Major: major, VcpuCount: 1, MemMiB: 384, VolumeBytes: 1 << 30},
			Source: source.DatabaseID, Target: target})
	}
	if _, err := restore(17, beforeUpgrade); !errors.Is(err, ErrInvalid) {
		t.Fatalf("restore before the upgrade on the new major = %v, want ErrInvalid", err)
	}
	old, err := restore(14, beforeUpgrade)
	if err != nil {
		t.Fatal(err)
	}
	s.restoreWG.Wait()
	if got, _ := store.Get(ctx, old.DatabaseID); got.State != StateParked || got.Major != 14 {
		t.Fatalf("old-major restore = %+v", got)
	}
	if len(vmm.untilEnd) != 1 || !vmm.untilEnd[0] || !vmm.flushes[source.DatabaseID].IsZero() {
		t.Fatalf("old-major restore: until end %v, flushed %v; want to the end of the old archive, no flush", vmm.untilEnd, vmm.flushes[source.DatabaseID])
	}

	time.Sleep(20 * time.Millisecond)
	after := time.Now()
	if !after.After(until) {
		t.Fatal("clock did not move past the upgrade")
	}
	if _, err := restore(14, after); !errors.Is(err, ErrInvalid) {
		t.Fatalf("restore after the upgrade on the old major = %v, want ErrInvalid", err)
	}
	if _, err := restore(17, after); err != nil {
		t.Fatal(err)
	}
	s.restoreWG.Wait()
	if len(vmm.untilEnd) != 2 || vmm.untilEnd[1] || vmm.flushes[source.DatabaseID].IsZero() {
		t.Fatalf("new-major restore: until end %v, flushed %v", vmm.untilEnd, vmm.flushes[source.DatabaseID])
	}
}

func TestMajorAt(t *testing.T) {
	t0 := time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC)
	vm := VM{Major: 17, MajorHistory: []MajorSpan{{Major: 14, Until: t0}, {Major: 16, Until: t0.Add(time.Hour)}}}
	for at, want := range map[time.Time]int{
		t0.Add(-time.Hour): 14, t0.Add(-time.Nanosecond): 14, t0: 16, t0.Add(59 * time.Minute): 16, t0.Add(time.Hour): 17, t0.Add(48 * time.Hour): 17,
	} {
		if got := vm.MajorAt(at); got != want {
			t.Errorf("major at %v = %d, want %d", at, got, want)
		}
	}
	if (VM{Major: 15}).MajorAt(t0) != 15 {
		t.Fatal("a database never upgraded ran its major throughout")
	}
}
