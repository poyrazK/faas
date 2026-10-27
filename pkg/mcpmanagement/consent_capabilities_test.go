// adr: 20261026195952
package mcpmanagement

import (
	"io"
	"net/url"
	"slices"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestChosenCapabilitiesNeedWritesAndKnownScopes(t *testing.T) {
	if got, err := chosenCapabilities([]string{"storage:manage", "secrets:request"}, true); err != nil || !slices.Equal(got, []string{"storage:manage", "secrets:request"}) {
		t.Fatalf("chosen = %v %v", got, err)
	}
	for _, tc := range []struct {
		values []string
		writes bool
	}{
		{[]string{"secrets:request"}, false},
		{[]string{"secrets:write"}, true},
		{[]string{"admin"}, true},
		{[]string{"storage:manage", "storage:manage"}, true},
	} {
		if _, err := chosenCapabilities(tc.values, tc.writes); err == nil {
			t.Errorf("%v writes=%v accepted", tc.values, tc.writes)
		}
	}
}

func TestNativeConsentCarriesChosenCapabilitiesToTheGrant(t *testing.T) {
	cli := newProvisionCLI()
	f := newRemoteFixtureWithOptions(t, true, HTTPOptions{Onboarding: "browser", CLIRunner: func(token string) LocalCLI {
		if token != "fp_test_alice_remote_key" {
			t.Errorf("runner token %q", token)
		}
		return cli.cli
	}})
	flow := f.begin(scopeRead + " " + scopeWrite)
	start := f.submitConsent(flow, url.Values{"request_id": {flow.requestID}, "csrf": {flow.csrf}, "decision": {"start-login"}, "capability": {"secrets:request", "storage:manage"}}, f.server.URL)
	_ = start.Body.Close()
	if start.StatusCode != 200 {
		t.Fatalf("start-login=%d", start.StatusCode)
	}
	f.mu.Lock()
	mint := f.mints[len(f.mints)-1]
	f.mu.Unlock()
	if mint.Purpose != api.CliAuthPurposeMCP || !slices.Equal(mint.Scopes, []string{api.ScopeDeployWrite, api.ScopeSecretsRequest, api.ScopeStorageManage}) {
		t.Fatalf("minted %+v", mint)
	}
	review := f.consent(flow, "check-login", "", f.server.URL, flow.csrf, true)
	page, _ := io.ReadAll(review.Body)
	_ = review.Body.Close()
	if !strings.Contains(string(page), "enter secrets on a Gregale page") || strings.Contains(string(page), "PostgreSQL databases") {
		t.Fatalf("review page does not list the chosen capabilities: %s", page)
	}
	approved := f.consent(flow, "approve", "", f.server.URL, flow.csrf, true)
	_ = approved.Body.Close()
	if approved.StatusCode != 303 {
		t.Fatalf("approve=%d", approved.StatusCode)
	}
	redirect, err := url.Parse(approved.Header.Get("Location"))
	if err != nil {
		t.Fatal(err)
	}
	var tokens tokenResponse
	decodeResponse(t, f.exchange(redirect.Query().Get("code"), testVerifier, f.resource, f.clientID), &tokens)
	cs, ctx := f.connect(tokens, "2026-07-28")
	tools := listedTools(t, ctx, cs)
	for _, want := range []string{"gregale_request_secret", "gregale_get_secret_request", "gregale_plan_domain", "gregale_plan_cron", "gregale_plan_bucket", "gregale_check_domain", "gregale_apply_provision"} {
		if !tools[want] {
			t.Errorf("%s not listed", want)
		}
	}
	for _, unwanted := range []string{"gregale_plan_postgres", "gregale_plan_local_deploy", "gregale_plan_env_change"} {
		if tools[unwanted] {
			t.Errorf("%s listed without consent", unwanted)
		}
	}
}

func TestNativeConsentRefusesCapabilitiesWithoutWrites(t *testing.T) {
	f := newRemoteFixtureWithOptions(t, true, HTTPOptions{Onboarding: "browser"})
	flow := f.begin(scopeRead)
	reply := f.submitConsent(flow, url.Values{"request_id": {flow.requestID}, "csrf": {flow.csrf}, "decision": {"start-login"}, "capability": {"secrets:request"}}, f.server.URL)
	_ = reply.Body.Close()
	if reply.StatusCode != 400 {
		t.Fatalf("read-only consent accepted a capability: %d", reply.StatusCode)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.mints) != 0 {
		t.Fatalf("a code was minted: %+v", f.mints)
	}
}
