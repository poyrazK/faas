//go:build !no_pg

package state_test

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func TestPg_FeatureFlagRequestOutcomesWeightsCollapsedRows(t *testing.T) {
	f := newTelemetryFixture(t)
	customerA, customerB := mustPgUUID(t, uuid.NewString()), mustPgUUID(t, uuid.NewString())
	now := time.Now().UTC()
	insert := func(status, latency, count int32, customer pgtype.UUID, evidence string, offset time.Duration) {
		t.Helper()
		if err := f.s.InsertRequestTelemetry(f.ctx, sqlc.InsertRequestTelemetryParams{
			AccountID: f.account, AppID: f.app, DeploymentID: f.dep,
			PlatformTenantID: customer, Route: "/exports", Method: "GET",
			Status: status, LatencyMs: latency, Count: count,
			ReceivedAt: ts(now.Add(offset)), Country: "__unknown__",
			UaFamily: "__unknown__", ReferrerHost: "__none__",
			GuestRuntime: "__unknown__", GuestOutcome: "missing",
			FlagEvidenceJson: evidence,
		}); err != nil {
			t.Fatalf("insert flag evidence: %v", err)
		}
	}
	insert(200, 10, 2, customerA, `[{"flag":"new-export","value":true,"used":true}]`, 0)
	insert(503, 20, 1, customerA, `[{"flag":"new-export","value":true,"used":false}]`, time.Second)
	insert(500, 30, 2, customerB, `[{"flag":"new-export","value":false,"used":true}]`, 2*time.Second)
	insert(201, 40, 4, customerA, `[{"flag":"pipeline","type":"variant","value":"new","used":true}]`, 3*time.Second)
	insert(500, 1, 9, customerA, `[{"flag":"other-flag","value":true,"used":true}]`, 4*time.Second)

	query := sqlc.FeatureFlagRequestOutcomesParams{
		EnvironmentSlug: "production", AccountID: f.account, AppIds: []pgtype.UUID{f.app},
		ReceivedFrom: ts(now.Add(-time.Hour)), ReceivedUntil: ts(now.Add(time.Hour)),
		FlagKey: "new-export",
	}
	rows, err := f.s.FeatureFlagRequestOutcomes(f.ctx, query)
	if err != nil {
		t.Fatalf("FeatureFlagRequestOutcomes: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("outcomes=%+v, want true and false cohorts", rows)
	}
	byValue := map[string]sqlc.FeatureFlagRequestOutcomesRow{}
	for _, row := range rows {
		byValue[row.DecisionValue] = row
	}
	trueOutcome := byValue["true"]
	if trueOutcome.DecisionType != "boolean" || trueOutcome.RequestCount != 3 || trueOutcome.UsedCount != 2 || trueOutcome.ErrorCount != 1 || trueOutcome.P50LatencyMs != 10 || trueOutcome.P95LatencyMs != 20 {
		t.Errorf("true outcome=%+v", trueOutcome)
	}
	falseOutcome := byValue["false"]
	if falseOutcome.DecisionType != "boolean" || falseOutcome.RequestCount != 2 || falseOutcome.UsedCount != 2 || falseOutcome.ErrorCount != 2 || falseOutcome.P50LatencyMs != 30 || falseOutcome.P95LatencyMs != 30 {
		t.Errorf("false outcome=%+v", falseOutcome)
	}

	query.FlagKey = "pipeline"
	rows, err = f.s.FeatureFlagRequestOutcomes(f.ctx, query)
	if err != nil || len(rows) != 1 || rows[0].DecisionType != "variant" || rows[0].DecisionValue != "new" || rows[0].RequestCount != 4 || rows[0].UsedCount != 4 || rows[0].P50LatencyMs != 40 || rows[0].P95LatencyMs != 40 {
		t.Fatalf("variant outcomes=%+v err=%v", rows, err)
	}

	query.FlagKey, query.CustomerID = "new-export", uuidFromPgTest(customerA)
	rows, err = f.s.FeatureFlagRequestOutcomes(f.ctx, query)
	if err != nil || len(rows) != 1 || rows[0].DecisionValue != "true" || rows[0].RequestCount != 3 {
		t.Fatalf("customer-scoped outcomes=%+v err=%v", rows, err)
	}
}

func uuidFromPgTest(value pgtype.UUID) string {
	return uuid.UUID(value.Bytes).String()
}
