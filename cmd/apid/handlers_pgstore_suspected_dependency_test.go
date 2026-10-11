//go:build !no_pg

package main

// adr: 957 — regression alerts carry the suspected dependency end to end.

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func TestPGHandler_DebugRegressionSuspectedDependency(t *testing.T) {
	e := setupPGHandler(t, api.PlanPro)
	ctx := context.Background()
	app := seedPGApp(t, e, "pg-regression-suspect")
	deploymentID := uuid.New()

	var hookID string
	if err := e.pool.QueryRow(ctx, `
		insert into app_webhooks (app_id, account_id, target_url, secret_sealed, event_filter, enabled)
		values ($1, $2, 'https://suspect.example/hook', $3, $4, true)
		returning id
	`, app.ID, e.acct.ID, []byte("sealed-test-secret"), []string{string(state.AppWebhookEventDebugRegressionDetected)}).Scan(&hookID); err != nil {
		t.Fatalf("insert app webhook: %v", err)
	}

	factor := pgtype.Numeric{}
	if err := factor.Scan("2.33"); err != nil {
		t.Fatal(err)
	}
	suspect := &api.DebugSuspectedDependency{Type: "app_dependency", Kind: "postgresql", Name: "SELECT orders", P95BaseMS: 82, P95MS: 191, RegressionFactor: 2.33}
	params := sqlc.UpsertRegressionObservationParams{
		AppID:        pgtype.UUID{Bytes: uuid.MustParse(app.ID), Valid: true},
		DeploymentID: pgtype.UUID{Bytes: deploymentID, Valid: true},
		Route:        "GET /checkout", P95Ms: 240, P95BaseMs: 103, AffectedCount: 40,
		RegressionFactor: factor,
		Column8:          encodeSuspectedDependency(suspect),
	}
	if err := e.store.UpsertRegressionObservation(ctx, params); err != nil {
		t.Fatalf("upsert with suspect: %v", err)
	}

	// A later pass without a computable suspect keeps the stored one.
	params.Column8 = nil
	if err := e.store.UpsertRegressionObservation(ctx, params); err != nil {
		t.Fatalf("upsert without suspect: %v", err)
	}
	row, err := e.store.GetRegressionObservation(ctx, sqlc.GetRegressionObservationParams{
		AppID: params.AppID, DeploymentID: params.DeploymentID, Route: params.Route,
	})
	if err != nil {
		t.Fatalf("GetRegressionObservation: %v", err)
	}
	if got := debugRegressionObservationToItem(row).SuspectedDependency; got == nil || *got != *suspect {
		t.Fatalf("stored suspect = %+v, want %+v", got, suspect)
	}

	var payload []byte
	if err := e.pool.QueryRow(ctx, `
		select payload from app_webhook_deliveries
		 where webhook_id = $1 and event = 'debug.regression.detected'
	`, hookID).Scan(&payload); err != nil {
		t.Fatalf("detected delivery: %v", err)
	}
	var event struct {
		Suspected *api.DebugSuspectedDependency `json:"suspected_dependency"`
	}
	if err := json.Unmarshal(payload, &event); err != nil {
		t.Fatalf("decode payload %s: %v", payload, err)
	}
	if event.Suspected == nil || *event.Suspected != *suspect {
		t.Fatalf("webhook suspect = %+v, payload %s", event.Suspected, payload)
	}

	// The column is bounded by its CHECK constraint.
	params.Column8 = []byte(`{"name":"` + strings.Repeat("x", 1100) + `"}`)
	if err := e.store.UpsertRegressionObservation(ctx, params); err == nil {
		t.Fatal("oversized suspected_dependency accepted")
	}
}
