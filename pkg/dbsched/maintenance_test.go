package dbsched

import (
	"context"
	"github.com/onebox-faas/faas/pkg/dbguest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestMaintenanceWindowAdmissionAndRecovery(t *testing.T) {
	store := storeFactories(t)["pg"](t).(*PgStore)
	ctx := context.Background()
	seedNodes(t, store, Node{ID: "db-a", TargetURL: "unix:///a", AdmissionCeilingMB: 4096, VolumeCapacityBytes: 20 << 30, Lifecycle: NodeActive})
	vmm := newFakeVMM()
	scheduler := New(store, vmm, Config{}, nil)
	vm := newVM(1<<30, 384)
	vm.AlwaysOn = true
	if _, err := store.Place(ctx, vm); err != nil {
		t.Fatal(err)
	}
	if err := store.Transition(ctx, vm.DatabaseID, []State{StateProvisioning}, StateRunning, TransitionOptions{Bootstrapped: true}); err != nil {
		t.Fatal(err)
	}
	var err error
	vm, err = store.Get(ctx, vm.DatabaseID)
	if err != nil {
		t.Fatal(err)
	}
	vmm.running[vm.DatabaseID], vmm.volumes[vm.DatabaseID] = true, true
	vmm.stale[vm.NodeID+"/"+vm.DatabaseID] = true
	account, err := state.NewPgStore(store.pool).CreateAccount(ctx, uuid.NewString()+"@maintenance.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.pool.Exec(ctx, `
		INSERT INTO managed_postgres_databases
			(id, account_id, name, region, postgres_major, service_class, availability, backend_id,
			 backend_fingerprint, provider_resource_id, state, observed_generation)
		VALUES ($1, $2, 'maintenance-db', 'eu', 17, 'development', 'single_zone', 'test', $3, $4, 'ready', 1)`,
		mustUUID(vm.DatabaseID), account.ID, strings.Repeat("a", 64), vm.DatabaseID); err != nil {
		t.Fatal(err)
	}
	setWindow := func(start time.Time) {
		t.Helper()
		if _, err := store.pool.Exec(ctx, `
			INSERT INTO native_postgres_maintenance_windows (database_id, weekday, start_minute, duration_minutes)
			VALUES ($1, $2, $3, 60) ON CONFLICT (database_id) DO UPDATE
			SET weekday = EXCLUDED.weekday, start_minute = EXCLUDED.start_minute`,
			mustUUID(vm.DatabaseID), int(start.Weekday()), start.Hour()*60+start.Minute()); err != nil {
			t.Fatal(err)
		}
	}
	var now time.Time
	if err := store.pool.QueryRow(ctx, `SELECT clock_timestamp()`).Scan(&now); err != nil {
		t.Fatal(err)
	}
	now = now.UTC()
	setWindow(now.AddDate(0, 0, 1))
	if _, err := scheduler.SampleStorage(ctx); err != nil {
		t.Fatal(err)
	}
	var pending bool
	if err := store.pool.QueryRow(ctx, `SELECT stale_since IS NOT NULL FROM native_postgres_maintenance_windows WHERE database_id = $1`,
		mustUUID(vm.DatabaseID)).Scan(&pending); err != nil || !pending {
		t.Fatalf("stale observation persisted = %v, %v", pending, err)
	}
	if vmm.callCount("stop") != 0 {
		t.Fatal("database stopped outside the customer's window")
	}

	setWindow(now.Add(-5 * time.Minute))
	if err := store.Touch(ctx, vm.DatabaseID, now); err != nil {
		t.Fatal(err)
	}
	refresh, admitted, err := store.beginBaseRefresh(ctx, vm, now.Add(-time.Hour), 0, dbguest.Response{Stale: true})
	if err != nil || !admitted || refresh == nil {
		t.Fatalf("busy database was not admitted inside its window: %v, %v, %v", refresh, admitted, err)
	}
	if _, admitted, err := store.beginBaseRefresh(ctx, vm, now, 0, dbguest.Response{Stale: true}); err != nil || admitted {
		t.Fatalf("second admission: %v, %v", admitted, err)
	}
	// A scheduler died after admitting the refresh. Its replacement sees
	// the original running guest and never repeats the stop.
	if err := scheduler.Recover(ctx); err != nil {
		t.Fatal(err)
	}
	vm, err = store.Get(ctx, vm.DatabaseID)
	if err != nil || vm.State != StateRunning || vmm.callCount("stop") != 0 {
		t.Fatalf("recovered VM = %+v, %v, stops %d", vm, err, vmm.callCount("stop"))
	}
	if _, admitted, err := store.beginBaseRefresh(ctx, vm, now, 0, dbguest.Response{Stale: true}); err != nil || admitted {
		t.Fatalf("repeat attempt in the same window: %v, %v", admitted, err)
	}

	// A different window admits a new attempt. Simulate a crash after
	// boot completed but before the outcome was written.
	setWindow(now.Add(-10 * time.Minute))
	refresh, admitted, err = store.beginBaseRefresh(ctx, vm, now.Add(-time.Hour), 0, dbguest.Response{Stale: true})
	if err != nil || !admitted || refresh == nil {
		t.Fatalf("second window admission: %v, %v, %v", refresh, admitted, err)
	}
	for _, step := range [][2]State{{StateParking, StateParked}, {StateParked, StateWaking}, {StateWaking, StateRunning}} {
		if err := store.Transition(ctx, vm.DatabaseID, []State{step[0]}, step[1], TransitionOptions{}); err != nil {
			t.Fatal(err)
		}
	}
	delete(vmm.stale, vm.NodeID+"/"+vm.DatabaseID)
	if err := scheduler.Recover(ctx); err != nil {
		t.Fatal(err)
	}
	var outcome string
	if err := store.pool.QueryRow(ctx, `SELECT outcome FROM native_postgres_base_refreshes WHERE database_id = $1 AND window_start = $2`,
		mustUUID(vm.DatabaseID), refresh.Window.Start).Scan(&outcome); err != nil || outcome != "succeeded" || vmm.callCount("stop") != 0 {
		t.Fatalf("observed ready current guest: outcome %q, stops %d, error %v", outcome, vmm.callCount("stop"), err)
	}

	// A boot can still report a stale launch. It consumes the window's
	// attempt and stays pending for the next one.
	setWindow(now.Add(-15 * time.Minute))
	vmm.stale[vm.NodeID+"/"+vm.DatabaseID] = true
	for range 2 {
		if _, err := scheduler.SampleStorage(ctx); err != nil {
			t.Fatal(err)
		}
	}
	if vmm.callCount("stop") != 1 || vmm.callCount("start") != 1 {
		t.Fatalf("scheduled dispatch attempted %d stops and %d starts", vmm.callCount("stop"), vmm.callCount("start"))
	}
	if err := store.pool.QueryRow(ctx, `SELECT outcome FROM native_postgres_base_refreshes
		WHERE database_id = $1 ORDER BY started_at DESC LIMIT 1`, mustUUID(vm.DatabaseID)).Scan(&outcome); err != nil || outcome != "still_stale" {
		t.Fatalf("stale launch after scheduled restart: outcome %q, error %v", outcome, err)
	}
}
