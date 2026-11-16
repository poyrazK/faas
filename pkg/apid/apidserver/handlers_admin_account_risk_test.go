// adr: 20261115090000 — the operator account risk view and held-link review.
package apidserver_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestAdminAccountRisk(t *testing.T) {
	e := apidserver.NewIssueCreditEnv(t, api.ScopesAdminOnly, "ops@example.com", "ops@example.com")
	mem := (*e.Store())
	ctx := context.Background()
	now := (*apidserver.TimeNow)().UTC()

	held, err := mem.CreateAccount(ctx, "held@acme-risk.io", api.PlanFree)
	if err != nil {
		t.Fatal(err)
	}
	newcomer, err := mem.CreateAccount(ctx, "new@acme-risk.io", api.PlanFree)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range []state.AccountSignupFacts{
		{AccountID: held.ID, Method: "password", SignupIP: "198.51.100.5", UserAgent: "held-ua", Country: "NL", EmailDomain: "acme-risk.io", CreatedAt: now.Add(-2 * time.Hour)},
		{AccountID: newcomer.ID, Method: "github", SignupIP: "198.51.100.6", UserAgent: "new-ua", Country: "NL", EmailDomain: "acme-risk.io", CreatedAt: now.Add(-time.Minute)},
	} {
		if err := mem.RecordAccountSignupFacts(ctx, f); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := mem.RecordAccountAbuseStrike(ctx, held.ID, state.AccountAbuseHoldCPUSustained, "", now.Add(-10*time.Minute), time.Hour); err != nil {
		t.Fatal(err)
	}
	if _, err := mem.SetAccountAbuseHold(ctx, held.ID, state.AccountAbuseHoldCPUSustained, now.Add(-9*time.Minute)); err != nil {
		t.Fatal(err)
	}
	subject := held.ID
	if err := mem.AppendEventAt(ctx, "schedd", "accounts.abuse_hold", &subject, []byte(`{"reason":"cpu_sustained"}`), now.Add(-9*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err := mem.AppendEventAt(ctx, "schedd", "wake.boot_started", &subject, []byte(`{}`), now.Add(-8*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if _, err := mem.LinkAccountToHeld(ctx, newcomer.ID, true, now.Add(-90*24*time.Hour), now); err != nil {
		t.Fatal(err)
	}

	get := func(path string) (int, []byte) {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		e.AddAdminSession(t, req)
		rec := httptest.NewRecorder()
		(*e.H()).ServeHTTP(rec, req)
		return rec.Code, rec.Body.Bytes()
	}

	code, body := get("/v1/admin/accounts/" + held.ID + "/risk")
	if code != http.StatusOK {
		t.Fatalf("risk: %d %s", code, body)
	}
	var risk api.AccountRiskResponse
	if err := json.Unmarshal(body, &risk); err != nil {
		t.Fatal(err)
	}
	if risk.Email != "" || risk.Signup == nil || risk.Signup.SignupIP != "" || risk.Signup.UserAgent != "" {
		t.Fatalf("default view leaked PII: email=%q signup=%+v", risk.Email, risk.Signup)
	}
	if risk.AbuseHoldReason != state.AccountAbuseHoldCPUSustained || risk.Signup.Country != "NL" || risk.Signup.EmailDomain != "acme-risk.io" {
		t.Fatalf("risk = %+v", risk)
	}
	if len(risk.Strikes) != 1 || risk.Strikes[0].Reason != state.AccountAbuseHoldCPUSustained {
		t.Fatalf("strikes = %+v", risk.Strikes)
	}
	if len(risk.LinkedAccounts) != 2 || len(risk.LinksToHeld) != 0 {
		t.Fatalf("links: to_held=%+v linked=%+v; want the newcomer linked by network and domain", risk.LinksToHeld, risk.LinkedAccounts)
	}
	if len(risk.AbuseEvents) != 1 || risk.AbuseEvents[0].Kind != "accounts.abuse_hold" {
		t.Fatalf("abuse events = %+v; want only the hold", risk.AbuseEvents)
	}

	code, body = get("/v1/admin/accounts/" + held.ID + "/risk?include_pii=1")
	if code != http.StatusOK {
		t.Fatalf("risk with PII: %d %s", code, body)
	}
	risk = api.AccountRiskResponse{}
	if err := json.Unmarshal(body, &risk); err != nil {
		t.Fatal(err)
	}
	if risk.Email != "held@acme-risk.io" || risk.Signup.SignupIP != "198.51.100.5" || risk.Signup.UserAgent != "held-ua" {
		t.Fatalf("PII view = email %q signup %+v", risk.Email, risk.Signup)
	}

	code, body = get("/v1/admin/accounts/" + newcomer.ID + "/risk")
	if code != http.StatusOK {
		t.Fatalf("newcomer risk: %d %s", code, body)
	}
	risk = api.AccountRiskResponse{}
	_ = json.Unmarshal(body, &risk)
	if len(risk.LinksToHeld) != 2 || risk.LinksToHeld[0].HeldAccountID != held.ID {
		t.Fatalf("newcomer links_to_held = %+v", risk.LinksToHeld)
	}

	if code, _ := get("/v1/admin/accounts/not-a-uuid/risk"); code != http.StatusBadRequest {
		t.Fatalf("bad id: %d, want 400", code)
	}
	if code, _ := get("/v1/admin/accounts/00000000-0000-4000-8000-000000000000/risk"); code != http.StatusNotFound {
		t.Fatalf("missing account: %d, want 404", code)
	}

	code, body = get("/v1/admin/held-links?since=1h")
	if code != http.StatusOK {
		t.Fatalf("held-links: %d %s", code, body)
	}
	var review api.HeldLinksResponse
	if err := json.Unmarshal(body, &review); err != nil {
		t.Fatal(err)
	}
	if len(review.Links) != 2 || review.Links[0].AccountID != newcomer.ID || review.Links[0].EmailDomain != "acme-risk.io" || review.Truncated {
		t.Fatalf("held-links = %+v", review)
	}
	for _, bad := range []string{"since=forever", "since=1000h", "limit=0"} {
		if code, _ := get("/v1/admin/held-links?" + bad); code != http.StatusBadRequest {
			t.Errorf("held-links?%s: %d, want 400", bad, code)
		}
	}
}
