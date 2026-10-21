package apidserver_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
)

// ADR-960 over HTTP: mode defaults to enforce, can be set to log and back,
// rejects unknown values, and per-rule counts are read back by window.
func TestEdgeRuleModeAndStats(t *testing.T) {
	e := apidserver.Setup(t, api.PlanHobby)
	slug := apidserver.MustSeedEdgeRuleApp(t, e, "log-mode")

	req := apidserver.EdgeRuleRouteReq("canary")
	req.MatchHost = "api.example.com"
	rec := e.Do(t, "POST", "/v1/apps/"+slug+"/edge-rules", req, nil)
	var created api.EdgeRuleResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &created)
	if rec.Code != http.StatusCreated || created.Mode != api.EdgeRuleModeEnforce {
		t.Fatalf("create: %d mode=%q, want 201 enforce", rec.Code, created.Mode)
	}

	logMode := api.EdgeRuleModeLog
	rec = e.Do(t, "PATCH", "/v1/edge-rules/"+created.ID, api.UpdateEdgeRuleRequest{Mode: &logMode}, nil)
	var updated api.EdgeRuleResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &updated)
	if rec.Code != http.StatusOK || updated.Mode != api.EdgeRuleModeLog {
		t.Fatalf("switch to log: %d mode=%q", rec.Code, updated.Mode)
	}
	bogus := "shadow"
	if rec := e.Do(t, "PATCH", "/v1/edge-rules/"+created.ID, api.UpdateEdgeRuleRequest{Mode: &bogus}, nil); rec.Code != http.StatusBadRequest {
		t.Fatalf("unknown mode: %d, want 400", rec.Code)
	}

	var hits state.EdgeRuleHitStore = (*e.Store())
	if err := hits.RecordEdgeRuleHits(context.Background(), []state.EdgeRuleHit{
		{RuleID: created.ID, AppID: created.AppID, Bucket: time.Now(), Outcome: state.EdgeRuleHitLogged, Hits: 7},
		{RuleID: created.ID, AppID: created.AppID, Bucket: time.Now().Add(-3 * time.Hour), Outcome: state.EdgeRuleHitMatched, Hits: 2},
	}); err != nil {
		t.Fatal(err)
	}
	read := func(window string) api.EdgeRuleStatsResponse {
		t.Helper()
		rec := e.Do(t, "GET", "/v1/apps/"+slug+"/edge-rules/stats?window="+window, nil, nil)
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
	if rec := e.Do(t, "GET", "/v1/apps/"+slug+"/edge-rules/stats?window=30d", nil, nil); rec.Code != http.StatusBadRequest {
		t.Fatalf("bad window: %d, want 400", rec.Code)
	}

	// adr: 20261020105001 — series=true adds each rule's hourly totals, one per hour
	// from since through the current hour.
	if got := read("24h"); got.Rules[0].Series != nil {
		t.Fatalf("series without series=true: %+v", got.Rules[0])
	}
	rec = e.Do(t, "GET", "/v1/apps/"+slug+"/edge-rules/stats?window=24h&series=true", nil, nil)
	var withSeries api.EdgeRuleStatsResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &withSeries)
	if rec.Code != http.StatusOK || len(withSeries.Rules) != 1 {
		t.Fatalf("series: %d %s", rec.Code, rec.Body.String())
	}
	series := withSeries.Rules[0].Series
	if len(series) != 25 || series[24] != 7 || series[21] != 2 {
		t.Fatalf("series = %v, want 25 hours with 2 three hours ago and 7 now", series)
	}
	if rec := e.Do(t, "GET", "/v1/apps/"+slug+"/edge-rules/stats?series=maybe", nil, nil); rec.Code != http.StatusBadRequest {
		t.Fatalf("bad series: %d, want 400", rec.Code)
	}
}
