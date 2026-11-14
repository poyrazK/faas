package apidserver

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/support"
	"github.com/onebox-faas/faas/pkg/apid/supportportal"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestCustomerAccountHealthHidesInternalDetail(t *testing.T) {
	now := time.Now().UTC()
	health := customerAccountHealth(api.ObsTenantDiagnosis{
		GeneratedAt: now,
		Restrictions: []api.ObsTenantRestriction{
			{Kind: api.RestrictionAbuseHold, Reason: "egress_fanout", Since: &now},
			{Kind: api.RestrictionAbuseHold, Reason: "organization acme: operator"},
			{Kind: api.RestrictionEmailUnverified},
			{Kind: "future_internal_kind", Reason: "secret detection signal"},
		},
		FailedDeployments: []api.ObsTenantFailedDeploy{{DeploymentID: "d1", AppID: "a1", AppSlug: "shop", ErrorCode: "build_failed", Error: "npm ci", Hint: "pin node", CreatedAt: now}},
		DomainIssues:      []api.ObsTenantDomainIssue{{Domain: "www.shop.example", AppSlug: "shop", Problem: "certificate issuance failed", CertLastError: "CAA"}},
		EnabledFlags:      []string{"secret_preview"},
		CustomerErrors: api.ObsCustomerErrorSummary{WindowHours: 24, Total: 2, Dropped: 99,
			ByCode: []api.ObsCustomerErrorCode{{Code: "plan_excess", Count: 2, LastAt: now}},
			Recent: []api.ObsCustomerErrorEvent{{OccurredAt: now, Route: "POST /v1/apps", Code: "plan_excess", RequestID: "req-1"}}},
	})
	encoded, err := json.Marshal(health)
	if err != nil {
		t.Fatal(err)
	}
	for _, leak := range []string{"egress_fanout", "operator", "acme", "future_internal_kind", "secret detection", "secret_preview", "dropped"} {
		if strings.Contains(string(encoded), leak) {
			t.Errorf("customer view leaks %q: %s", leak, encoded)
		}
	}
	if len(health.Restrictions) != 2 || health.Restrictions[0].Kind != api.RestrictionAbuseHold || health.Restrictions[0].Since == nil ||
		health.Restrictions[0].ActionURL != "/dashboard/support#new-ticket" || health.Restrictions[1].Kind != api.RestrictionEmailUnverified {
		t.Fatalf("restrictions = %+v", health.Restrictions)
	}
	if health.Attention != 4 || health.FailedDeployments[0].Hint != "pin node" || health.DomainIssues[0].CertError != "CAA" ||
		health.RefusedRequests.Total != 2 || health.RefusedRequests.Recent[0].RequestID != "req-1" {
		t.Fatalf("health = %+v", health)
	}
	for kind := range customerRestrictionCopy {
		if c := customerRestrictionCopy[kind]; c.Title == "" || c.Detail == "" || (c.Action == "") != (c.ActionURL == "") {
			t.Errorf("%s wording incomplete: %+v", kind, c)
		}
	}
}

func customerSessionCookie(t *testing.T, e testEnv, accountID string) *http.Cookie {
	t.Helper()
	sid := uuid.NewString()
	if _, err := e.store.CreateSession(context.Background(), sid, accountID, "192.0.2.20", "customer-test-ua"); err != nil {
		t.Fatal(err)
	}
	token, err := e.s.sessions.IssueWithSessionAndBindingHashAndStepUp(sid, accountID, "", time.Now(), false)
	if err != nil {
		t.Fatal(err)
	}
	return &http.Cookie{Name: sessionCookie, Value: token}
}

func TestAccountHealthAPIAndDashboard(t *testing.T) {
	ctx := context.Background()
	e := newObsEnv(t, api.ScopesReadSurface, "ops@faas.dev", "customer@faas.dev")
	app, err := e.store.CreateApp(ctx, state.App{AccountID: e.acct.ID, Slug: "shop", Type: state.AppTypeApp})
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
	if err := e.store.UpdateAccountStatus(ctx, e.acct.ID, state.AccountPastDue); err != nil {
		t.Fatal(err)
	}
	// Another account's failure never appears.
	other, err := e.store.CreateAccount(ctx, "other@faas.dev", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	otherApp, err := e.store.CreateApp(ctx, state.App{AccountID: other.ID, Slug: "rival", Type: state.AppTypeApp})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.store.CreateDeployment(ctx, state.Deployment{AppID: otherApp.ID, Status: state.DeployFailed}); err != nil {
		t.Fatal(err)
	}

	rec := e.do(t, "GET", "/v1/account/health", nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var health api.AccountHealth
	if err := json.Unmarshal(rec.Body.Bytes(), &health); err != nil {
		t.Fatal(err)
	}
	if len(health.Restrictions) != 1 || health.Restrictions[0].Kind != api.RestrictionAccountPastDue {
		t.Fatalf("restrictions = %+v", health.Restrictions)
	}
	if len(health.FailedDeployments) != 1 || health.FailedDeployments[0].DeploymentID != failed.ID || health.Attention != 2 {
		t.Fatalf("health = %+v", health)
	}
	if strings.Contains(rec.Body.String(), "egress_fanout") || strings.Contains(rec.Body.String(), "rival") {
		t.Fatalf("leaked internal reason or another account: %s", rec.Body.String())
	}

	req := httptest.NewRequest("GET", "/dashboard/account/health", nil)
	req.AddCookie(customerSessionCookie(t, e, e.acct.ID))
	page := httptest.NewRecorder()
	e.h.ServeHTTP(page, req)
	if page.Code != http.StatusOK {
		t.Fatalf("dashboard status=%d body=%s", page.Code, page.Body.String())
	}
	body := page.Body.String()
	for _, want := range []string{"Account health", "A payment failed", "build failed: npm ci exited 1",
		"/dashboard/apps/shop/deployments/" + failed.ID, "/dashboard/support?app_id=" + app.ID + "&amp;deployment_id=" + failed.ID, "No API requests were refused."} {
		if !strings.Contains(body, want) {
			t.Errorf("dashboard missing %q", want)
		}
	}
	if strings.Contains(body, "egress_fanout") {
		t.Fatal("dashboard leaked the abuse-hold reason")
	}

	req = httptest.NewRequest("GET", "/dashboard/", nil)
	req.AddCookie(customerSessionCookie(t, e, e.acct.ID))
	overview := httptest.NewRecorder()
	e.h.ServeHTTP(overview, req)
	if !strings.Contains(overview.Body.String(), "1 thing needs your attention") {
		t.Fatalf("overview banner missing: %d", overview.Code)
	}
}

func TestEncodeTicketSnapshotStaysWithinLimit(t *testing.T) {
	snapshot := supportportal.TicketSnapshot{CapturedAt: time.Now(), Plan: "pro", Status: "active"}
	for i := 0; i < 20; i++ {
		snapshot.Health.RefusedRequests.Recent = append(snapshot.Health.RefusedRequests.Recent,
			api.AccountHealthRequest{Route: strings.Repeat("r", 200), Code: "plan_excess", RequestID: strings.Repeat("q", 128)})
	}
	for i := 0; i < 10; i++ {
		snapshot.Health.FailedDeployments = append(snapshot.Health.FailedDeployments,
			api.AccountHealthDeployment{DeploymentID: "d", Error: strings.Repeat("e", 2500), Hint: strings.Repeat("h", 2500)})
	}
	raw, err := encodeTicketSnapshot(snapshot)
	if err != nil || len(raw) > support.SnapshotMaxBytes {
		t.Fatalf("len=%d err=%v", len(raw), err)
	}
	var decoded supportportal.TicketSnapshot
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatal(err)
	}
	if len(decoded.Health.RefusedRequests.Recent) != 0 || len(decoded.Health.FailedDeployments) != 10 || decoded.Health.FailedDeployments[0].Error != "" {
		t.Fatalf("trim order wrong: recent=%d deployments=%+v", len(decoded.Health.RefusedRequests.Recent), decoded.Health.FailedDeployments[0])
	}
	small, err := encodeTicketSnapshot(supportportal.TicketSnapshot{Plan: "pro", Health: api.AccountHealth{FailedDeployments: []api.AccountHealthDeployment{{Error: "kept"}}}})
	if err != nil || !strings.Contains(string(small), "kept") {
		t.Fatalf("small snapshot trimmed: %s %v", small, err)
	}
}

func TestRestrictionChanges(t *testing.T) {
	r := func(kinds ...string) []api.AccountHealthRestriction {
		out := []api.AccountHealthRestriction{}
		for _, k := range kinds {
			out = append(out, api.AccountHealthRestriction{Kind: k})
		}
		return out
	}
	lifted, added := restrictionChanges(r(api.RestrictionAccountPastDue, api.RestrictionEmailUnverified), r(api.RestrictionEmailUnverified, api.RestrictionAbuseHold))
	if len(lifted) != 1 || lifted[0] != api.RestrictionAccountPastDue || len(added) != 1 || added[0] != api.RestrictionAbuseHold {
		t.Fatalf("lifted=%v added=%v", lifted, added)
	}
	if lifted, added := restrictionChanges(r(), r()); lifted != nil || added != nil {
		t.Fatalf("no change reported %v %v", lifted, added)
	}
}

func TestCreateReceiptWriterPassesThroughAndKeepsReceipt(t *testing.T) {
	inner := httptest.NewRecorder()
	w := &createReceiptWriter{ResponseWriter: inner}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_, _ = w.Write([]byte(`{"ticket_id":"t-1",`))
	_, _ = w.Write([]byte(`"revision":1}` + strings.Repeat(" ", 5000)))
	if inner.Code != http.StatusCreated || inner.Body.Len() != len(`{"ticket_id":"t-1","revision":1}`)+5000 {
		t.Fatalf("response altered: %d %d", inner.Code, inner.Body.Len())
	}
	if w.status != http.StatusCreated || w.body.Len() != 4096 || !strings.HasPrefix(w.body.String(), `{"ticket_id":"t-1","revision":1}`) {
		t.Fatalf("receipt not kept: %d %d", w.status, w.body.Len())
	}
}
