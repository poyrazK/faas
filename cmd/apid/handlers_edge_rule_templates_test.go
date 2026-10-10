package main

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

// adr: 967 — templated header and redirect actions are validated on write
// and the template flag round-trips.
func TestEdgeRuleTemplatesAPI(t *testing.T) {
	e := setup(t, api.PlanHobby)
	slug := mustSeedEdgeRuleApp(t, e, "templates")
	create := func(kind, action string) (int, string) {
		t.Helper()
		req := api.CreateEdgeRuleRequest{MatchHost: "api.example.com", MatchPath: "/*", Kind: kind, Action: json.RawMessage(action)}
		rec := e.do(t, "POST", "/v1/apps/"+slug+"/edge-rules", req, nil)
		return rec.Code, rec.Body.String()
	}
	if code, body := create("headers", `{"request_headers":[{"name":"X-Client-Country","action":"set","value":"${country}","template":true}]}`); code != http.StatusCreated || !strings.Contains(body, `"template":true`) {
		t.Fatalf("templated headers: %d %s", code, body)
	}
	if code, body := create("redirect", `{"status_code":308,"to":"https://new.example${path}?${query}","template":true}`); code != http.StatusCreated {
		t.Fatalf("templated redirect: %d %s", code, body)
	}
	for name, action := range map[string]string{
		"unknown value":     `{"request_headers":[{"name":"X-A","action":"set","value":"${nope}","template":true}]}`,
		"template remove":   `{"request_headers":[{"name":"X-A","action":"remove","template":true}]}`,
		"open redirect":     `{"status_code":302,"to":"https://${header:x-host}/a","template":true}`,
		"relative variable": `{"status_code":302,"to":"${path}","template":true}`,
	} {
		kind := "headers"
		if strings.Contains(action, "status_code") {
			kind = "redirect"
		}
		if code, body := create(kind, action); code != http.StatusBadRequest && code != http.StatusUnprocessableEntity {
			t.Errorf("%s: %d %s, want 400/422", name, code, body)
		}
	}
	// Without template, ${...} stays a literal and is accepted.
	if code, body := create("headers", `{"request_headers":[{"name":"X-Literal","action":"set","value":"${nope}"}]}`); code != http.StatusCreated {
		t.Fatalf("literal value: %d %s", code, body)
	}
}
