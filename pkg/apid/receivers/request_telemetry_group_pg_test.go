//go:build !no_pg

// adr: 127 — request telemetry group commit.

package receivers

import (
	"testing"
	"time"

	apidpb "github.com/onebox-faas/faas/api/proto/onebox/faas/apid/v1"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state"
)

// A gateway batch lands in one transaction on Postgres; a replayed event in
// the same batch, or of an earlier one, adds neither a second telemetry row
// nor a second log line.
func TestRequestTelemetryGroupCommitOnPostgres(t *testing.T) {
	ctx := t.Context()
	pool := pgtest.OpenMigrated(t)
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	store := state.NewPgStore(pool)
	account, err := store.CreateAccount(ctx, "telemetry-group@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, Slug: "telemetry-group"})
	if err != nil {
		t.Fatal(err)
	}
	deployment := "00000000-0000-0000-0000-0000000000bb"
	records := make([]*apidpb.IncrementRequestTelemetryRequest, 0, 4)
	for range 3 {
		records = append(records, rollupTelemetryRequest(account.ID, app.ID, deployment, time.Now()))
	}
	replay := *records[1]
	records = append(records, &replay) // the same event twice in one batch
	receiver := NewRequestTelemetryReceiver(store, nil, nil, true)
	stream := newTelemetryStream(ctx, records, false)
	if err := receiver.IncrementRequestTelemetry(stream); err != nil {
		t.Fatal(err)
	}
	for i, out := range stream.outs {
		if out.GetOutcome() != RtOutcomeInserted {
			t.Fatalf("record %d outcome = %q, want inserted", i, out.GetOutcome())
		}
	}
	// The batch again: every event is a replay.
	if err := receiver.IncrementRequestTelemetry(newTelemetryStream(ctx, records, false)); err != nil {
		t.Fatal(err)
	}
	var telemetry, logs int
	if err := pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM request_telemetry WHERE app_id = $1), (SELECT count(*) FROM log_events WHERE app_id = $1)`, app.ID).Scan(&telemetry, &logs); err != nil {
		t.Fatal(err)
	}
	if telemetry != 3 || logs != 3 {
		t.Fatalf("telemetry rows %d, log lines %d; want 3 each", telemetry, logs)
	}
}
