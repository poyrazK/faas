package dbsched

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/dbguest"
	"github.com/onebox-faas/faas/pkg/state"
)

type growthVMM struct {
	*fakeVMM
	beforeStop func(context.Context, Node, string) error
}

func (f growthVMM) Stop(ctx context.Context, node Node, id string) error {
	if err := f.beforeStop(ctx, node, id); err != nil {
		return err
	}
	return f.fakeVMM.Stop(ctx, node, id)
}

func (f growthVMM) Start(ctx context.Context, node Node, vm VM) error {
	if err := f.fakeVMM.Start(ctx, node, vm); err != nil {
		return err
	}
	f.storage[vm.DatabaseID] = &dbguest.StorageStatus{TotalBytes: vm.VolumeBytes, FreeBytes: vm.VolumeBytes / 2}
	delete(f.stale, node.ID+"/"+vm.DatabaseID)
	return nil
}

func TestScheduledStorageGrowthAndBaseRefresh(t *testing.T) {
	ctx := t.Context()
	store := storeFactories(t)["pg"](t).(*PgStore)
	const source, target = 1 << 30, 2 << 30
	node := Node{ID: "db-a", TargetURL: "unix:///a", AdmissionCeilingMB: 4096, VolumeCapacityBytes: source, Lifecycle: NodeActive}
	seedNodes(t, store, node)
	vmm := growthVMM{fakeVMM: newFakeVMM(), beforeStop: func(ctx context.Context, node Node, id string) error {
		loads, err := store.Loads(ctx)
		if err != nil {
			return err
		}
		vm, err := store.Get(ctx, id)
		if err != nil {
			return err
		}
		if loads[node.ID].VolumeBytes != target || vm.VolumeBytes != source || vm.State != StateParking {
			t.Fatalf("stop without an intact capacity reservation: %+v, %+v", vm, loads)
		}
		return nil
	}}
	primary := New(store, vmm, Config{}, nil)
	replica := New(store, vmm, Config{}, nil)
	vm := newVM(source, 384)
	vm.AlwaysOn = true
	if _, err := store.Place(ctx, vm); err != nil {
		t.Fatal(err)
	}
	if err := store.Transition(ctx, vm.DatabaseID, []State{StateProvisioning}, StateRunning, TransitionOptions{Bootstrapped: true}); err != nil {
		t.Fatal(err)
	}
	account, err := state.NewPgStore(store.pool).CreateAccount(ctx, uuid.NewString()+"@scheduled-growth.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	key, resizeID := mustUUID(vm.DatabaseID), uuid.NewString()
	var now time.Time
	if err := store.pool.QueryRow(ctx, `SELECT clock_timestamp()`).Scan(&now); err != nil {
		t.Fatal(err)
	}
	now = now.UTC()
	epoch, policyAt := now.Add(-10*time.Minute).Truncate(time.Microsecond), now.Add(-5*time.Minute).Truncate(time.Microsecond)
	windowStart := now.Add(-5 * time.Minute).Truncate(time.Minute)
	fingerprint := strings.Repeat("a", 64)
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := store.pool.Exec(ctx, sql, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`UPDATE database_vms SET state_changed_at = $2, last_active_at = clock_timestamp() WHERE database_id = $1`, key, epoch)
	exec(`INSERT INTO managed_postgres_databases (id, account_id, name, region, postgres_major, service_class, availability,
		backend_id, backend_fingerprint, provider_resource_id, data_resource_id, state, observed_generation, storage_limit_bytes)
		VALUES ($1, $2, 'growth-db', 'eu', 17, 'development', 'single_zone', 'test', $3, $4, $4, 'ready', 1, $5)`,
		key, account.ID, fingerprint, vm.DatabaseID, source)
	exec(`INSERT INTO native_postgres_auto_grow (database_id, max_bytes, updated_at) VALUES ($1, $2, $3)`, key, target, policyAt)
	exec(`INSERT INTO native_postgres_maintenance_windows (database_id, weekday, start_minute, duration_minutes)
		VALUES ($1, $2, $3, 60)`, key, int(windowStart.AddDate(0, 0, 1).Weekday()), windowStart.Hour()*60+windowStart.Minute())
	exec(`INSERT INTO native_postgres_storage_samples (database_id, role, node_id, state_changed_at, allocated_bytes,
		total_bytes, free_bytes, low, read_only, sampled_at, low_since)
		VALUES ($1, 'primary', 'db-a', $2, $3, $3, 1000, true, false, $4, $5)`,
		key, epoch, source, now.Add(-time.Second), now.Add(-2*time.Minute))
	vmm.running[vm.DatabaseID], vmm.volumes[vm.DatabaseID], vmm.volNode[vm.DatabaseID] = true, true, node.ID
	vmm.storage[vm.DatabaseID] = &dbguest.StorageStatus{TotalBytes: source, FreeBytes: 1000, Low: true}
	vmm.stale[node.ID+"/"+vm.DatabaseID] = true

	// A ready catalog row and low space should leave this restart for the
	// growth planner, even though the running guest also reports stale.
	exec(`UPDATE native_postgres_maintenance_windows SET weekday = $2 WHERE database_id = $1`, key, int(windowStart.Weekday()))
	running, err := store.Get(ctx, vm.DatabaseID)
	if err != nil {
		t.Fatal(err)
	}
	if _, admitted, err := store.beginBaseRefresh(ctx, running, now, time.Minute, dbguest.Response{Stale: true}); err != nil || admitted {
		t.Fatalf("base-only restart should defer to growth planning: %v, %v", admitted, err)
	}
	exec(`UPDATE native_postgres_maintenance_windows SET weekday = $2 WHERE database_id = $1`, key, int(windowStart.AddDate(0, 0, 1).Weekday()))

	exec(`UPDATE managed_postgres_databases SET state = 'updating', desired_generation = 2 WHERE id = $1`, key)
	exec(`INSERT INTO managed_postgres_resizes (id, account_id, database_id, backend_id, backend_fingerprint,
		provider_resource_id, data_resource_id, source_spec, target_class, generation, created_at, target_storage_limit_bytes, automatic)
		VALUES ($1, $2, $3, 'test', $4, $5, $5, jsonb_build_object('Region', 'eu', 'Class', 'development',
			'Availability', 'single_zone', 'PostgresMajor', 17, 'ScaleToZero', false, 'StorageLimitBytes', $6::bigint, 'RestoreWindowSeconds', 0),
			'development', 2, clock_timestamp(), $7, true)`, resizeID, account.ID, key, fingerprint, vm.DatabaseID, source, target)
	exec(`INSERT INTO native_postgres_scheduled_growth (resize_id, window_start, window_end, source_running_at, policy_at)
		VALUES ($1, $2, $3, $4, $5)`, resizeID, windowStart, windowStart.Add(time.Hour), epoch, policyAt)

	if _, err := primary.AutoGrowStorage(ctx, vm.DatabaseID, resizeID, target); !errors.Is(err, ErrConflict) {
		t.Fatalf("outside the window: %v", err)
	}
	exec(`UPDATE native_postgres_maintenance_windows SET weekday = $2 WHERE database_id = $1`, key, int(windowStart.Weekday()))
	if _, err := primary.AutoGrowStorage(ctx, vm.DatabaseID, resizeID, target); !errors.Is(err, ErrNoCapacity) {
		t.Fatalf("without node capacity: %v", err)
	}
	if vmm.callCount("stop") != 0 {
		t.Fatal("a rejected resize stopped the database")
	}

	node.VolumeCapacityBytes = target
	seedNodes(t, store, node)
	vmm.growErr = errors.New("interrupted filesystem grow")
	if _, err := primary.AutoGrowStorage(ctx, vm.DatabaseID, resizeID, target); !errors.Is(err, ErrStorageResizePending) {
		t.Fatalf("interrupted grow did not stay pending: %v", err)
	}
	if _, err := store.AdmitWake(ctx, vm.DatabaseID, false); !errors.Is(err, ErrStorageResizePending) {
		t.Fatalf("wake during interrupted grow: %v", err)
	}
	loads, err := store.Loads(ctx)
	if err != nil || loads[node.ID].VolumeBytes != target {
		t.Fatalf("node capacity after interrupted growth: %+v, %v", loads, err)
	}
	exec(`DELETE FROM native_postgres_maintenance_windows WHERE database_id = $1`, key)
	vmm.growErr = nil
	got, err := replica.AutoGrowStorage(ctx, vm.DatabaseID, resizeID, target)
	if err != nil || got.VolumeBytes != target || got.ResizePending || got.State != StateRunning {
		t.Fatalf("resumed resize = %+v, %v", got, err)
	}
	if _, err := primary.AutoGrowStorage(ctx, vm.DatabaseID, resizeID, target); err != nil {
		t.Fatalf("replaying the completed resize: %v", err)
	}
	if vmm.callCount("stop") != 1 || vmm.callCount("start") != 1 {
		t.Fatalf("growth and refresh took %d stops and %d starts", vmm.callCount("stop"), vmm.callCount("start"))
	}
	if observed := vmm.storage[vm.DatabaseID]; observed.ReadOnly || observed.TotalBytes != target || vmm.stale[node.ID+"/"+vm.DatabaseID] {
		t.Fatalf("replacement guest storage = %+v, stale %v", observed, vmm.stale[node.ID+"/"+vm.DatabaseID])
	}
	var outcome string
	var both bool
	if err := store.pool.QueryRow(ctx, `SELECT outcome, refresh_base FROM native_postgres_base_refreshes WHERE resize_id = $1`,
		resizeID).Scan(&outcome, &both); err != nil || outcome != "succeeded" || !both {
		t.Fatalf("combined operation outcome = %q, base refresh %v, %v", outcome, both, err)
	}
}
