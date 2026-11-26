package dbstorage

import (
	"bytes"
	"context"
	"errors"
	"os"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestStorageAlertsSurviveDeliveryRestartAndRecoverAfterGrowth(t *testing.T) {
	dsn := os.Getenv("FAAS_TEST_NATIVE_STORAGE_DSN")
	if dsn == "" {
		t.Skip("set FAAS_TEST_NATIVE_STORAGE_DSN to a disposable PostgreSQL database")
	}
	config, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	ctx := t.Context()
	admin, err := pgxpool.NewWithConfig(ctx, config.Copy())
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	schema := "storage_alert_" + uuid.NewString()[:8]
	if _, err := admin.Exec(ctx, "CREATE SCHEMA "+schema); err != nil {
		t.Fatal(err)
	}
	defer func() { _, _ = admin.Exec(context.Background(), "DROP SCHEMA "+schema+" CASCADE") }()
	config.ConnConfig.RuntimeParams["search_path"] = schema
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(ctx, sql, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`CREATE TABLE database_vms (database_id uuid PRIMARY KEY, node_id text, standby_node text,
		standby_running boolean, state text, resize_pending boolean, state_changed_at timestamptz, volume_bytes bigint);
		CREATE TABLE managed_postgres_databases (id uuid PRIMARY KEY, org_id uuid, name text, provider_resource_id text,
			state text, clone_resource_role text, scale_to_zero boolean);
		CREATE TABLE managed_postgres_resizes (database_id uuid, state text, target_storage_limit_bytes bigint);
		CREATE TABLE native_postgres_auto_grow (database_id uuid PRIMARY KEY, max_bytes bigint, reason text, checked_at timestamptz);
		CREATE TABLE native_postgres_maintenance_windows (database_id uuid PRIMARY KEY);
		CREATE TABLE orgs (id uuid PRIMARY KEY, personal_org boolean, personal_owner_account_id uuid,
			deleted_pending boolean, deletion_requested_at timestamptz);
		CREATE TABLE org_memberships (org_id uuid, account_id uuid, role text, removed_at timestamptz);
		CREATE TABLE accounts (id uuid PRIMARY KEY, email text, status text, deletion_requested_at timestamptz, email_verified_at timestamptz, plan text NOT NULL DEFAULT 'pro');`)
	for _, path := range []string{"20261126195619201_native_postgres_storage_samples.sql", "20261126195619210_native_postgres_storage_alerts.sql"} {
		raw, err := os.ReadFile("../../migrations/" + path)
		if err != nil {
			t.Fatal(err)
		}
		raw, _, _ = bytes.Cut(raw, []byte("-- +goose Down"))
		exec(string(raw))
		if path == "20261126195619201_native_postgres_storage_samples.sql" {
			exec(`ALTER TABLE native_postgres_storage_samples ADD COLUMN low_since timestamptz;`)
		}
	}

	id, org, owner, former := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	now := time.Now().UTC().Truncate(time.Microsecond).Add(-time.Minute)
	epoch := now.Add(-time.Hour)
	exec(`INSERT INTO database_vms VALUES ($1, 'db-a', 'db-b', true, 'running', false, $2, 1000)`, id, epoch)
	exec(`INSERT INTO managed_postgres_databases VALUES ($1, $2, 'orders', $1::uuid::text, 'ready', 'target', false)`, id, org)
	exec(`INSERT INTO orgs VALUES ($1, false, NULL, false, NULL)`, org)
	exec(`INSERT INTO accounts VALUES ($1, 'owner@example.test', 'active', NULL, $3), ($2, 'former@example.test', 'active', NULL, $3)`, owner, former, epoch)
	exec(`INSERT INTO org_memberships VALUES ($1, $2, 'owner', NULL), ($1, $3, 'admin', NULL)`, org, owner, former)
	exec(`INSERT INTO native_postgres_auto_grow VALUES ($1, 2000, 'storage_allowance', $2)`, id, now)
	exec(`INSERT INTO native_postgres_maintenance_windows VALUES ($1)`, id)

	sample := Sample{DatabaseID: id.String(), Role: "primary", NodeID: "db-a", StateChangedAt: epoch,
		AllocatedBytes: 1000, TotalBytes: 980, FreeBytes: 300, SampledAt: now}
	record := func(s Sample) {
		t.Helper()
		if err := Record(ctx, pool, []Sample{s}); err != nil {
			t.Fatal(err)
		}
	}
	var pgID pgtype.UUID
	if err := pgID.Scan(id.String()); err != nil {
		t.Fatal(err)
	}
	history := func(want ...string) []Alert {
		t.Helper()
		all, err := ReadAlerts(ctx, pool, []pgtype.UUID{pgID})
		if err != nil {
			t.Fatal(err)
		}
		var states []string
		for _, event := range all {
			states = append(states, event.State)
		}
		if !slices.Equal(states, want) {
			t.Fatalf("history = %v, want %v", states, want)
		}
		return all
	}
	record(sample)
	standby := sample
	standby.Role, standby.NodeID, standby.ReadOnly = "standby", "db-b", true
	record(standby)
	sample.Low, sample.FreeBytes, sample.SampledAt = true, 120, now.Add(time.Second)
	record(sample)
	record(sample)
	history()
	sample.SampledAt = now.Add(2 * time.Second)
	record(sample)
	if latest := history("low_space")[0]; latest.GrowthReason != "storage_allowance" || latest.FreeBytes != 120 {
		t.Fatalf("warning evidence = %+v", latest)
	}

	var delivered []string
	received := make(map[string]bool)
	send := func(_ context.Context, a Alert, recipient AlertRecipient) error {
		if recipient.AccountID != owner.String() || recipient.OrgID != org.String() || recipient.Plan != "pro" || recipient.Email != "owner@example.test" {
			return errors.New("sent to the wrong owner")
		}
		if !received[a.ID] {
			received[a.ID] = true
			delivered = append(delivered, a.State)
		}
		return nil
	}
	claimed, err := claimAlert(ctx, pool)
	if err != nil {
		t.Fatal(err)
	}
	if err := deliverToAlertOwner(ctx, pool, claimed, send); err != nil {
		t.Fatal(err)
	}
	// Model a lost acknowledgement or worker exit after transport acceptance.
	exec(`INSERT INTO managed_postgres_resizes VALUES ($1, 'pending', 2000)`, id)
	sample.ReadOnly, sample.FreeBytes, sample.SampledAt = true, 30, now.Add(3*time.Second)
	record(sample)
	if latest := history("read_only", "low_space")[0]; latest.GrowthReason != "resize_queued" {
		t.Fatalf("escalation evidence = %+v", latest)
	}
	if err := DeliverAlerts(ctx, pool, send); err != nil || len(delivered) != 1 {
		t.Fatalf("delivered past a live lease: %v, %v", delivered, err)
	}

	exec(`UPDATE database_vms SET state = 'parked', state_changed_at = $2 WHERE database_id = $1`, id, now.Add(4*time.Second))
	sample.Low, sample.ReadOnly, sample.FreeBytes, sample.SampledAt = false, false, 500, now.Add(5*time.Second)
	record(sample)
	history("read_only", "low_space")
	exec(`UPDATE database_vms SET state = 'running', state_changed_at = $2, volume_bytes = 2000 WHERE database_id = $1`, id, now.Add(6*time.Second))
	sample.AllocatedBytes, sample.TotalBytes, sample.StateChangedAt, sample.SampledAt = 2000, 1980, now.Add(6*time.Second), now.Add(7*time.Second)
	record(sample)
	events := history("recovered", "read_only", "low_space")
	for _, event := range events {
		if event.IncidentID != events[0].IncidentID {
			t.Fatalf("expected one incident, got %+v", events)
		}
	}
	exec(`UPDATE native_postgres_storage_alerts SET available_at = statement_timestamp() WHERE id = $1::uuid`, claimed.ID)
	if err := DeliverAlerts(ctx, pool, send); err != nil {
		t.Fatal(err)
	}
	if err := DeliverAlerts(ctx, pool, send); err != nil || !slices.Equal(delivered, []string{"low_space", "read_only", "recovered"}) {
		t.Fatalf("delivery after recovery and restart = %v, %v", delivered, err)
	}
}
