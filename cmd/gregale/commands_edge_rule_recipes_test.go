package main_test

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
)

// adr: 986 — `edge-rules recipe` expands the recipe locally and posts an
// ordinary create request: log mode by default, enforce on request, the
// --set values applied, and bad input rejected before any request.
func TestEdgeRulesRecipeCommand(t *testing.T) {
	main.ResetJSONOut(t)
	oldOut := (*main.OsStdout)
	(*main.OsStdout) = io.Discard
	t.Cleanup(func() { (*main.OsStdout) = oldOut })

	f := main.AuthedFakeAPI(t, `{"id":"r1","kind":"ip"}`, http.StatusCreated)
	if code := main.CmdEdgeRules([]string{"recipe", "allow-ips-only", "--app", "demo", "--host", "API.example.com", "--set", "cidrs=203.0.113.7 198.51.100.0/24"}); code != 0 {
		t.Fatalf("exit = %d", code)
	}
	if (*f.SawMethod()) != http.MethodPost || (*f.SawPath()) != "/v1/apps/demo/edge-rules" {
		t.Fatalf("request = %s %s", (*f.SawMethod()), (*f.SawPath()))
	}
	var req api.CreateEdgeRuleRequest
	if err := json.Unmarshal((*f.SawBody()), &req); err != nil {
		t.Fatal(err)
	}
	if req.Kind != "ip" || req.Mode != api.EdgeRuleModeLog || req.MatchHost != "api.example.com" || req.MatchPath != "/admin/**" ||
		req.Match == nil || req.Match.Not == nil || strings.Join(req.Match.Not.Values, ",") != "203.0.113.7/32,198.51.100.0/24" {
		t.Fatalf("create request = %s", (*f.SawBody()))
	}

	// adr: 1191 — without --host the rule covers every host the app serves.
	f = main.AuthedFakeAPI(t, `{"id":"r0","kind":"ip"}`, http.StatusCreated)
	if code := main.CmdEdgeRules([]string{"recipe", "block-unverified-bots", "--app", "demo"}); code != 0 {
		t.Fatalf("exit = %d", code)
	}
	if err := json.Unmarshal((*f.SawBody()), &req); err != nil || req.MatchHost != api.EdgeRuleAppHosts || (*f.SawPath()) != "/v1/apps/demo/edge-rules" {
		t.Fatalf("default-host request %s %s", (*f.SawPath()), (*f.SawBody()))
	}

	f = main.AuthedFakeAPI(t, `{"id":"r2","kind":"throttle"}`, http.StatusCreated)
	if code := main.CmdEdgeRules([]string{"recipe", "rate-limit-path", "--app", "demo", "--host", "api.example.com", "--set", "per_minute=30", "--enforce"}); code != 0 {
		t.Fatalf("exit = %d", code)
	}
	if err := json.Unmarshal((*f.SawBody()), &req); err != nil || req.Mode != api.EdgeRuleModeEnforce || req.Kind != "throttle" {
		t.Fatalf("enforced request = %s", (*f.SawBody()))
	}

	for name, args := range map[string][]string{
		"unknown recipe":    {"recipe", "nope", "--app", "demo", "--host", "h.example"},
		"missing recipe":    {"recipe", "--app", "demo"},
		"extra positional":  {"recipe", "block-unverified-bots", "extra", "--app", "demo", "--host", "h.example"},
		"bad --set":         {"recipe", "block-countries", "--app", "demo", "--host", "h.example", "--set", "countries"},
		"unknown parameter": {"recipe", "block-unverified-bots", "--app", "demo", "--host", "h.example", "--set", "cidrs=1.2.3.4"},
		"invalid value":     {"recipe", "block-countries", "--app", "demo", "--host", "h.example", "--set", "countries=ZZ"},
	} {
		f = main.AuthedFakeAPI(t, `{}`, http.StatusCreated)
		if code := main.CmdEdgeRules(args); code == 0 || (*f.SawMethod()) != "" {
			t.Errorf("%s: exit %d, request %q; want failure before any request", name, code, (*f.SawMethod()))
		}
	}

	// adr: 986 — --alert-webhook-url adds an edge_rule_activity alert scoped
	// to the new rule after creating it.
	f = main.AuthedFakeAPI(t, `{"id":"0123456789abcdef","kind":"ip","name":"x"}`, http.StatusCreated)
	if code := main.CmdEdgeRules([]string{"recipe", "block-unverified-bots", "--app", "demo", "--alert-webhook-url", "https://hooks.example.com/x", "--alert-webhook-secret", "s3cret", "--alert-threshold", "250"}); code != 0 {
		t.Fatalf("recipe with alert exit = %d", code)
	}
	var alert api.CreateAlertRuleRequest
	if err := json.Unmarshal((*f.SawBody()), &alert); err != nil || (*f.SawPath()) != "/v1/apps/demo/alerts" || alert.Metric != api.AlertMetricEdgeRuleActivity ||
		alert.EdgeRuleID != "0123456789abcdef" || alert.Threshold != 250 || alert.WebhookSecret != "s3cret" {
		t.Fatalf("alert request %s %s", (*f.SawPath()), (*f.SawBody()))
	}

	if code := main.CmdEdgeRules([]string{"recipes"}); code != 0 {
		t.Fatalf("recipes exit = %d", code)
	}
}
