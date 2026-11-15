package main

import (
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// adr: 20261115090000 — accounts risk asks for PII only on --include-pii and
// prints the hold, sign-up record, links, strikes and events.
func TestOperatorAccountsRisk(t *testing.T) {
	held := time.Date(2026, 11, 15, 8, 0, 0, 0, time.UTC)
	seen := operatorAPIServer(t, api.AccountRiskResponse{
		AccountID: testAccountID, Status: "active", Plan: "free", CreatedAt: held.Add(-time.Hour),
		AbuseHoldAt: &held, AbuseHoldReason: "cpu_sustained",
		Signup: &api.AccountRiskSignup{Method: "password", Country: "NL", ASN: 64501, ASNOrg: "Example Hosting",
			EmailDomain: "acme.io", CreatedAt: held.Add(-time.Hour)},
		LinkedAccounts: []api.AccountRiskLink{{AccountID: "new-acct", HeldAccountID: testAccountID, HeldReason: "cpu_sustained", Signal: "signup_network", DetectedAt: held}},
		Strikes:        []api.AccountRiskStrike{{Reason: "cpu_sustained", StruckAt: held}},
		AbuseEvents:    []api.AccountRiskEvent{{At: held, Kind: "accounts.abuse_hold", Actor: "schedd", Subject: testAccountID, Data: []byte(`{"reason": "cpu_sustained"}`)}},
	})
	out, stderr, restore := captureOperatorIO()
	defer restore()
	if code := run([]string{"accounts", "risk", "--account-id", testAccountID}); code != 0 {
		t.Fatalf("run = %d, stderr=%s", code, stderr.String())
	}
	got := (*seen)[0]
	if got.path != "/v1/admin/accounts/"+testAccountID+"/risk" || got.query != "" {
		t.Fatalf("request = %+v; want no include_pii by default", got)
	}
	for _, want := range []string{
		"abuse_hold=cpu_sustained", "signup method=password country=NL asn=64501", "email_domain=acme.io",
		"linked_account=new-acct signal=signup_network", "strike reason=cpu_sustained", "kind=accounts.abuse_hold",
		`data={"reason":"cpu_sustained"}`, "summary links_to_held=0 linked_accounts=1 strikes=1 abuse_events=1",
	} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("stdout missing %q:\n%s", want, out.String())
		}
	}
	if code := run([]string{"accounts", "risk", "--account-id", testAccountID, "--include-pii"}); code != 0 {
		t.Fatalf("run with PII = %d, stderr=%s", code, stderr.String())
	}
	if got := (*seen)[1]; got.query != "include_pii=1" {
		t.Fatalf("PII request = %+v", got)
	}
	if code := run([]string{"accounts", "risk", "--account-id", "nope"}); code != 2 {
		t.Fatalf("bad account id = %d, want 2", code)
	}
}

func TestOperatorAccountsHeldLinks(t *testing.T) {
	seen := operatorAPIServer(t, api.HeldLinksResponse{Links: []api.HeldLinkReview{{
		AccountRiskLink: api.AccountRiskLink{AccountID: "new-acct", HeldAccountID: testAccountID, HeldReason: "egress_fanout",
			Signal: "email_domain", DetectedAt: time.Date(2026, 11, 15, 9, 0, 0, 0, time.UTC)},
		Country: "DE", EmailDomain: "acme.io",
	}}})
	out, stderr, restore := captureOperatorIO()
	defer restore()
	if code := run([]string{"accounts", "held-links", "--since", "48h", "--limit", "5"}); code != 0 {
		t.Fatalf("run = %d, stderr=%s", code, stderr.String())
	}
	got := (*seen)[0]
	if got.path != "/v1/admin/held-links" || got.query != "limit=5&since=48h0m0s" {
		t.Fatalf("request = %+v", got)
	}
	if !strings.Contains(out.String(), "account_id=new-acct held_account="+testAccountID+" held_reason=egress_fanout signal=email_domain country=DE email_domain=acme.io") {
		t.Fatalf("stdout = %q", out.String())
	}
	for _, bad := range [][]string{{"--since", "forever"}, {"--since", "1000h"}, {"--limit", "5000"}} {
		if code := run(append([]string{"accounts", "held-links"}, bad...)); code != 2 {
			t.Errorf("held-links %v = %d, want 2", bad, code)
		}
	}
}
