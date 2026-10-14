package main

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

// adr: 986 — `edge-rules recipe` expands the recipe locally and posts an
// ordinary create request: log mode by default, enforce on request, the
// --set values applied, and bad input rejected before any request.
func TestEdgeRulesRecipeCommand(t *testing.T) {
	resetJSONOut(t)
	oldOut := osStdout
	osStdout = io.Discard
	t.Cleanup(func() { osStdout = oldOut })

	f := authedFakeAPI(t, `{"id":"r1","kind":"ip"}`, http.StatusCreated)
	if code := cmdEdgeRules([]string{"recipe", "allow-ips-only", "--app", "demo", "--host", "API.example.com", "--set", "cidrs=203.0.113.7 198.51.100.0/24"}); code != 0 {
		t.Fatalf("exit = %d", code)
	}
	if f.sawMethod != http.MethodPost || f.sawPath != "/v1/apps/demo/edge-rules" {
		t.Fatalf("request = %s %s", f.sawMethod, f.sawPath)
	}
	var req api.CreateEdgeRuleRequest
	if err := json.Unmarshal(f.sawBody, &req); err != nil {
		t.Fatal(err)
	}
	if req.Kind != "ip" || req.Mode != api.EdgeRuleModeLog || req.MatchHost != "api.example.com" || req.MatchPath != "/admin/**" ||
		req.Match == nil || req.Match.Not == nil || strings.Join(req.Match.Not.Values, ",") != "203.0.113.7/32,198.51.100.0/24" {
		t.Fatalf("create request = %s", f.sawBody)
	}

	// adr: 1191 — without --host the rule covers every host the app serves.
	f = authedFakeAPI(t, `{"id":"r0","kind":"ip"}`, http.StatusCreated)
	if code := cmdEdgeRules([]string{"recipe", "block-unverified-bots", "--app", "demo"}); code != 0 {
		t.Fatalf("exit = %d", code)
	}
	if err := json.Unmarshal(f.sawBody, &req); err != nil || req.MatchHost != api.EdgeRuleAppHosts || f.sawPath != "/v1/apps/demo/edge-rules" {
		t.Fatalf("default-host request %s %s", f.sawPath, f.sawBody)
	}

	f = authedFakeAPI(t, `{"id":"r2","kind":"throttle"}`, http.StatusCreated)
	if code := cmdEdgeRules([]string{"recipe", "rate-limit-path", "--app", "demo", "--host", "api.example.com", "--set", "per_minute=30", "--enforce"}); code != 0 {
		t.Fatalf("exit = %d", code)
	}
	if err := json.Unmarshal(f.sawBody, &req); err != nil || req.Mode != api.EdgeRuleModeEnforce || req.Kind != "throttle" {
		t.Fatalf("enforced request = %s", f.sawBody)
	}

	for name, args := range map[string][]string{
		"unknown recipe":    {"recipe", "nope", "--app", "demo", "--host", "h.example"},
		"missing recipe":    {"recipe", "--app", "demo"},
		"extra positional":  {"recipe", "block-unverified-bots", "extra", "--app", "demo", "--host", "h.example"},
		"bad --set":         {"recipe", "block-countries", "--app", "demo", "--host", "h.example", "--set", "countries"},
		"unknown parameter": {"recipe", "block-unverified-bots", "--app", "demo", "--host", "h.example", "--set", "cidrs=1.2.3.4"},
		"invalid value":     {"recipe", "block-countries", "--app", "demo", "--host", "h.example", "--set", "countries=ZZ"},
	} {
		f = authedFakeAPI(t, `{}`, http.StatusCreated)
		if code := cmdEdgeRules(args); code == 0 || f.sawMethod != "" {
			t.Errorf("%s: exit %d, request %q; want failure before any request", name, code, f.sawMethod)
		}
	}

	if code := cmdEdgeRules([]string{"recipes"}); code != 0 {
		t.Fatalf("recipes exit = %d", code)
	}
}
