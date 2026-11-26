package managedpostgres

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
)

type scheduledGrowthProvider struct {
	fakeProvider
	updates []UpdateRequest
}

func (p *scheduledGrowthProvider) Update(_ context.Context, req UpdateRequest) (ObservedDatabase, error) {
	p.updates = append(p.updates, req)
	return ObservedDatabase{}, ErrUnavailable
}

func TestAutoGrowScheduledCatalogAdmission(t *testing.T) {
	store, pool, ctx, accountID := postgresStoreFixture(t)
	const source, ceiling = 1 << 30, 2 << 30
	id, signature := uuid.NewString(), strings.Repeat("a", 64)
	now := time.Now().UTC()
	start := now.Add(-5 * time.Minute).Truncate(time.Minute)
	epoch, policyAt := now.Add(-10*time.Minute).Truncate(time.Microsecond), now.Add(-5*time.Minute).Truncate(time.Microsecond)
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(ctx, sql, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`INSERT INTO database_nodes (id, target_url, admission_ceiling_mb, volume_capacity_bytes)
		VALUES ('db-a', 'unix:///a', 4096, 21474836480)`)
	exec(`INSERT INTO database_vms (database_id, node_id, postgres_major, vcpu_count, mem_mib,
		volume_bytes, state, always_on, state_changed_at)
		VALUES ($1, 'db-a', 17, 1, 384, $2, 'running', true, $3)`, id, source, epoch)
	exec(`INSERT INTO managed_postgres_databases (id, account_id, name, region, postgres_major, service_class, availability,
		backend_id, backend_fingerprint, provider_resource_id, data_resource_id, state, observed_generation, storage_limit_bytes)
		VALUES ($1, $2, 'scheduled-growth-db', 'us-east-1', 17, 'development', 'single_zone',
			'native', $3, $4, $4, 'ready', 1, $5)`, id, accountID, signature, id, source)
	exec(`INSERT INTO native_postgres_storage_samples (database_id, role, node_id, state_changed_at, allocated_bytes,
		total_bytes, free_bytes, low, read_only, sampled_at, low_since)
		VALUES ($1, 'primary', 'db-a', $2, $3, $3, 1000, true, false, $4, $5)`,
		id, epoch, source, now.Add(-time.Second), now.Add(-2*time.Minute))
	provider := &scheduledGrowthProvider{}
	caps := testCapabilities()
	caps.StorageResize = true
	registry := &Registry{backends: map[string]Backend{"native": {
		Driver: "firecracker", Fingerprint: signature, Provider: provider, Capabilities: caps,
	}}}
	allowance := int64(source)
	service, err := NewService(registry, store, ServiceOptions{
		OperationsEnabled: func() bool { return true },
		AutoGrowAllowance: func(context.Context, string) (int64, error) { return allowance, nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.SetAutoGrow(ctx, accountID, id, ceiling); err != nil {
		t.Fatal(err)
	}
	// The observations in this fixture model pressure sustained after opt-in.
	exec(`UPDATE native_postgres_auto_grow SET updated_at = $2 WHERE database_id = $1`, id, policyAt)
	weekDay, minute := int(start.Weekday()), start.Hour()*60+start.Minute()
	window := &api.ManagedPostgresMaintenanceWindow{Weekday: (weekDay + 1) % 7, StartMinute: minute, DurationMinutes: 60}
	if _, err := service.SetMaintenanceWindow(ctx, accountID, id, window); err != nil {
		t.Fatal(err)
	}
	if err := service.autoGrowSweep(ctx, 10); err != nil || len(provider.updates) != 0 {
		t.Fatalf("outside-window sweep dispatched %d updates: %v", len(provider.updates), err)
	}
	window.Weekday = weekDay
	if _, err := service.SetMaintenanceWindow(ctx, accountID, id, window); err != nil {
		t.Fatal(err)
	}
	if err := service.autoGrowSweep(ctx, 10); err != nil || len(provider.updates) != 0 {
		t.Fatalf("grow above allowance dispatched %d updates: %v", len(provider.updates), err)
	}
	database, err := service.Get(ctx, accountID, id)
	if err != nil {
		t.Fatal(err)
	}
	status, err := service.AutoGrowStatus(ctx, database, &api.ManagedPostgresStorageStatus{
		Primary: &api.ManagedPostgresVolumeStatus{Fresh: true, Low: true},
	})
	if err != nil || status == nil || status.Reason != "storage_allowance" {
		t.Fatalf("quota rejection status = %+v, %v", status, err)
	}

	// Model the first sweep of a window with sufficient storage allowance.
	exec(`UPDATE native_postgres_auto_grow SET checked_at = NULL WHERE database_id = $1`, id)
	allowance = ceiling
	if err := service.autoGrowSweep(ctx, 10); err != nil || len(provider.updates) != 1 {
		t.Fatalf("in-window sweep dispatched %d updates: %v", len(provider.updates), err)
	}
	req := provider.updates[0]
	if !req.AutomaticResize || req.Spec.ScaleToZero || req.Spec.StorageLimitBytes != ceiling {
		t.Fatalf("unexpected storage resize intent: %+v", req)
	}
	var savedStart, savedEpoch time.Time
	if err := pool.QueryRow(ctx, `SELECT window_start, source_running_at FROM native_postgres_scheduled_growth WHERE resize_id = $1`,
		strings.TrimPrefix(req.IdempotencyKey, "resize-")).Scan(&savedStart, &savedEpoch); err != nil ||
		!savedStart.Equal(start) || !savedEpoch.Equal(epoch) {
		t.Fatalf("scheduled authorization = %v, %v, %v", savedStart, savedEpoch, err)
	}
}
