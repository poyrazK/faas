package main

import (
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func TestFeatureFlagOutcomeQueryClampsWindowAndScopesCustomer(t *testing.T) {
	customerID := uuid.NewString()
	r := httptest.NewRequest("GET", "/?since=72h&customer_id="+customerID, nil)
	r.SetPathValue("key", "export")
	r.SetPathValue("environment", "production")
	scope := state.FeatureFlagScope{AccountID: uuid.NewString()}
	retention := 12 * time.Hour

	params, start, end, err := featureFlagOutcomeQuery(r, scope, retention)
	if err != nil {
		t.Fatal(err)
	}
	if got := end.Sub(start); got != retention {
		t.Fatalf("window=%s, want retention clamp %s", got, retention)
	}
	if params.EnvironmentSlug != "production" || params.FlagKey != "export" || params.CustomerID != customerID {
		t.Fatalf("query params=%+v", params)
	}

	r = httptest.NewRequest("GET", "/?since=1h&rule_id=selected&config_version=9", nil)
	r.SetPathValue("key", "export")
	r.SetPathValue("environment", "production")
	params, _, _, err = featureFlagOutcomeQuery(r, scope, retention)
	if err != nil || params.RuleID != "selected" || params.ConfigVersion != 9 {
		t.Fatalf("rule-filter query params=%+v err=%v", params, err)
	}
}

func TestFeatureFlagOutcomeQueryRejectsInvalidFilters(t *testing.T) {
	scope := state.FeatureFlagScope{AccountID: uuid.NewString()}
	for _, tc := range []struct {
		name string
		path string
		key  string
	}{
		{name: "duration", path: "/?since=0", key: "export"},
		{name: "customer", path: "/?customer_id=not-a-uuid", key: "export"},
		{name: "key", path: "/?since=1h", key: "Bad Key"},
		{name: "rule", path: "/?rule_id=Bad.Rule", key: "export"},
		{name: "configuration version", path: "/?config_version=0", key: "export"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest("GET", tc.path, nil)
			r.SetPathValue("key", tc.key)
			r.SetPathValue("environment", "production")
			if _, _, _, err := featureFlagOutcomeQuery(r, scope, 24*time.Hour); err == nil {
				t.Fatal("expected invalid filter to fail")
			}
		})
	}
}

func TestMakeFeatureFlagOutcomesResponseComputesRates(t *testing.T) {
	start := time.Date(2026, time.September, 30, 0, 0, 0, 0, time.UTC)
	end := start.Add(time.Hour)
	got := makeFeatureFlagOutcomesResponse([]sqlc.FeatureFlagRequestOutcomesRow{{
		DecisionType: "variant", DecisionValue: "new", RequestCount: 10, UsedCount: 7, ErrorCount: 2,
		P50LatencyMs: 40, P95LatencyMs: 90,
	}}, start, end)
	if len(got.Outcomes) != 1 {
		t.Fatalf("outcomes=%+v", got.Outcomes)
	}
	outcome := got.Outcomes[0]
	if outcome.Type != "variant" || outcome.Value != "new" || outcome.RequestCount != 10 || outcome.UsedCount != 7 || outcome.HTTP5xxCount != 2 || outcome.HTTP5xxRate != 0.2 || outcome.P50LatencyMS != 40 || outcome.P95LatencyMS != 90 || !outcome.LatencyQuantized {
		t.Fatalf("outcome=%+v", outcome)
	}
	if !got.WindowStart.Equal(start) || !got.WindowEnd.Equal(end) {
		t.Fatalf("window=[%s,%s]", got.WindowStart, got.WindowEnd)
	}
	if got.Truncated {
		t.Fatal("response with one cohort is unexpectedly truncated")
	}
}

func TestMakeFeatureFlagOutcomesResponseBoundsHistoricalGroups(t *testing.T) {
	rows := make([]sqlc.FeatureFlagRequestOutcomesRow, 101)
	for i := range rows {
		rows[i] = sqlc.FeatureFlagRequestOutcomesRow{DecisionType: "variant", DecisionValue: "old", RequestCount: int64(101 - i)}
	}
	got := makeFeatureFlagOutcomesResponse(rows, time.Time{}, time.Time{})
	if len(got.Outcomes) != 100 || !got.Truncated || got.Outcomes[0].Value != "old" {
		t.Fatalf("groups=%d truncated=%v", len(got.Outcomes), got.Truncated)
	}
}
