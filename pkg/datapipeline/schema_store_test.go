package datapipeline

import (
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestSchemaRefreshIsAtomicReplayableAndFenced(t *testing.T) {
	service, store, p, plan := schemaPipelineFixture(t)
	ctx := t.Context()
	schema, err := os.ReadFile("catalog_schema.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.pool.Exec(ctx, string(schema)); err != nil {
		t.Fatal(err)
	}
	if _, err = store.pool.Exec(ctx, `CREATE TABLE notification_outbox(id bigserial PRIMARY KEY,channel text NOT NULL,payload text NOT NULL,available_at timestamptz NOT NULL,state text NOT NULL DEFAULT 'pending',attempts int NOT NULL DEFAULT 0,last_error text,created_at timestamptz NOT NULL DEFAULT clock_timestamp(),delivered_at timestamptz)`); err != nil {
		t.Fatal(err)
	}
	app, dep, snap := uuid.NewString(), uuid.NewString(), uuid.NewString()
	if _, err = store.pool.Exec(ctx, `INSERT INTO apps VALUES($1,$2,'orders-api','active')`, app, p.AccountID); err != nil {
		t.Fatal(err)
	}
	if _, err = store.pool.Exec(ctx, `INSERT INTO deployments VALUES($1,$2,'live','default')`, dep, app); err != nil {
		t.Fatal(err)
	}
	if _, err = store.pool.Exec(ctx, `INSERT INTO snapshots VALUES($1,$2,false)`, snap, dep); err != nil {
		t.Fatal(err)
	}
	if _, err = store.pool.Exec(ctx, `INSERT INTO managed_postgres_bindings VALUES($1,$2,$3,$4,'default','read_only','ready')`, uuid.NewString(), p.AccountID, p.DatabaseID, app); err != nil {
		t.Fatal(err)
	}
	if err = service.ApproveSchema(ctx, p.AccountID, p.ID, p.Revision, plan.ID, false); err != nil {
		t.Fatal(err)
	}
	p, err = store.Claim(ctx, uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	// An outbox failure must roll back the stamp, invalidation and replay receipt.
	if _, err = store.pool.Exec(ctx, `CREATE FUNCTION reject_schema_refresh() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'injected outbox failure'; END $$; CREATE TRIGGER reject_schema_refresh BEFORE INSERT ON notification_outbox FOR EACH ROW EXECUTE FUNCTION reject_schema_refresh()`); err != nil {
		t.Fatal(err)
	}
	if _, _, err = store.RefreshSchema(ctx, p, plan); !errors.Is(err, ErrUnavailable) {
		t.Fatal("outbox failure accepted", err)
	}
	var rolledBack bool
	if err = store.pool.QueryRow(ctx, `SELECT NOT (SELECT stale FROM snapshots WHERE id=$1) AND NOT EXISTS(SELECT 1 FROM app_runtime_config_changes) AND NOT EXISTS(SELECT 1 FROM data_pipeline_schema_refreshes)`, snap).Scan(&rolledBack); err != nil || !rolledBack {
		t.Fatal("partial refresh committed", err)
	}
	if _, err = store.pool.Exec(ctx, `DROP TRIGGER reject_schema_refresh ON notification_outbox`); err != nil {
		t.Fatal(err)
	}
	records, ready, err := store.RefreshSchema(ctx, p, plan)
	if err != nil || ready || len(records) != 1 || records[0].Status != "pending" {
		t.Fatal("refresh was not pending", records, ready, err)
	}
	var stamp time.Time
	var stale bool
	if err = store.pool.QueryRow(ctx, `SELECT changed_at,(SELECT stale FROM snapshots WHERE id=$2) FROM app_runtime_config_changes WHERE app_id=$1`, app, snap).Scan(&stamp, &stale); err != nil || !stale {
		t.Fatal("freshness boundary not installed", err)
	}
	replay, ready, err := NewPostgresStore(store.pool).RefreshSchema(ctx, p, plan)
	if err != nil || ready || len(replay) != 1 || replay[0].WakeID != records[0].WakeID {
		t.Fatal("lost response replay changed intent", err)
	}
	var stable bool
	if err = store.pool.QueryRow(ctx, `SELECT (SELECT count(*)=1 FROM notification_outbox) AND (SELECT changed_at=$2 FROM app_runtime_config_changes WHERE app_id=$1)`, app, stamp).Scan(&stable); err != nil || !stable {
		t.Fatal("replay moved freshness boundary", err)
	}
	foreign := p
	foreign.AccountID = uuid.NewString()
	if _, _, err = store.RefreshSchema(ctx, foreign, plan); err == nil {
		t.Fatal("foreign worker queued refresh")
	}
	staleLease := p
	staleLease.Revision++
	if _, _, err = store.RefreshSchema(ctx, staleLease, plan); err == nil {
		t.Fatal("stale worker queued refresh")
	}
	if _, err = store.pool.Exec(ctx, `UPDATE notification_outbox SET state='dead_letter'`); err != nil {
		t.Fatal(err)
	}
	if _, ready, err = store.RefreshSchema(ctx, p, plan); !errors.Is(err, ErrUnavailable) || ready {
		t.Fatal("failed refresh reopened reads", err)
	}
	// Explicit retry changes the durable attempt and wake ID only after fresh observation.
	plan.State = "refreshing"
	if err = store.Finish(ctx, p, Observation{State: "blocked", Reason: "schema_api_refresh_failed", SchemaChange: &plan, Initialized: true}); err != nil {
		t.Fatal(err)
	}
	p, err = store.Get(ctx, p.AccountID, p.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err = service.ApproveSchema(ctx, p.AccountID, p.ID, p.Revision, plan.ID, true); err != nil {
		t.Fatal(err)
	}
	p, err = store.Claim(ctx, uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	if p.SchemaChange.Retry != 1 {
		t.Fatal("retry attempt was not durable")
	}
	plan = *p.SchemaChange
	retry, ready, err := store.RefreshSchema(ctx, p, plan)
	if err != nil || ready || len(retry) != 1 || retry[0].WakeID == records[0].WakeID {
		t.Fatal("retry reused failed intent", err)
	}
	if _, err = store.pool.Exec(ctx, `UPDATE notification_outbox SET state='delivered',delivered_at=clock_timestamp() WHERE payload::jsonb->>'wake_id'=$1`, retry[0].WakeID); err != nil {
		t.Fatal(err)
	}
	if _, ready, err = store.RefreshSchema(ctx, p, plan); err != nil || !ready {
		t.Fatal("completed refresh not acknowledged", err)
	}
	// A cancellation must move the API freshness boundary again, even when the
	// original addition's refresh already completed at the same retry number.
	cancel := &TableCancellation{ID: strings.Repeat("c", 64), Table: "customers", State: "approved", Retention: "retain_private"}
	plan.Cancellation = cancel
	encoded, _ := json.Marshal(plan)
	if _, err = store.pool.Exec(ctx, `UPDATE data_pipelines SET schema_change=$2 WHERE id=$1`, p.ID, encoded); err != nil {
		t.Fatal(err)
	}
	if _, _, err = store.RefreshSchema(ctx, p, plan); err == nil {
		t.Fatal("onboarding refresh bypassed active cancellation")
	}
	cancelRefresh := SchemaChange{ID: cancel.ID}
	removed, ready, err := store.RefreshSchema(ctx, p, cancelRefresh)
	if err != nil || ready || len(removed) != 1 || removed[0].WakeID == retry[0].WakeID {
		t.Fatal("cancellation did not request a fresh API runtime", removed, ready, err)
	}
	replayed, _, err := store.RefreshSchema(ctx, p, cancelRefresh)
	if err != nil || len(replayed) != 1 || replayed[0].WakeID != removed[0].WakeID {
		t.Fatal("cancellation refresh replay changed intent", err)
	}
	if _, err = store.pool.Exec(ctx, `UPDATE notification_outbox SET state='delivered',delivered_at=clock_timestamp() WHERE payload::jsonb->>'wake_id'=$1`, removed[0].WakeID); err != nil {
		t.Fatal(err)
	}
	if _, ready, err = store.RefreshSchema(ctx, p, cancelRefresh); err != nil || !ready {
		t.Fatal("cancelled API exposure not acknowledged", err)
	}
}
