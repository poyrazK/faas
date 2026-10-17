package migrations_test

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

func TestMCPReadinessIndependentOutboxMigration(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	ctx := t.Context()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	// Remove later acknowledgement dependencies before dropping the incident
	// tables, in the same order as a real migration rollback.
	for _, name := range []string{"20261017181315189_mcp_readiness_pagerduty_release.sql", "20261017181315175_mcp_readiness_pagerduty_independent_bindings.sql", "20261017181315159_mcp_readiness_pagerduty_fallback.sql", "20261017181315142_mcp_readiness_pagerduty_acknowledgements.sql"} {
		raw, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = tx.Exec(ctx, strings.Split(string(raw), "-- +goose Down")[1]); err != nil {
			t.Fatal(err)
		}
	}
	attention, err := os.ReadFile("20261017181315063_mcp_readiness_attention.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, strings.Split(string(attention), "-- +goose Down")[1]); err != nil {
		t.Fatal(err)
	}
	// Roll back the dependent recovery ledger before exercising the older table
	// migration. Real rollbacks also remove newer migrations first.
	recovery, err := os.ReadFile("20261017181315046_mcp_readiness_notification_recovery.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, strings.Split(string(recovery), "-- +goose Down")[1]); err != nil {
		t.Fatal(err)
	}

	raw, err := os.ReadFile("20261017181315018_mcp_readiness_independent_outbox.sql")
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.Split(string(raw), "-- +goose Down")
	if _, err = tx.Exec(ctx, parts[1]); err != nil {
		t.Fatal(err)
	}
	var account, app, rule, other string
	if err = tx.QueryRow(ctx, `INSERT INTO accounts(email,plan) VALUES('independent-migration@example.com','pro') RETURNING id::text`).Scan(&account); err != nil {
		t.Fatal(err)
	}
	if err = tx.QueryRow(ctx, `INSERT INTO apps(account_id,slug,ram_mb) VALUES($1,'independent-migration',256) RETURNING id::text`, account).Scan(&app); err != nil {
		t.Fatal(err)
	}
	for _, slot := range []*string{&rule, &other} {
		if err = tx.QueryRow(ctx, `INSERT INTO alert_rules(account_id,app_id,name,enabled,metric,comparison,threshold,window_spec,webhook_url,webhook_secret_sealed) VALUES($1,$2,$3,true,'mcp_readiness_unhealthy','gt',0,'5m','https://example.com/hook',decode('01','hex')) RETURNING id::text`, account, app, uuid.NewString()).Scan(slot); err != nil {
			t.Fatal(err)
		}
	}
	at := time.Now().UTC().Truncate(time.Microsecond)
	incident, event := uuid.NewString(), uuid.NewString()
	var latest string
	record := func(rule, incident, event, kind, status string, when time.Time) {
		t.Helper()
		body, _ := json.Marshal(map[string]any{"metric": "mcp_readiness_unhealthy", "incident_id": incident, "event_id": event, "event": kind, "occurred_at": at, "incident_started_at": at, "mcp_readiness_coverage_fingerprint": strings.Repeat("a", 64), "mcp_readiness_scopes": []string{"staging"}})
		if err = tx.QueryRow(ctx, `INSERT INTO alert_deliveries(rule_id,account_id,app_id,idempotency_key,payload,status,observed_value,fired_at) VALUES($1,$2,$3,$4,$5,$6,1,$7) RETURNING id::text`, rule, account, app, uuid.NewString(), body, status, when).Scan(&latest); err != nil {
			t.Fatal(err)
		}
	}
	record(rule, incident, event, "alert.fired", "failed", at)
	record(rule, incident, event, "alert.fired", "delivered", at.Add(time.Second))
	deliveredID := latest
	record(rule, incident, uuid.NewString(), "alert.resolved", "failed", at.Add(2*time.Second))
	record(other, uuid.NewString(), uuid.NewString(), "alert.fired", "pending", at.Add(3*time.Second))
	for range 2 {
		if _, err = tx.Exec(ctx, parts[0]); err != nil {
			t.Fatal(err)
		}
	}
	var count, attempts int
	var status, delivery string
	if err = tx.QueryRow(ctx, `SELECT count(*) FROM mcp_readiness_incident_events`).Scan(&count); err != nil || count != 3 {
		t.Fatal("duplicate or missing logical events", count, err)
	}
	if err = tx.QueryRow(ctx, `SELECT attempt_count,delivery_status,last_delivery_id::text FROM mcp_readiness_incident_events WHERE id=$1`, event).Scan(&attempts, &status, &delivery); err != nil || attempts != 2 || status != "delivered" || delivery != deliveredID {
		t.Fatal("latest attempt not preserved", attempts, status, delivery, err)
	}
	if err = tx.QueryRow(ctx, `SELECT count(*) FROM mcp_readiness_incidents WHERE resolved_at IS NULL`).Scan(&count); err != nil || count != 1 {
		t.Fatal("pending recovery coupled to incident state", count, err)
	}
	future := at.Add(24 * time.Hour)
	if _, err = tx.Exec(ctx, `UPDATE mcp_readiness_incident_events SET available_at=$2 WHERE id=$1`, event, future); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, parts[0]); err != nil {
		t.Fatal(err)
	}
	var got time.Time
	if err = tx.QueryRow(ctx, `SELECT available_at FROM mcp_readiness_incident_events WHERE id=$1`, event).Scan(&got); err != nil || !got.Equal(future) {
		t.Fatal("replay reset queue state", got, err)
	}
	for _, sql := range []string{parts[1], parts[0], parts[0]} {
		if _, err = tx.Exec(ctx, sql); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = tx.Exec(ctx, `UPDATE mcp_readiness_incident_events SET imported=false WHERE id=$1`, event); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, `SAVEPOINT rollback_guard`); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, parts[1]); err == nil {
		t.Fatal("rollback discarded independent history")
	}
	if _, err = tx.Exec(ctx, `ROLLBACK TO SAVEPOINT rollback_guard`); err != nil {
		t.Fatal(err)
	}
}
