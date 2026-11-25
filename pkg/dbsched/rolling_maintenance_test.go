package dbsched

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/dbguest"
	"github.com/onebox-faas/faas/pkg/state"
)

type rollingMaintenanceVMM struct {
	*fakeVMM
	losePromotionReply atomic.Bool
	splitImages        atomic.Bool
}

func (f *rollingMaintenanceVMM) Control(ctx context.Context, node Node, id string, req dbguest.Request) (dbguest.Response, error) {
	resp, err := f.fakeVMM.Control(ctx, node, id, req)
	if req.Op == dbguest.OpStatus && err == nil {
		resp.Major = 17
		resp.DesiredImage = "current.ext4:1"
		resp.RunningImage = resp.DesiredImage
		if resp.Stale {
			resp.RunningImage = "old.ext4:1"
		}
		if node.ID == "db-b" && f.splitImages.Load() {
			resp.DesiredImage, resp.RunningImage, resp.Stale = "old.ext4:1", "old.ext4:1", false
		}
	}
	if req.Op == dbguest.OpPromote && resp.State == dbguest.StateReady && f.losePromotionReply.Swap(false) {
		return dbguest.Response{}, context.DeadlineExceeded
	}
	return resp, err
}

func rollingMaintenanceFixture(t *testing.T) (*PgStore, *Scheduler, *rollingMaintenanceVMM, VM) {
	t.Helper()
	ctx := t.Context()
	store := storeFactories(t)["pg"](t).(*PgStore)
	seedNodes(t, store,
		Node{ID: "db-a", TargetURL: "unix:///a", AdmissionCeilingMB: 4096, VolumeCapacityBytes: 30 << 30, Lifecycle: NodeActive},
		Node{ID: "db-b", TargetURL: "unix:///b", AdmissionCeilingMB: 4096, VolumeCapacityBytes: 20 << 30, Lifecycle: NodeActive})
	vmm := &rollingMaintenanceVMM{fakeVMM: newFakeVMM()}
	s := New(store, vmm, Config{StandbySyncTimeout: time.Second}, nil)
	t.Cleanup(s.standbyWG.Wait)
	vm, err := s.Provision(ctx, ProvisionRequest{DatabaseID: uuid.NewString(), Major: 17, VcpuCount: 1, MemMiB: 384,
		VolumeBytes: 1 << 30, AlwaysOn: true, Replicated: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Wake(ctx, vm.DatabaseID); err != nil {
		t.Fatal(err)
	}
	s.standbyWG.Wait()
	vm, err = store.Get(ctx, vm.DatabaseID)
	if err != nil || !vm.StandbySynced || vm.NodeID != "db-a" || vm.StandbyNode != "db-b" {
		t.Fatalf("fixture: %+v, %v", vm, err)
	}
	registerRollingFixtureDatabase(t, store, vm)
	vmm.mu.Lock()
	vmm.calls, vmm.onNode = nil, nil
	vmm.mu.Unlock()
	return store, s, vmm, vm
}

func registerRollingFixtureDatabase(t *testing.T, store *PgStore, vm VM) {
	t.Helper()
	ctx := t.Context()
	account, err := state.NewPgStore(store.pool).CreateAccount(ctx, uuid.NewString()+"@rolling.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.pool.Exec(ctx, `
		INSERT INTO managed_postgres_databases
			(id, account_id, name, region, postgres_major, service_class, availability, scale_to_zero, backend_id,
			 backend_fingerprint, provider_resource_id, state, observed_generation)
		VALUES ($1, $2, 'rolling-db', 'eu', 17, 'production', 'single_zone', false, 'test', $3, $4, 'ready', 1)`,
		mustUUID(vm.DatabaseID), account.ID, strings.Repeat("a", 64), vm.DatabaseID); err != nil {
		t.Fatal(err)
	}
}

func setRollingFixtureWindow(t *testing.T, store *PgStore, vm VM, opening time.Duration) {
	t.Helper()
	var now time.Time
	if err := store.pool.QueryRow(t.Context(), "SELECT clock_timestamp()").Scan(&now); err != nil {
		t.Fatal(err)
	}
	start := now.UTC().Add(opening)
	_, err := store.pool.Exec(t.Context(), `INSERT INTO native_postgres_maintenance_windows
		(database_id, weekday, start_minute, duration_minutes) VALUES ($1, $2, $3, 60)
		ON CONFLICT (database_id) DO UPDATE SET weekday = EXCLUDED.weekday, start_minute = EXCLUDED.start_minute`,
		mustUUID(vm.DatabaseID), int(start.Weekday()), start.Hour()*60+start.Minute())
	if err != nil {
		t.Fatal(err)
	}
}

func rollingFixtureOutcome(t *testing.T, store *PgStore, vm VM) (phase, outcome, reason string) {
	t.Helper()
	err := store.pool.QueryRow(t.Context(), `SELECT phase, outcome, reason FROM native_postgres_base_refreshes
		WHERE database_id = $1 ORDER BY started_at DESC LIMIT 1`, mustUUID(vm.DatabaseID)).Scan(&phase, &outcome, &reason)
	if err != nil {
		t.Fatal(err)
	}
	return phase, outcome, reason
}

func TestRollingMaintenanceResumesUnconfirmedHandoff(t *testing.T) {
	store, s, vmm, vm := rollingMaintenanceFixture(t)
	ctx := t.Context()
	vmm.stale[vm.NodeID+"/"+vm.DatabaseID] = true
	vmm.stale[vm.StandbyNode+"/"+vm.DatabaseID] = true
	setRollingFixtureWindow(t, store, vm, time.Hour)
	before := vmm.callCount("stop")
	if _, err := s.SampleStorage(ctx); err != nil || vmm.callCount("stop") != before {
		t.Fatalf("outside the window: %v, %s", err, vmm.nodeCalls())
	}

	setRollingFixtureWindow(t, store, vm, -5*time.Minute)
	// Another scheduler is still changing physical replication. Observe
	// the stale database, but leave the standby alone until it releases.
	other, err := store.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer other.Rollback(context.Background())
	if locked, err := tryStandbyOperation(ctx, other, vm.DatabaseID); err != nil || !locked {
		t.Fatalf("competing standby operation: %v, %v", locked, err)
	}
	if _, err := s.SampleStorage(ctx); err != nil || vmm.callCount("stop") != before {
		t.Fatalf("maintenance overlapped a standby operation: %v, %s", err, vmm.nodeCalls())
	}
	if err := other.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	s.resumeRollingRefreshes(ctx, store)
	s.standbyWG.Wait()
	phase, outcome, _ := rollingFixtureOutcome(t, store, vm)
	if phase != refreshingStandby || outcome != "" || vmm.callCount("promote") != 0 {
		t.Fatalf("after the standby restart: %s, %s, %s", phase, outcome, vmm.nodeCalls())
	}
	if events := vmm.nodeCalls(); strings.Contains(events, "stop@"+vm.NodeID) {
		t.Fatalf("the primary stopped before verification: %s", events)
	}
	if admitted, err := store.beginRollingRefresh(ctx, vm); err != nil || admitted {
		t.Fatalf("duplicate admission: %v, %v", admitted, err)
	}
	if armed, err := s.advanceRollingRefresh(ctx, store, vm.DatabaseID); err != nil || !armed {
		t.Fatalf("arm handoff: %v, %v", armed, err)
	}
	if _, err := s.Wake(ctx, vm.DatabaseID); !errors.Is(err, ErrConflict) {
		t.Fatalf("old primary reopened during handoff: %v", err)
	}
	// These are the states the previous scheduler recovers as interrupted
	// wake/park operations. A restart there must leave both placements alone.
	legacyCandidates, err := store.List(ctx, StateWaking, StateParking)
	if err != nil || len(legacyCandidates) != 0 {
		t.Fatalf("previous scheduler could recover the fenced primary: %+v, %v", legacyCandidates, err)
	}
	if err := s.Park(ctx, vm.DatabaseID); !errors.Is(err, ErrConflict) {
		t.Fatalf("ordinary park superseded the rolling fence: %v", err)
	}

	vmm.losePromotionReply.Store(true)
	s.resumeRollingRefreshes(ctx, store)
	phase, outcome, reason := rollingFixtureOutcome(t, store, vm)
	if phase != switchingOver || outcome != "" || reason != "standby_promotion_unconfirmed" || vmm.callCount("promote") != 1 {
		t.Fatalf("after an unconfirmed promotion: %s, %s, %s", phase, outcome, reason)
	}
	if parked, err := store.Get(ctx, vm.DatabaseID); err != nil || parked.State != stateMaintenanceHandoff || parked.NodeID != vm.NodeID {
		t.Fatalf("old routing fence lost: %+v, %v", parked, err)
	}
	vmm.mu.Lock()
	vmm.down[vm.StandbyNode] = true
	vmm.mu.Unlock()
	s.resumeRollingRefreshes(ctx, store)
	if _, err := s.Wake(ctx, vm.DatabaseID); !errors.Is(err, ErrConflict) {
		t.Fatalf("standby loss reopened the old primary after an uncertain promotion: %v", err)
	}
	vmm.mu.Lock()
	vmm.down[vm.StandbyNode] = false
	vmm.mu.Unlock()

	// Losing catalog evidence after an ambiguous promotion must not
	// retire the durable fence or make the old primary eligible to wake.
	for _, synced := range []bool{false, true} {
		if _, err := store.pool.Exec(ctx, "UPDATE database_vms SET standby_synced = $2 WHERE database_id = $1",
			mustUUID(vm.DatabaseID), synced); err != nil {
			t.Fatal(err)
		}
		if !synced {
			s.resumeRollingRefreshes(ctx, store)
			phase, outcome, reason = rollingFixtureOutcome(t, store, vm)
			if phase != switchingOver || outcome != "" || reason != "synchronized_standby_unconfirmed" {
				t.Fatalf("lost synchronization proof cleared the fence: %s, %s, %s", phase, outcome, reason)
			}
		}
	}

	restarted := New(store, vmm, s.cfg, nil)
	t.Cleanup(restarted.standbyWG.Wait)
	if err := restarted.Recover(ctx); err != nil {
		t.Fatal(err)
	}
	restarted.standbyWG.Wait()
	restarted.resumeRollingRefreshes(ctx, store)
	after, err := restarted.Wake(ctx, vm.DatabaseID)
	phase, outcome, reason = rollingFixtureOutcome(t, store, vm)
	if err != nil || after.NodeID != vm.StandbyNode || !after.StandbySynced || phase != restoringRedundancy ||
		outcome != "succeeded" || reason != "" || vmm.callCount("promote") != 1 {
		t.Fatalf("after recovery: %+v, %v, %s, %s, %s, events %s", after, err, phase, outcome, reason, vmm.nodeCalls())
	}
}

func TestRollingMaintenanceRejectsStandbyStillOnOldImage(t *testing.T) {
	store, s, vmm, vm := rollingMaintenanceFixture(t)
	ctx := t.Context()
	vmm.stale[vm.NodeID+"/"+vm.DatabaseID] = true
	vmm.splitImages.Store(true)
	setRollingFixtureWindow(t, store, vm, -5*time.Minute)
	if _, err := s.SampleStorage(ctx); err != nil {
		t.Fatal(err)
	}
	phase, outcome, reason := rollingFixtureOutcome(t, store, vm)
	if phase != refreshingStandby || outcome != "" || reason != "awaiting_matching_node_images" || vmm.callCount("stop") != 0 {
		t.Fatalf("mismatched node images disturbed traffic: %s, %s, %s, %s", phase, outcome, reason, vmm.nodeCalls())
	}

	vmm.splitImages.Store(false)
	vmm.stale[vm.StandbyNode+"/"+vm.DatabaseID] = true
	s.resumeRollingRefreshes(ctx, store)
	s.standbyWG.Wait()
	vmm.stale[vm.StandbyNode+"/"+vm.DatabaseID] = true
	s.resumeRollingRefreshes(ctx, store)
	phase, outcome, reason = rollingFixtureOutcome(t, store, vm)
	if phase != refreshingStandby || outcome != "still_stale" || reason != "standby_image_still_outdated" ||
		vmm.callCount("promote") != 0 || strings.Contains(vmm.nodeCalls(), "stop@"+vm.NodeID) {
		t.Fatalf("unverified standby disturbed primary: %s, %s, %s, %s", phase, outcome, reason, vmm.nodeCalls())
	}
	if _, err := s.SampleStorage(ctx); err != nil || vmm.callCount("promote") != 0 {
		t.Fatalf("same window retried failed attempt: %v, %s", err, vmm.nodeCalls())
	}
}

func TestRollingMaintenanceReopensPrimaryBeforeStoppingIt(t *testing.T) {
	store, s, vmm, vm := rollingMaintenanceFixture(t)
	ctx := t.Context()
	vmm.stale[vm.NodeID+"/"+vm.DatabaseID] = true
	setRollingFixtureWindow(t, store, vm, -5*time.Minute)
	if admitted, err := store.beginRollingRefresh(ctx, vm); err != nil || !admitted {
		t.Fatalf("admit handoff: %v, %v", admitted, err)
	}
	if armed, err := s.advanceRollingRefresh(ctx, store, vm.DatabaseID); err != nil || !armed {
		t.Fatalf("arm handoff: %v, %v", armed, err)
	}
	vmm.stale[vm.StandbyNode+"/"+vm.DatabaseID] = true
	s.resumeRollingRefreshes(ctx, store)
	phase, outcome, reason := rollingFixtureOutcome(t, store, vm)
	after, err := s.Wake(ctx, vm.DatabaseID)
	if err != nil || after.NodeID != vm.NodeID || after.State != StateRunning ||
		phase != switchingOver || outcome != "still_stale" || reason != "standby_image_changed_during_handoff" ||
		vmm.callCount("stop") != 0 || vmm.callCount("promote") != 0 {
		t.Fatalf("cancel handoff: %+v, %v, %s, %s, %s, events %s", after, err, phase, outcome, reason, vmm.nodeCalls())
	}
	var reserved bool
	if err := store.pool.QueryRow(ctx, `SELECT configured_admission_ceiling_mb IS NOT NULL
		FROM database_nodes WHERE id = $1`, vm.NodeID).Scan(&reserved); err != nil || reserved {
		t.Fatalf("canceled handoff retained a reservation: %v, %v", reserved, err)
	}
}

func TestRollingHandoffFailsOverDeclaredLostPrimary(t *testing.T) {
	store, s, vmm, vm := rollingMaintenanceFixture(t)
	ctx := t.Context()
	vmm.stale[vm.NodeID+"/"+vm.DatabaseID] = true
	setRollingFixtureWindow(t, store, vm, -5*time.Minute)
	if admitted, err := store.beginRollingRefresh(ctx, vm); err != nil || !admitted {
		t.Fatalf("admit handoff: %v, %v", admitted, err)
	}
	for range 2 {
		if progressed, err := s.advanceRollingRefresh(ctx, store, vm.DatabaseID); err != nil || !progressed {
			t.Fatalf("prepare handoff: %v, %v", progressed, err)
		}
	}
	primary, err := s.node(ctx, vm.NodeID)
	if err != nil {
		t.Fatal(err)
	}
	originalCeiling := primary.AdmissionCeilingMB
	checkCeiling := func(configured, legacy int) {
		t.Helper()
		var raw int
		err := store.pool.QueryRow(ctx, `SELECT admission_ceiling_mb FROM database_nodes WHERE id = $1`, primary.ID).Scan(&raw)
		if err != nil || raw != legacy {
			t.Fatalf("previous scheduler's ceiling = %d, want %d: %v", raw, legacy, err)
		}
		current, err := s.node(ctx, primary.ID)
		if err != nil || current.AdmissionCeilingMB != configured {
			t.Fatalf("current scheduler's configured ceiling = %d, want %d: %v", current.AdmissionCeilingMB, configured, err)
		}
	}
	reserved := int(billableMB(vm.MemMiB))
	checkCeiling(originalCeiling, originalCeiling-reserved)

	// The old registry writes the configured limit to the original column.
	// That write has to preserve the reservation while changing the limit.
	primary.AdmissionCeilingMB = 2048
	if err := store.UpsertNode(ctx, primary); err != nil {
		t.Fatal(err)
	}
	checkCeiling(2048, 2048-reserved)

	vmm.mu.Lock()
	vmm.down[primary.ID] = true
	vmm.mu.Unlock()
	s.resumeRollingRefreshes(ctx, store)
	phase, outcome, reason := rollingFixtureOutcome(t, store, vm)
	if phase != switchingOver || outcome != "" || reason != "primary_stop_unconfirmed" || vmm.callCount("promote") != 0 {
		t.Fatalf("unconfirmed primary loss: %s, %s, %s, events %s", phase, outcome, reason, vmm.nodeCalls())
	}
	checkCeiling(2048, 2048-reserved)

	primary.Lifecycle = NodeUnavailable
	if err := store.UpsertNode(ctx, primary); err != nil {
		t.Fatal(err)
	}
	s.resumeRollingRefreshes(ctx, store)
	after, err := s.Wake(ctx, vm.DatabaseID)
	if err != nil || after.State != StateRunning || after.NodeID != vm.StandbyNode || vmm.callCount("promote") != 1 {
		t.Fatalf("fail over fenced primary: %+v, %v, events %s", after, err, vmm.nodeCalls())
	}
	checkCeiling(2048, 2048)
}

func TestRollingHandoffRecoversPrimaryIfPreparedStandbyIsLost(t *testing.T) {
	store, s, vmm, vm := rollingMaintenanceFixture(t)
	ctx := t.Context()
	vmm.stale[vm.NodeID+"/"+vm.DatabaseID] = true
	setRollingFixtureWindow(t, store, vm, -5*time.Minute)
	if admitted, err := store.beginRollingRefresh(ctx, vm); err != nil || !admitted {
		t.Fatalf("admit handoff: %v, %v", admitted, err)
	}
	for range 2 {
		if progressed, err := s.advanceRollingRefresh(ctx, store, vm.DatabaseID); err != nil || !progressed {
			t.Fatalf("prepare handoff: %v, %v", progressed, err)
		}
	}
	standby, err := s.node(ctx, vm.StandbyNode)
	if err != nil {
		t.Fatal(err)
	}
	standby.Lifecycle = NodeUnavailable
	if err := store.UpsertNode(ctx, standby); err != nil {
		t.Fatal(err)
	}
	restarted := New(store, vmm, s.cfg, nil)
	t.Cleanup(restarted.standbyWG.Wait)
	restarted.resumeRollingRefreshes(ctx, store)
	phase, outcome, reason := rollingFixtureOutcome(t, store, vm)
	got, err := restarted.Wake(ctx, vm.DatabaseID)
	if err != nil || got.State != StateRunning || got.NodeID != vm.NodeID ||
		phase != switchingOver || outcome != "interrupted" || reason != "database_node_unavailable" ||
		vmm.callCount("stop") != 0 || vmm.callCount("promote") != 0 {
		t.Fatalf("reopen original primary: %+v, %v, %s, %s, %s, events %s", got, err, phase, outcome, reason, vmm.nodeCalls())
	}
}
