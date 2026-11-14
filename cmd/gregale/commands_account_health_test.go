package main_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
)

// adr: 20261114090000 — `gregale account health` prints the account health
// checks in the dashboard page's order, with dashboard links for next steps
// and server text made terminal-safe; --json passes the response through.
func TestCmdAccountHealth(t *testing.T) {
	now := time.Date(2026, 11, 14, 18, 0, 0, 0, time.UTC)
	health := api.AccountHealth{
		GeneratedAt: now, Attention: 4,
		Restrictions: []api.AccountHealthRestriction{{Kind: api.RestrictionAccountPastDue, Title: "A payment failed",
			Detail: "Update your payment method.", Action: "Update payment method", ActionURL: "/dashboard/billing", Since: &now}},
		Signals: []api.AccountHealthSignal{{Kind: api.AttentionKindAppOOM, Subject: "shop", Title: "shop keeps running out of memory",
			Detail: "It was stopped 3 times.", Action: "Compare plans", ActionURL: "/dashboard/billing", Since: now}},
		FailedDeployments: []api.AccountHealthDeployment{{DeploymentID: "dep-1", AppID: "app-1", AppSlug: "shop",
			ErrorCode: "build_failed", Error: "npm ci exited 1\x1b]52;c;cGF5bG9hZA==\x07", Hint: "Pin node", CreatedAt: now}},
		DomainIssues: []api.AccountHealthDomain{{Domain: "www.shop.example", AppSlug: "shop", Problem: "certificate issuance failed", CertError: "CAA"}},
		RefusedRequests: api.AccountHealthRequests{WindowHours: 24, Total: 2,
			ByCode: []api.AccountHealthCode{{Code: api.CodeAPIKeyRevoked, Count: 2, LastAt: now}},
			Recent: []api.AccountHealthRequest{{OccurredAt: now, Route: "GET /v1/apps", Code: api.CodeAPIKeyRevoked, RequestID: "req-1"}}},
		Unavailable: []string{"billing"},
	}
	var calls int
	stdout := withConsumersTestAPI(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method+" "+r.URL.Path != "GET /v1/account/health" {
			t.Errorf("unexpected route %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
			return
		}
		calls++
		_ = json.NewEncoder(w).Encode(health)
	})

	if code := main.CmdAccount([]string{"health"}); code != 0 {
		t.Fatalf("health exit = %d", code)
	}
	out := stdout.String()
	for _, want := range []string{
		"4 things need your attention.",
		"Some checks could not be loaded (billing)",
		"Account\n  ! A payment failed",
		"Update payment method: http://",
		"Keeps going wrong\n  ! shop keeps running out of memory",
		"Since 14 Nov 2026, 18:00 UTC",
		"shop  dep-1  build_failed",
		"How to fix: Pin node",
		"www.shop.example: certificate issuance failed: CAA",
		"/dashboard/apps/shop/domains/www.shop.example/doctor",
		"Refused API requests, last 24 hours: 2",
		"api_key_revoked x2",
		"GET /v1/apps  api_key_revoked  request req-1",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "\x1b") || strings.Contains(out, "\x07") {
		t.Fatalf("a terminal control sequence reached the output: %q", out)
	}
	if strings.Index(out, "Account\n") > strings.Index(out, "Keeps going wrong") ||
		strings.Index(out, "Keeps going wrong") > strings.Index(out, "Failed deployments") {
		t.Fatalf("sections out of order:\n%s", out)
	}

	stdout.Reset()
	*main.JsonOutput = true
	if code := main.CmdAccount([]string{"health"}); code != 0 {
		t.Fatalf("health --json exit = %d", code)
	}
	var got api.AccountHealth
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("--json output is not account health: %v\n%s", err, stdout.String())
	}
	if got.Attention != 4 || len(got.Signals) != 1 || got.FailedDeployments[0].Error != health.FailedDeployments[0].Error {
		t.Fatalf("--json changed the response: %+v", got)
	}

	*main.JsonOutput = false
	stdout.Reset()
	health = api.AccountHealth{GeneratedAt: now, RefusedRequests: api.AccountHealthRequests{WindowHours: 24}}
	if code := main.CmdAccount([]string{"health"}); code != 0 {
		t.Fatalf("healthy exit = %d", code)
	}
	if out := stdout.String(); !strings.Contains(out, "Nothing needs your attention.") || !strings.Contains(out, "last 24 hours: 0") ||
		strings.Contains(out, "Keeps going wrong") {
		t.Fatalf("healthy output = %q", out)
	}
	if code := main.CmdAccount([]string{"health", "extra"}); code != 1 {
		t.Fatalf("extra argument exit = %d, want 1", code)
	}
	if calls != 3 {
		t.Fatalf("API calls = %d, want 3", calls)
	}
}
