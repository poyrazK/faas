package managedpostgres

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestNativeMaintenanceWindowPolicy(t *testing.T) {
	store, pool, ctx, accountID := postgresStoreFixture(t)
	id, otherID := uuid.NewString(), uuid.NewString()
	fingerprint := strings.Repeat("a", 64)
	for _, fixture := range []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO database_nodes (id, target_url, admission_ceiling_mb, volume_capacity_bytes)
			VALUES ('db-a', 'unix:///a', 4096, 21474836480)`, nil},
		{`INSERT INTO database_vms (database_id, node_id, postgres_major, vcpu_count, mem_mib, volume_bytes, state, always_on)
			VALUES ($1, 'db-a', 17, 1, 384, 1073741824, 'running', true)`, []any{id}},
		{`INSERT INTO managed_postgres_databases
			(id, account_id, name, region, postgres_major, service_class, availability, backend_id,
			 backend_fingerprint, provider_resource_id, state, observed_generation)
			VALUES ($1, $2, 'maintenance-db', 'eu', 17, 'development', 'single_zone', 'native', $3, $4, 'ready', 1)`,
			[]any{id, accountID, fingerprint, id}},
	} {
		if _, err := pool.Exec(ctx, fixture.sql, fixture.args...); err != nil {
			t.Fatal(err)
		}
	}
	service := Service{
		store:    store,
		registry: &Registry{backends: map[string]Backend{"native": {Driver: "firecracker", Fingerprint: fingerprint}}},
		now:      func() time.Time { return time.Date(2026, 11, 22, 1, 0, 0, 0, time.UTC) },
	}
	window := &api.ManagedPostgresMaintenanceWindow{Weekday: 0, StartMinute: 120, DurationMinutes: 60}
	set := func(account string, window *api.ManagedPostgresMaintenanceWindow) (api.ManagedPostgresMaintenanceStatus, error) {
		return service.SetMaintenanceWindow(ctx, account, id, window)
	}
	if _, err := set(otherID, window); !errors.Is(err, ErrNotFound) {
		t.Fatalf("setting another account's window: %v", err)
	}
	if _, err := set(accountID, &api.ManagedPostgresMaintenanceWindow{}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("setting an invalid window: %v", err)
	}
	status, err := set(accountID, window)
	if err != nil || status.Window == nil || *status.Window != *window || status.WindowOpen ||
		status.WindowStart == nil || !status.WindowStart.Equal(service.now().Add(time.Hour)) {
		t.Fatalf("updated status = %+v, %v", status, err)
	}
	// Production uses the same setting and reports the durable rolling phase.
	for _, query := range []string{
		"UPDATE managed_postgres_databases SET service_class = 'production' WHERE id = $1",
		"UPDATE database_vms SET replicated = true WHERE database_id = $1",
	} {
		if _, err := pool.Exec(ctx, query, id); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO native_postgres_base_refreshes (database_id, window_start, window_end, source_running_at,
			node_id, started_at, recover_after, rolling_standby_node, rolling_image, phase, reason)
		VALUES ($1::uuid, now(), now()+interval '1 hour', now(), 'db-a', now(), now()+interval '1 hour',
			'db-b', 'refreshed.ext4:1', 'switching_over', 'primary_stop_unconfirmed')`, id); err != nil {
		t.Fatal(err)
	}
	status, err = set(accountID, window)
	if err != nil || status.LastRefresh == nil || status.LastRefresh.Outcome != "running" ||
		status.LastRefresh.Phase != "switching_over" || status.LastRefresh.Reason != "primary_stop_unconfirmed" {
		t.Fatalf("production maintenance status = %+v, %v", status, err)
	}
	status, err = set(accountID, nil)
	if err != nil || status.Window != nil || status.WindowStart != nil {
		t.Fatalf("removed status = %+v, %v", status, err)
	}
}
