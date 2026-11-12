package apidserver

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/customererrors"
	"github.com/onebox-faas/faas/pkg/state"
)

// supportVisibilityFixture is an operator env plus one customer account
// with an app, a custom domain and a failed deployment.
type supportVisibilityFixture struct {
	env      testEnv
	customer state.Account
	app      state.App
	failed   state.Deployment
}

func newSupportVisibilityFixture(t *testing.T) supportVisibilityFixture {
	t.Helper()
	ctx := context.Background()
	e := newObsEnv(t, api.ScopesAdminOnly, "ops@faas.dev", "ops@faas.dev")
	customer, err := e.store.CreateAccount(ctx, "customer@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := e.store.CreateApp(ctx, state.App{AccountID: customer.ID, Slug: "shop", Type: state.AppTypeApp})
	if err != nil {
		t.Fatal(err)
	}
	failed, err := e.store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Status: state.DeployPending})
	if err != nil {
		t.Fatal(err)
	}
	if err := e.store.UpdateDeploymentStatus(ctx, failed.ID, state.DeployFailed, "build failed: npm ci exited 1"); err != nil {
		t.Fatal(err)
	}
	if _, err := e.store.CreateCustomDomain(ctx, "www.shop.example", app.ID, "verify-token"); err != nil {
		t.Fatal(err)
	}
	return supportVisibilityFixture{env: e, customer: customer, app: app, failed: failed}
}

func (f supportVisibilityFixture) lookup(t *testing.T, q string) (int, api.ObsLookupResponse) {
	t.Helper()
	rec := f.env.doOperator(t, "GET", "/v1/admin/obs/lookup?"+url.Values{"q": {q}}.Encode(), nil, nil)
	var response api.ObsLookupResponse
	if rec.Code == http.StatusOK {
		if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
			t.Fatal(err)
		}
	}
	return rec.Code, response
}

func TestObsLookupResolvesCustomerIdentifiers(t *testing.T) {
	f := newSupportVisibilityFixture(t)
	for _, tc := range []struct {
		name, q, kind, detail string
		app                   bool
	}{
		{"email any case", "Customer@Example.TEST", api.ObsLookupAccount, "email", false},
		{"account id", f.customer.ID, api.ObsLookupAccount, "account id", false},
		{"app id", f.app.ID, api.ObsLookupApp, "app id", true},
		{"deployment id", f.failed.ID, api.ObsLookupDeployment, "deployment failed", true},
		{"app slug", "shop", api.ObsLookupApp, "app slug", true},
		{"app URL", "https://shop.gregale.dev/checkout?x=1", api.ObsLookupApp, "app hostname", true},
		{"preview hostname", "shop--pr-12.gregale.dev", api.ObsLookupApp, "app hostname", true},
		{"custom domain URL", "https://WWW.shop.example/", api.ObsLookupDomain, "custom domain", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			code, response := f.lookup(t, tc.q)
			if code != http.StatusOK {
				t.Fatalf("status=%d", code)
			}
			found := false
			for _, m := range response.Matches {
				if m.Kind == tc.kind && m.Detail == tc.detail && m.AccountID == f.customer.ID {
					found = true
					if tc.app && (m.AppID != f.app.ID || m.AppSlug != "shop") {
						t.Fatalf("match lacks app: %+v", m)
					}
				}
			}
			if !found {
				t.Fatalf("no %s/%s match in %+v", tc.kind, tc.detail, response.Matches)
			}
		})
	}

	if code, response := f.lookup(t, "nobody-has-this"); code != http.StatusOK || len(response.Matches) != 0 || response.Matches == nil {
		t.Fatalf("unknown identifier: status=%d matches=%#v", code, response.Matches)
	}
	if code, _ := f.lookup(t, strings.Repeat("a", api.ObsLookupQueryMaxBytes+1)); code != http.StatusBadRequest {
		t.Fatalf("oversized query status=%d, want 400", code)
	}
	if code, _ := f.lookup(t, "   "); code != http.StatusBadRequest {
		t.Fatalf("blank query status=%d, want 400", code)
	}
	// Matches never carry an email; only the query echoes what was asked.
	_, response := f.lookup(t, "customer@example.test")
	encoded, _ := json.Marshal(response.Matches)
	if strings.Contains(string(encoded), "@") {
		t.Fatalf("lookup matches carry an email: %s", encoded)
	}
}

func TestObsTenant360DiagnosisExplainsBrokenAccount(t *testing.T) {
	f := newSupportVisibilityFixture(t)
	ctx := context.Background()
	if _, err := f.env.store.SetAccountAbuseHold(ctx, f.customer.ID, state.AccountAbuseHoldOperator, time.Now()); err != nil {
		t.Fatal(err)
	}
	rec := f.env.doOperator(t, "GET", "/v1/admin/obs/tenants/"+f.customer.ID+"/360", nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("360 status=%d body=%s", rec.Code, rec.Body.String())
	}
	var response api.ObsTenant360Response
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	d := response.Diagnosis
	if d.GeneratedAt.IsZero() || len(d.Unavailable) != 0 {
		t.Fatalf("diagnosis incomplete: %+v", d)
	}
	hold := false
	for _, r := range d.Restrictions {
		if r.Kind == api.RestrictionAbuseHold && r.Reason == state.AccountAbuseHoldOperator && r.Since != nil {
			hold = true
		}
	}
	if !hold {
		t.Fatalf("abuse hold missing from restrictions: %+v", d.Restrictions)
	}
	if len(d.FailedDeployments) != 1 || d.FailedDeployments[0].DeploymentID != f.failed.ID ||
		d.FailedDeployments[0].AppSlug != "shop" || !strings.Contains(d.FailedDeployments[0].Error, "npm ci") {
		t.Fatalf("failed deployments = %+v", d.FailedDeployments)
	}
	if len(d.DomainIssues) != 1 || d.DomainIssues[0].Domain != "www.shop.example" || d.DomainIssues[0].Problem != "domain not verified" {
		t.Fatalf("domain issues = %+v", d.DomainIssues)
	}
	if d.CustomerErrors.WindowHours != 24 || d.CustomerErrors.ByCode == nil || d.CustomerErrors.Recent == nil || d.EnabledFlags == nil {
		t.Fatalf("empty sections must be lists, not null: %+v", d)
	}
}

func TestAccountRestrictionsCoverAccountStates(t *testing.T) {
	now := time.Now()
	limit := int64(500)
	got := accountRestrictions(state.Account{Status: state.AccountPastDue, PastDueAt: &now, DeletionRequestedAt: &now},
		api.ObsTenantBilling{CurrentMonthOverageCents: 500, OverageCapCents: &limit})
	kinds := map[string]bool{}
	for _, r := range got {
		kinds[r.Kind] = true
	}
	for _, want := range []string{api.RestrictionAccountPastDue, api.RestrictionDeletionPending, api.RestrictionEmailUnverified, api.RestrictionOverageCapReached} {
		if !kinds[want] {
			t.Errorf("missing %s in %+v", want, got)
		}
	}
	verified := now
	if got := accountRestrictions(state.Account{Status: state.AccountActive, EmailVerifiedAt: &verified}, api.ObsTenantBilling{}); len(got) != 0 {
		t.Fatalf("healthy account has restrictions: %+v", got)
	}
}

func TestDomainIssueClassifiesCertificateStates(t *testing.T) {
	now := time.Now()
	verified := now.Add(-time.Hour)
	for _, tc := range []struct {
		domain  state.CustomDomain
		problem string
	}{
		{state.CustomDomain{Domain: "a", VerifiedAt: verified, CertStatus: state.CustomDomainCertIssued, CertExpiresAt: now.Add(time.Hour)}, ""},
		{state.CustomDomain{Domain: "b", VerifiedAt: verified, CertStatus: state.CustomDomainCertFailed, CertLastError: "CAA forbids"}, "certificate issuance failed"},
		{state.CustomDomain{Domain: "c", VerifiedAt: verified, CertStatus: state.CustomDomainCertDNSDrifted}, "DNS no longer points at Gregale"},
		{state.CustomDomain{Domain: "d", VerifiedAt: verified, CertStatus: state.CustomDomainCertIssued, CertExpiresAt: now.Add(-time.Minute)}, "certificate expired"},
	} {
		issue, ok := domainIssue(tc.domain, now)
		if (tc.problem == "") == ok || issue.Problem != tc.problem {
			t.Errorf("%s: issue=%+v ok=%v, want %q", tc.domain.Domain, issue, ok, tc.problem)
		}
	}
}

func TestRecordCustomerErrorsCapturesCustomerProblemsOnly(t *testing.T) {
	e := newObsEnv(t, api.ScopesAdminOnly, "ops@faas.dev", "ops@faas.dev")
	var captured []customererrors.Event
	e.s.customerErrorSink = func(event customererrors.Event) { captured = append(captured, event) }

	rec := e.do(t, "GET", "/v1/apps/missing-app", nil, map[string]string{api.RequestIDHeader: "req-123"})
	if rec.Code < 400 {
		t.Fatalf("missing app status=%d, want a problem", rec.Code)
	}
	if len(captured) != 1 {
		t.Fatalf("captured %d events, want 1: %+v", len(captured), captured)
	}
	got := captured[0]
	if got.AccountID != e.acct.ID || got.Route != "GET /v1/apps/{slug}" || got.Code == "" || got.AppSlug != "missing-app" || got.RequestID != "req-123" {
		t.Fatalf("event = %+v", got)
	}
	if got.Code != rec.Header().Get(api.ErrorCodeHeader) {
		t.Fatalf("code %q != response error code %q", got.Code, rec.Header().Get(api.ErrorCodeHeader))
	}

	captured = nil
	if rec := e.do(t, "GET", "/v1/apps", nil, nil); rec.Code != http.StatusOK {
		t.Fatalf("list apps status=%d", rec.Code)
	}
	if rec := e.doOperator(t, "GET", "/v1/admin/obs/tenants/not-a-uuid/360", nil, nil); rec.Code != http.StatusBadRequest {
		t.Fatalf("operator bad id status=%d", rec.Code)
	}
	if len(captured) != 0 {
		t.Fatalf("success or operator responses were recorded: %+v", captured)
	}
}
