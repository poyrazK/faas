package apidserver_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/events"
	"github.com/onebox-faas/faas/pkg/state"
)

// The wake-latency report reads the window's wake events, names nodes and
// apps, filters by node name and app, and rejects bad windows
// (ADR-20261121200409).
func TestObsWakeLatencyReport(t *testing.T) {
	e := apidserver.NewObsEnv(t, api.ScopesAdminOnly, "ops@faas.dev", "ops@faas.dev")
	ctx := context.Background()
	store := *e.Store()
	node, err := store.UpsertComputeNodeFromOperator(ctx, state.ComputeNode{Name: "wake-a", TargetURL: "unix:///run/faas/wake-a.sock",
		VPCPUs: 8, MemMB: 16384, MaxConcurrency: 32, AdmissionCeilingMB: 12000})
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: (*e.Acct()).ID, Slug: "wake-shop", Type: state.AppTypeApp})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	boot := func(wake string, ms int, at time.Time) {
		t.Helper()
		start := at.Add(-time.Duration(ms) * time.Millisecond)
		raw, _ := json.Marshal(map[string]any{"wake_id": wake, "instance_id": "i-" + wake, "app_id": app.ID, "node_id": node.ID,
			"method": "restore", "started_at": start.Format(time.RFC3339Nano), "completed_at": at.Format(time.RFC3339Nano)})
		if err := store.AppendEventAt(ctx, "schedd", events.WakeBootCompleted, nil, raw, at); err != nil {
			t.Fatal(err)
		}
	}
	boot("recent-fast", 90, now.Add(-10*time.Minute))
	boot("recent-slow", 700, now.Add(-5*time.Minute))
	boot("old", 5000, now.Add(-3*time.Hour))

	get := func(query string, want int) api.ObsWakeLatencyReport {
		t.Helper()
		rec := e.DoOperator(t, http.MethodGet, "/v1/admin/obs/wake-latency"+query, nil, nil)
		if rec.Code != want {
			t.Fatalf("%s: status %d body=%s", query, rec.Code, rec.Body.String())
		}
		var out api.ObsWakeLatencyReport
		if want == http.StatusOK {
			if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
				t.Fatal(err)
			}
		}
		return out
	}

	r := get("", http.StatusOK)
	if r.Window != "1h" || r.TargetMS != 350 || r.Totals.Restores != 2 || r.Totals.RestoresOverTarget != 1 {
		t.Fatalf("default report = %+v", r.Totals)
	}
	if len(r.Slowest) != 2 || r.Slowest[0].WakeID != "recent-slow" || r.Slowest[0].AppSlug != "wake-shop" || r.Slowest[0].NodeName != "wake-a" {
		t.Fatalf("slowest = %+v", r.Slowest)
	}
	if got := get("?window=6h", http.StatusOK); got.Totals.Restores != 3 {
		t.Fatalf("6h restores = %d", got.Totals.Restores)
	}
	if got := get("?node=wake-a&app_id="+app.ID, http.StatusOK); got.Totals.Restores != 2 || got.Nodes[0].NodeName != "wake-a" {
		t.Fatalf("filtered = %+v", got)
	}
	get("?node=missing", http.StatusNotFound)
	for _, bad := range []string{"?window=48h", "?window=10s", "?window=soon", "?app_id=nope"} {
		get(bad, http.StatusBadRequest)
	}
}
