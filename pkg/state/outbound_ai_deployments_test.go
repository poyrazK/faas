package state_test

// adr: 20261022153155 — an app's AI calls are listed by deployment, latest
// call first, within the window and up to the limit, in Postgres and in
// memory; release gates read one deployment's window, and Postgres stores
// an ai gate's metric and increase and refuses malformed ones.

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

type deploymentStatsStore interface {
	ListOutboundAIDeploymentStats(context.Context, string, time.Time, int) ([]state.OutboundAIDeploymentStats, error)
}

// checkDeploymentStats checks three deployments: old (outside the window),
// earlier and latest.
func checkDeploymentStats(t *testing.T, store deploymentStatsStore, appID, earlier, latest string, now time.Time) {
	t.Helper()
	ctx := context.Background()
	stats, err := store.ListOutboundAIDeploymentStats(ctx, appID, now.Add(-24*time.Hour), 10)
	if err != nil || len(stats) != 2 {
		t.Fatalf("stats = %+v, %v", stats, err)
	}
	if stats[0].DeploymentID != latest || stats[0].Calls != 5 || stats[0].ErrorCalls != 1 || stats[0].TTFBMSTotal != 1_000 ||
		stats[1].DeploymentID != earlier || stats[1].Calls != 7 || stats[1].CostMillicents != 700 {
		t.Fatalf("stats = %+v", stats)
	}
	if limited, err := store.ListOutboundAIDeploymentStats(ctx, appID, now.Add(-24*time.Hour), 1); err != nil || len(limited) != 1 || limited[0].DeploymentID != latest {
		t.Fatalf("limited = %+v, %v", limited, err)
	}
	if none, err := store.ListOutboundAIDeploymentStats(ctx, uuid.NewString(), now.Add(-24*time.Hour), 10); err != nil || len(none) != 0 {
		t.Fatalf("another app = %+v, %v", none, err)
	}
}

func TestMemOutboundAIDeploymentStats(t *testing.T) {
	store := state.NewMemStore()
	now := time.Now().UTC().Truncate(time.Minute)
	appID, earlier, latest := uuid.NewString(), uuid.NewString(), uuid.NewString()
	store.SeedOutboundAIDeploymentStats(appID,
		state.OutboundAIDeploymentStats{DeploymentID: uuid.NewString(), FirstMinute: now.Add(-72 * time.Hour), LastMinute: now.Add(-48 * time.Hour), Calls: 9},
		state.OutboundAIDeploymentStats{DeploymentID: earlier, FirstMinute: now.Add(-3 * time.Hour), LastMinute: now.Add(-2 * time.Hour), Calls: 7, CostMillicents: 700},
		state.OutboundAIDeploymentStats{DeploymentID: latest, FirstMinute: now.Add(-time.Hour), LastMinute: now, Calls: 5, ErrorCalls: 1, TTFBCalls: 4, TTFBMSTotal: 1_000})
	checkDeploymentStats(t, store, appID, earlier, latest, now)
}

func TestPgOutboundAIDeploymentStats(t *testing.T) {
	s, ctx, pool := pgWithPool(t)
	now := time.Now().UTC().Truncate(time.Minute)
	appID, old, earlier, latest := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
	for _, row := range []struct {
		deployment                        string
		minute                            time.Time
		calls, errors, cost, ttfb, ttfbMS int64
	}{
		{old, now.Add(-48 * time.Hour), 9, 0, 900, 0, 0},
		{earlier, now.Add(-3 * time.Hour), 3, 0, 300, 0, 0},
		{earlier, now.Add(-2 * time.Hour), 4, 0, 400, 0, 0},
		{latest, now.Add(-time.Hour), 2, 1, 0, 1, 400},
		{latest, now, 3, 0, 0, 3, 600},
	} {
		if _, err := pool.Exec(ctx, `
			INSERT INTO outbound_ai_deployment_minutes (deployment_id, minute, app_id, calls, error_calls, cost_millicents, ttfb_calls, ttfb_ms_total)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`, row.deployment, row.minute, appID, row.calls, row.errors, row.cost, row.ttfb, row.ttfbMS); err != nil {
			t.Fatal(err)
		}
	}
	checkDeploymentStats(t, s, appID, earlier, latest, now)
	window, err := s.ReleaseGateDeploymentAI(ctx, appID, earlier, now.Add(-2*time.Hour-30*time.Second), now)
	if err != nil || window != (state.ReleaseGateAIWindow{Calls: 4, CostMillicents: 400}) {
		t.Fatalf("window = %+v, %v", window, err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO outbound_ai_deployment_minutes (deployment_id, minute, app_id, calls, error_calls)
		VALUES ($1, $2, $3, 1, 2)`, uuid.NewString(), now, appID); err == nil {
		t.Fatal("more errors than calls were accepted")
	}
}

func TestPgAIReleaseGateRoundTrip(t *testing.T) {
	s, ctx, _ := pgWithPool(t)
	acct, err := s.CreateAccount(ctx, "gate-"+uuid.NewString()+"@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := s.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "gate-" + uuid.NewString()[:8], RAMMB: 128})
	if err != nil {
		t.Fatal(err)
	}
	gate := state.ReleaseGate{AccountID: acct.ID, AppID: app.ID, Name: "ai-cost", Kind: state.ReleaseGateAI,
		MetricName: "cost_per_call", Threshold: 30, MinSamples: 20, CreatedBy: "test"}
	created, err := s.CreateReleaseGate(ctx, gate, api.ReleaseGatesPerAppMax, time.Now())
	if err != nil || created.Kind != state.ReleaseGateAI || created.MetricName != "cost_per_call" || created.Threshold != 30 || created.Comparison != "" {
		t.Fatalf("created = %+v, %v", created, err)
	}
	if listed, err := s.ListReleaseGates(ctx, app.ID); err != nil || len(listed) != 1 || listed[0].Threshold != 30 {
		t.Fatalf("listed = %+v, %v", listed, err)
	}
	for _, bad := range []state.ReleaseGate{
		{Name: "bad-metric", MetricName: "tokens", Threshold: 30},
		{Name: "bad-increase", MetricName: "ttfb_ms", Threshold: 1001},
		{Name: "bad-comparison", MetricName: "ttfb_ms", Comparison: "above", Threshold: 30},
	} {
		bad.AccountID, bad.AppID, bad.Kind, bad.MinSamples, bad.CreatedBy = acct.ID, app.ID, state.ReleaseGateAI, 20, "test"
		if _, err := s.CreateReleaseGate(ctx, bad, api.ReleaseGatesPerAppMax, time.Now()); err == nil {
			t.Fatalf("%s was stored", bad.Name)
		}
	}
}
