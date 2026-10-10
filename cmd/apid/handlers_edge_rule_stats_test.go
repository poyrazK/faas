package main

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// ADR-960 over HTTP: mode defaults to enforce, can be set to log and back,
// rejects unknown values, and per-rule counts are read back by window.
func TestEdgeRuleModeAndStats(t *testing.T) {
	e := setup(t, api.PlanHobby)
	slug := mustSeedEdgeRuleApp(t, e, "log-mode")

	req := edgeRuleRouteReq("canary")
	req.MatchHost = "api.example.com"
	rec := e.do(t, "POST", "/v1/apps/"+slug+"/edge-rules", req, nil)
	var created api.EdgeRuleResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &created)
	if rec.Code != http.StatusCreated || created.Mode != api.EdgeRuleModeEnforce {
		t.Fatalf("create: %d mode=%q, want 201 enforce", rec.Code, created.Mode)
	}

	logMode := api.EdgeRuleModeLog
	rec = e.do(t, "PATCH", "/v1/edge-rules/"+created.ID, api.UpdateEdgeRuleRequest{Mode: &logMode}, nil)
	var updated api.EdgeRuleResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &updated)
	if rec.Code != http.StatusOK || updated.Mode != api.EdgeRuleModeLog {
		t.Fatalf("switch to log: %d mode=%q", rec.Code, updated.Mode)
	}
	bogus := "shadow"
	if rec := e.do(t, "PATCH", "/v1/edge-rules/"+created.ID, api.UpdateEdgeRuleRequest{Mode: &bogus}, nil); rec.Code != http.StatusBadRequest {
		t.Fatalf("unknown mode: %d, want 400", rec.Code)
	}

	var hits state.EdgeRuleHitStore = e.store
	if err := hits.RecordEdgeRuleHits(context.Background(), []state.EdgeRuleHit{
		{RuleID: created.ID, AppID: created.AppID, Bucket: time.Now(), Outcome: state.EdgeRuleHitLogged, Hits: 7},
		{RuleID: created.ID, AppID: created.AppID, Bucket: time.Now().Add(-3 * time.Hour), Outcome: state.EdgeRuleHitMatched, Hits: 2},
	}); err != nil {
		t.Fatal(err)
	}
	read := func(window string) api.EdgeRuleStatsResponse {
		t.Helper()
		rec := e.do(t, "GET", "/v1/apps/"+slug+"/edge-rules/stats?window="+window, nil, nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("stats %s: %d %s", window, rec.Code, rec.Body.String())
		}
		var out api.EdgeRuleStatsResponse
		_ = json.Unmarshal(rec.Body.Bytes(), &out)
		return out
	}
	if got := read("1h"); len(got.Rules) != 1 || got.Rules[0].Logged != 7 || got.Rules[0].Matched != 0 {
		t.Fatalf("1h stats = %+v, want only the recent logged hits", got.Rules)
	}
	if got := read("24h"); len(got.Rules) != 1 || got.Rules[0].Logged != 7 || got.Rules[0].Matched != 2 {
		t.Fatalf("24h stats = %+v", got.Rules)
	}
	if rec := e.do(t, "GET", "/v1/apps/"+slug+"/edge-rules/stats?window=30d", nil, nil); rec.Code != http.StatusBadRequest {
		t.Fatalf("bad window: %d, want 400", rec.Code)
	}
}
