package main

import (
	"encoding/json"
	"net/http"
	"slices"
	"testing"
)

func TestCorsAllowCredentialedDefaultsUseExplicitHeaders(t *testing.T) {
	resetJSONOut(t)
	f := authedFakeAPI(t, `{"id":"rule-1","kind":"cors","match_host":"demo.gregale.dev","match_path":"/*"}`, http.StatusCreated)
	if code := cmdCorsAllow([]string{"demo", "https://app.example", "--host", "demo.gregale.dev", "--credentials"}); code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	var body struct {
		Action struct {
			AllowHeaders []string `json:"allow_headers"`
		} `json:"action"`
	}
	if err := json.Unmarshal(f.sawBody, &body); err != nil {
		t.Fatalf("decode request: %v; body=%s", err, f.sawBody)
	}
	if slices.Contains(body.Action.AllowHeaders, "*") {
		t.Fatalf("credentialed defaults contain wildcard: %v", body.Action.AllowHeaders)
	}
	for _, required := range []string{"Authorization", "Content-Type"} {
		if !slices.Contains(body.Action.AllowHeaders, required) {
			t.Fatalf("credentialed defaults %v missing %s", body.Action.AllowHeaders, required)
		}
	}
}

func TestCorsAllowCredentialedCustomHeaders(t *testing.T) {
	resetJSONOut(t)
	f := authedFakeAPI(t, `{"id":"rule-1","kind":"cors","match_host":"demo.gregale.dev","match_path":"/*"}`, http.StatusCreated)
	args := []string{"demo", "https://app.example", "--host", "demo.gregale.dev", "--credentials", "--allow-header", "content-type", "--allow-header", "x-audit"}
	if code := cmdCorsAllow(args); code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	var body map[string]any
	if err := json.Unmarshal(f.sawBody, &body); err != nil {
		t.Fatal(err)
	}
	action := body["action"].(map[string]any)
	headers := action["allow_headers"].([]any)
	if len(headers) != 2 || headers[0] != "Content-Type" || headers[1] != "X-Audit" {
		t.Fatalf("allow_headers = %v", headers)
	}
}

func TestCorsAllowRejectsCredentialedWildcardHeaders(t *testing.T) {
	resetJSONOut(t)
	if code := cmdCorsAllow([]string{"demo", "https://app.example", "--host", "demo.gregale.dev", "--credentials", "--allow-header", "*"}); code != 1 {
		t.Fatalf("exit = %d, want 1", code)
	}
}
