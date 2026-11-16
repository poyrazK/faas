package state_test

import (
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func TestAnalyticsPendingMarkerCommitsWithCountsAndLossRemains(t *testing.T) {
	store, ctx := pgStore(t)
	account, app, dep := seedLiveDeploy(t, store, ctx, "pending-window", "pending-window")
	at := time.Now().UTC().Truncate(time.Minute).Add(-10 * time.Minute)
	token, appID := uuid.New(), uuid.MustParse(app)
	if err := store.RecordRequestAnalyticsWindow(ctx, token, appID, at, "pending"); err != nil {
		t.Fatal(err)
	}
	read := func() []string {
		t.Helper()
		gaps, err := store.RequestAnalyticsCompleteness(ctx, app, at, at.Add(time.Minute))
		if err != nil {
			t.Fatal(err)
		}
		return gaps
	}
	if gaps := read(); !slices.Contains(gaps, "telemetry_ingestion_pending") {
		t.Fatalf("uncommitted counts look complete: %v", gaps)
	}
	row := state.RequestAnalyticsRollup{AppID: appID, AccountID: uuid.MustParse(account), DeploymentID: uuid.MustParse(dep), BucketStart: at, DimensionKind: "invalid", Dimension: "GET /", Method: "GET", Status: 200, Requests: 1}
	row.LatencyHist[0] = 1
	coverage := []state.RequestAnalyticsCoverage{{AppID: appID, PendingID: token}}
	if err := store.AddRequestAnalyticsRollups(ctx, []state.RequestAnalyticsRollup{row}, coverage); err == nil {
		t.Fatal("invalid flush accepted")
	}
	if gaps := read(); !slices.Contains(gaps, "telemetry_ingestion_pending") {
		t.Fatalf("failed transaction cleared pending: %v", gaps)
	}
	row.DimensionKind = "route"
	if err := store.AddRequestAnalyticsRollups(ctx, []state.RequestAnalyticsRollup{row}, coverage); err != nil {
		t.Fatal(err)
	}
	if gaps := read(); slices.Contains(gaps, "telemetry_ingestion_pending") {
		t.Fatalf("committed flush still pending: %v", gaps)
	}
	lost := uuid.New()
	if err := store.RecordRequestAnalyticsWindow(ctx, lost, appID, at, "lost"); err != nil {
		t.Fatal(err)
	}
	if err := store.AddRequestAnalyticsRollups(ctx, nil, []state.RequestAnalyticsCoverage{{AppID: appID, PendingID: lost}}); err != nil {
		t.Fatal(err)
	}
	if gaps := read(); !slices.Contains(gaps, "telemetry_window_incomplete") {
		t.Fatalf("loss evidence incorrectly cleared: %v", gaps)
	}
}

func TestAnalyticsReadsNormalizeLegacyHTTPMethods(t *testing.T) {
	f := newTelemetryFixture(t)
	at := time.Now().UTC().Truncate(time.Minute).Add(-10 * time.Minute)
	rows := make([]state.RequestAnalyticsRollup, 0, 2)
	for i, method := range []string{"X000", "X001"} {
		row := state.RequestAnalyticsRollup{AppID: uuid.UUID(f.app.Bytes), AccountID: uuid.UUID(f.account.Bytes), DeploymentID: uuid.UUID(f.dep.Bytes), BucketStart: at, DimensionKind: "route", Dimension: "__route_other__", Method: method, Status: 200, Requests: int64(i + 2)}
		row.LatencyHist[0] = row.Requests
		rows = append(rows, row)
	}
	if err := f.s.AddRequestAnalyticsRollups(f.ctx, rows, nil); err != nil {
		t.Fatal(err)
	}
	groups, err := f.s.RequestAnalyticsRollupGroups(f.ctx, sqlc.RequestAnalyticsRollupGroupsParams{AppID: f.app, AccountID: f.account, WindowFrom: ts(at), WindowUntil: ts(at.Add(time.Minute)), HoursFrom: ts(at.Add(time.Minute)), HoursUntil: ts(at.Add(time.Minute)), GroupBy: "route", GroupLimit: 50})
	if err != nil || len(groups) != 1 || groups[0].Method != "OTHER" || groups[0].Requests != 5 {
		t.Fatalf("legacy rollup groups: %+v %v", groups, err)
	}
}
