package main

import (
	"context"
	"encoding/json"
	"math"
	"net/http"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestGetSyntheticCheckReportsResults(t *testing.T) {
	e := setup(t, api.PlanPro)
	createApp(t, e, "shop")
	rec := e.do(t, http.MethodPost, "/v1/apps/shop/synthetics", api.CreateSyntheticCheckRequest{Name: "health", Path: "/healthz", IntervalSeconds: 300}, nil)
	var created api.SyntheticCheckResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("create: %d %s", rec.Code, rec.Body)
	}
	now := time.Now().UTC().Truncate(time.Second)
	for i, run := range []state.SyntheticCheckRun{
		{OK: true, StatusCode: 200, LatencyMS: 120},
		{OK: true, StatusCode: 200, LatencyMS: 900}, // a wake
		{OK: true, StatusCode: 200, LatencyMS: 80},
		{StatusCode: 503, LatencyMS: 40, ErrorClass: "status"},
	} {
		run.CheckID, run.StartedAt = created.ID, now.Add(-time.Duration(i)*5*time.Minute)
		if err := e.store.RecordSyntheticCheckRun(context.Background(), run); err != nil {
			t.Fatal(err)
		}
	}
	rec = e.do(t, http.MethodGet, "/v1/apps/shop/synthetics/"+created.ID, nil, nil)
	var got api.SyntheticCheckResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil || got.Results == nil {
		t.Fatalf("get: %d %s", rec.Code, rec.Body)
	}
	res := got.Results
	if res.Runs24h != 4 || res.Uptime24hPct == nil || math.Abs(*res.Uptime24hPct-75) > 1e-9 {
		t.Fatalf("results = %+v", res)
	}
	if len(res.Recent) != 4 || !res.Recent[0].OK || res.Recent[3].ErrorClass != "status" {
		t.Fatalf("recent = %+v; want newest first", res.Recent)
	}
	if res.P95LatencyMS24h != 900 {
		t.Fatalf("p95 = %v, want 900 (successful runs only)", res.P95LatencyMS24h)
	}
}
