package main

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestAppEdgeProtectionSummarizesPerAppCounters(t *testing.T) {
	e := setup(t, api.PlanPro)
	created := e.do(t, "POST", "/v1/apps", api.CreateAppRequest{Slug: "shielded"}, nil)
	if created.Code != http.StatusCreated {
		t.Fatalf("create = %d: %s", created.Code, created.Body.String())
	}
	var app api.AppResponse
	if err := json.Unmarshal(created.Body.Bytes(), &app); err != nil {
		t.Fatal(err)
	}
	installPromFixture(t, &e, func(query string) string {
		if !strings.Contains(query, `[1h]`) || !strings.Contains(query, app.ID) {
			t.Errorf("unexpected query: %s", query)
		}
		switch {
		case strings.Contains(query, "gateway_pre_auth_rate_limit_total"):
			return `{"status":"success","data":{"resultType":"vector","result":[
				{"metric":{"outcome":"blocked"},"value":[0,"3"]},
				{"metric":{"outcome":"route_would_block"},"value":[0,"7"]}]}}`
		case strings.Contains(query, "gateway_waf_inspections_total"):
			return `{"status":"success","data":{"resultType":"vector","result":[
				{"metric":{"outcome":"clean"},"value":[0,"90"]},
				{"metric":{"outcome":"detected"},"value":[0,"6"]},
				{"metric":{"outcome":"sampled_out"},"value":[0,"3"]},
				{"metric":{"outcome":"dropped"},"value":[0,"1"]}]}}`
		case strings.Contains(query, "gateway_waf_detections_total"):
			return `{"status":"success","data":{"resultType":"vector","result":[
				{"metric":{"category":"xss"},"value":[0,"2"]},
				{"metric":{"category":"sqli"},"value":[0,"5"]}]}}`
		case strings.Contains(query, "gateway_waf_rule_matches_total"):
			if !strings.HasPrefix(query, "topk(10,") {
				t.Errorf("rule query is not bounded: %s", query)
			}
			return `{"status":"success","data":{"resultType":"vector","result":[
				{"metric":{"rule_id":"941100"},"value":[0,"2"]},
				{"metric":{"rule_id":"942100"},"value":[0,"5"]}]}}`
		case strings.Contains(query, "gateway_validate_failures_total"):
			return `{"status":"success","data":{"resultType":"vector","result":[
				{"metric":{"mode":"observe"},"value":[0,"4"]},
				{"metric":{"mode":"block"},"value":[0,"0"]}]}}`
		default:
			return `{"status":"success","data":{"resultType":"vector","result":[
				{"metric":{"kind":"ip","status":"403"},"value":[0,"2"]},
				{"metric":{"kind":"throttle","status":"429"},"value":[0,"9"]}]}}`
		}
	})
	rec := e.do(t, "GET", "/v1/apps/shielded/edge-protection", nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("edge-protection = %d: %s", rec.Code, rec.Body.String())
	}
	var out api.EdgeProtectionResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if out.Range != "1h" || out.Source != "prometheus" || out.PreAuth.Blocked != 3 || out.PreAuth.WouldBlock != 7 {
		t.Fatalf("summary = %+v", out)
	}
	if len(out.ValidationFailures) != 1 || out.ValidationFailures[0] != (api.EdgeProtectionCount{Name: "observe", Count: 4}) {
		t.Fatalf("validation failures = %+v (zero counts must be omitted)", out.ValidationFailures)
	}
	if len(out.Rejections) != 2 || out.Rejections[0].Gate != "throttle" || out.Rejections[0].Count != 9 {
		t.Fatalf("rejections = %+v, want largest first", out.Rejections)
	}
	if out.WAF.Inspected != 96 || out.WAF.Detected != 6 || out.WAF.NotInspected != 4 {
		t.Fatalf("waf = %+v, want 96 inspected / 6 detected / 4 not inspected", out.WAF)
	}
	if len(out.WAF.Categories) != 2 || out.WAF.Categories[0].Name != "sqli" ||
		len(out.WAF.TopRules) != 2 || out.WAF.TopRules[0] != (api.EdgeProtectionCount{Name: "942100", Count: 5}) {
		t.Fatalf("waf breakdown = %+v, want largest first", out.WAF)
	}
	if rec := e.do(t, "GET", "/v1/apps/shielded/edge-protection?range=30d", nil, nil); rec.Code != http.StatusBadRequest {
		t.Fatalf("invalid range = %d", rec.Code)
	}
}

func TestAppEdgeProtectionDegradedWithoutPrometheus(t *testing.T) {
	e := setup(t, api.PlanPro)
	if rec := e.do(t, "POST", "/v1/apps", api.CreateAppRequest{Slug: "shielded"}, nil); rec.Code != http.StatusCreated {
		t.Fatalf("create = %d", rec.Code)
	}
	rec := e.do(t, "GET", "/v1/apps/shielded/edge-protection", nil, nil)
	var out api.EdgeProtectionResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil || !strings.HasPrefix(out.Source, "degraded:") || out.Rejections == nil || out.WAF.TopRules == nil {
		t.Fatalf("degraded summary = %d %+v (err=%v)", rec.Code, out, err)
	}
}
