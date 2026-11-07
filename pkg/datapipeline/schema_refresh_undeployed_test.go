package datapipeline

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestSchemaRefreshSkipsUnstartedRuntimesAndPreservesFreshness(t *testing.T) {
	service, store, p, plan := schemaPipelineFixture(t)
	ctx := t.Context()
	schema, err := os.ReadFile("catalog_schema.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.pool.Exec(ctx, string(schema)); err != nil {
		t.Fatal(err)
	}
	app := uuid.NewString()
	if _, err = store.pool.Exec(ctx, `INSERT INTO apps VALUES($1,$2,'not-yet-deployed-api','active')`, app, p.AccountID); err != nil {
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
	checkReady := func() {
		t.Helper()
		records, ready, err := store.RefreshSchema(ctx, p, plan)
		if err != nil || !ready || len(records) != 1 || records[0].AppID != app || records[0].Status != "not_required" || records[0].WakeID != "" {
			t.Fatal("undeployed app blocked schema refresh", records, ready, err)
		}
	}
	checkReady()
	var stamp time.Time
	if err = store.pool.QueryRow(ctx, `SELECT changed_at FROM app_runtime_config_changes WHERE app_id=$1`, app).Scan(&stamp); err != nil {
		t.Fatal("missing first-boot freshness boundary", err)
	}
	var queued int
	if err = store.pool.QueryRow(ctx, `SELECT count(*) FROM notification_outbox`).Scan(&queued); err != nil || queued != 0 {
		t.Fatal("unstartable runtime restart queued", queued, err)
	}
	deployment, snapshot := uuid.NewString(), uuid.NewString()
	if _, err = store.pool.Exec(ctx, `INSERT INTO deployments VALUES($1,$2,'snapshotting','default')`, deployment, app); err != nil {
		t.Fatal(err)
	}
	if _, err = store.pool.Exec(ctx, `INSERT INTO snapshots VALUES($1,$2,false)`, snapshot, deployment); err != nil {
		t.Fatal(err)
	}
	// A new schema attempt invalidates build snapshots, but cannot wait for an
	// unstarted deployment to restart. Failed deployment history is the same.
	plan.Retry++
	checkReady()
	var stale bool
	if err = store.pool.QueryRow(ctx, `SELECT stale FROM snapshots WHERE id=$1`, snapshot).Scan(&stale); err != nil || !stale {
		t.Fatal("pending build retained a stale schema snapshot", stale, err)
	}
	if _, err = store.pool.Exec(ctx, `UPDATE deployments SET status='failed' WHERE id=$1`, deployment); err != nil {
		t.Fatal(err)
	}
	checkReady()
	if err = store.pool.QueryRow(ctx, `SELECT changed_at FROM app_runtime_config_changes WHERE app_id=$1`, app).Scan(&stamp); err != nil {
		t.Fatal(err)
	}
	// A restart after serving begins uses the original attempt's stamp and one
	// durable wake. It neither moves the boundary nor duplicates the handoff.
	if _, err = store.pool.Exec(ctx, `UPDATE deployments SET status='live' WHERE id=$1`, deployment); err != nil {
		t.Fatal(err)
	}
	records, ready, err := store.RefreshSchema(ctx, p, plan)
	if err != nil || ready || len(records) != 1 || records[0].WakeID == "" || records[0].Status != "pending" {
		t.Fatal("new serving deployment missed restart", records, ready, err)
	}
	wake := records[0].WakeID
	for range 2 {
		replayed, ready, err := store.RefreshSchema(ctx, p, plan)
		if err != nil || ready || replayed[0].WakeID != wake {
			t.Fatal("restart replay changed intent", replayed, ready, err)
		}
	}
	var stable bool
	if err = store.pool.QueryRow(ctx, `SELECT changed_at=$2 AND (SELECT count(*)=1 FROM notification_outbox) FROM app_runtime_config_changes WHERE app_id=$1`, app, stamp).Scan(&stable); err != nil || !stable {
		t.Fatal("serving transition moved stamp or duplicated restart", stable, err)
	}
	if _, err = store.pool.Exec(ctx, `UPDATE notification_outbox SET state='delivered',delivered_at=clock_timestamp()`); err != nil {
		t.Fatal(err)
	}
	if _, ready, err = store.RefreshSchema(ctx, p, plan); err != nil || !ready {
		t.Fatal("serving restart never acknowledged", ready, err)
	}
	// Repair a legacy dead-letter for an app that currently has no runtime.
	if _, err = store.pool.Exec(ctx, `UPDATE deployments SET status='failed';UPDATE notification_outbox SET state='dead_letter',delivered_at=NULL`); err != nil {
		t.Fatal(err)
	}
	checkReady()
	plan.State = "complete"
	if err = store.Finish(ctx, p, Observation{State: "live", SchemaChange: &plan, Initialized: true}); err != nil {
		t.Fatal(err)
	}
	deployed := false
	service.PrepareDataAPI = func(context.Context, Setup) (string, bool, error) { return "", true, nil }
	service.DeployDataAPI = func(context.Context, Setup) (string, string, error) {
		deployed = true
		return uuid.NewString(), uuid.NewString(), nil
	}
	service.DataAPIState = func(context.Context, Setup) (string, string, error) { return "building", "", nil }
	job := Setup{AccountID: p.AccountID, PipelineID: p.ID, Phase: "deploying_api", DataAPIPlan: DataAPIPlan{AppID: app}}
	if err = service.advanceDataAPI(ctx, &job); err != nil || !deployed || job.Phase != "waiting_api" {
		t.Fatal("schema completion stranded initial API setup", job, err)
	}
}
