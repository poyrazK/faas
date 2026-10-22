//go:build !no_pg

package apidserver_test

// adr: 127 — error-rate regressions (amendment): a latency observation that
// gains the error signal reopens as a new incident, and error regressions
// sort first.

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func pgNumeric(t *testing.T, value string) pgtype.Numeric {
	t.Helper()
	var n pgtype.Numeric
	if err := n.Scan(value); err != nil {
		t.Fatalf("scan numeric %q: %v", value, err)
	}
	return n
}

func TestPGHandler_DebugRegressionReopensWhenErrorsAppear(t *testing.T) {
	e := apidserver.SetupPGHandler(t, api.PlanPro)
	ctx := context.Background()
	app := apidserver.SeedPGApp(t, e, "pg-regression-errors")
	deploymentID := uuid.New()

	var hookID string
	if err := (*e.Pool()).QueryRow(ctx, `
		insert into app_webhooks (app_id, account_id, target_url, secret_sealed, event_filter, enabled)
		values ($1, $2, 'https://errors.example/hook', $3, $4, true)
		returning id
	`, app.ID, (*e.Acct()).ID, []byte("sealed-test-secret"), []string{string(state.AppWebhookEventDebugRegressionDetected)}).Scan(&hookID); err != nil {
		t.Fatalf("insert app webhook: %v", err)
	}
	detected := func() int {
		t.Helper()
		var n int
		if err := (*e.Pool()).QueryRow(ctx, `
			select count(*) from app_webhook_deliveries
			 where webhook_id = $1 and event = 'debug.regression.detected'
		`, hookID).Scan(&n); err != nil {
			t.Fatalf("count deliveries: %v", err)
		}
		return n
	}
	appID := pgtype.UUID{Bytes: uuid.MustParse(app.ID), Valid: true}
	depID := pgtype.UUID{Bytes: deploymentID, Valid: true}
	latency := func(route string) sqlc.UpsertRegressionObservationParams {
		return sqlc.UpsertRegressionObservationParams{
			AppID: appID, DeploymentID: depID, Route: route,
			P95Ms: 300, P95BaseMs: 200, AffectedCount: 150, RegressionFactor: pgNumeric(t, "1.50"),
			Kind: "latency", ErrorRatePct: pgNumeric(t, "0.20"), ErrorRateBasePct: pgNumeric(t, "0.10"), ErrorCount: 1,
		}
	}
	withErrors := func(params sqlc.UpsertRegressionObservationParams) sqlc.UpsertRegressionObservationParams {
		params.Kind = "latency_and_errors"
		params.ErrorRatePct, params.ErrorRateBasePct, params.ErrorCount = pgNumeric(t, "12.50"), pgNumeric(t, "0.10"), 61
		return params
	}
	upsert := func(params sqlc.UpsertRegressionObservationParams) sqlc.DebugRegressionObservation {
		t.Helper()
		if err := (*e.Store()).UpsertRegressionObservation(ctx, params); err != nil {
			t.Fatalf("upsert %s: %v", params.Kind, err)
		}
		row, err := (*e.Store()).GetRegressionObservation(ctx, sqlc.GetRegressionObservationParams{AppID: appID, DeploymentID: depID, Route: params.Route})
		if err != nil {
			t.Fatalf("get observation: %v", err)
		}
		return row
	}

	// An acknowledged latency regression reopens when the route starts failing.
	first := upsert(latency("POST /pay"))
	if _, err := (*e.Store()).ApplyRegressionAction(ctx, sqlc.ApplyRegressionActionParams{
		AppID: appID, DeploymentID: depID, Route: "POST /pay", State: "acknowledged",
	}); err != nil {
		t.Fatalf("acknowledge: %v", err)
	}
	if got := upsert(latency("POST /pay")); got.State != "acknowledged" {
		t.Fatalf("a latency re-detection keeps the acknowledgement, state = %s", got.State)
	}
	reopened := upsert(withErrors(latency("POST /pay")))
	if reopened.State != "active" || reopened.AcknowledgedAt.Valid || !reopened.FirstDetectedAt.Time.After(first.FirstDetectedAt.Time) {
		t.Fatalf("reopened = state %s ack %v first %v (was %v)", reopened.State, reopened.AcknowledgedAt, reopened.FirstDetectedAt.Time, first.FirstDetectedAt.Time)
	}
	if n := detected(); n != 2 {
		t.Fatalf("detected deliveries = %d, want 2 (initial and reopen)", n)
	}
	// Further passes with the same signal are refreshes, not new incidents.
	if again := upsert(withErrors(latency("POST /pay"))); !again.FirstDetectedAt.Time.Equal(reopened.FirstDetectedAt.Time) {
		t.Fatalf("a refresh moved first_detected_at: %v -> %v", reopened.FirstDetectedAt.Time, again.FirstDetectedAt.Time)
	}
	if n := detected(); n != 2 {
		t.Fatalf("detected deliveries after refresh = %d, want 2", n)
	}

	// An active latency regression that gains errors also sends a new event.
	upsert(latency("GET /cart"))
	upsert(withErrors(latency("GET /cart")))
	if n := detected(); n != 4 {
		t.Fatalf("detected deliveries = %d, want 4 after the active route gained errors", n)
	}

	// An errors-only observation (factor 1.00) sorts ahead of latency ones.
	errorsOnly := latency("GET /search")
	errorsOnly.Kind, errorsOnly.RegressionFactor = "errors", pgNumeric(t, "1.00")
	upsert(latency("GET /slow"))
	upsert(errorsOnly)
	rows, err := (*e.Store()).ListActiveRegressionsByApp(ctx, sqlc.ListActiveRegressionsByAppParams{
		AppID: appID, Column2: pgtype.Interval{Microseconds: 3_600_000_000, Valid: true},
	})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(rows) == 0 || rows[len(rows)-1].Kind != "latency" {
		t.Fatalf("rows = %+v, want error regressions before latency ones", rows)
	}
	for i, row := range rows {
		if row.Kind == "latency" {
			for _, later := range rows[i:] {
				if later.Kind != "latency" {
					t.Fatalf("error regression %s sorted after latency %s", later.Route, row.Route)
				}
			}
			break
		}
	}
}
