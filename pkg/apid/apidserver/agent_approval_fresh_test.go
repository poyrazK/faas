package apidserver_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
)

// Approving an agent's delete (or production, credentials or billing) request
// needs a recent sign-in, like approving a CLI sign-in; a stale session is sent
// to sign in again. Denying needs none (F12-2, ADR-20261115160000).
func TestAgentApprovalNeedsRecentSignInToApprove(t *testing.T) {
	srv, store := apidserver.NewCliAuthTestServer(t)
	ctx := t.Context()
	const email = "fresh-approver@example.com"
	acct, err := store.CreateAccount(ctx, email, api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "prod-svc", Type: state.AppTypeApp}); err != nil {
		t.Fatal(err)
	}
	plaintext, hash, err := api.GenerateAPIKey()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateAPIKey(ctx, acct.ID, hash, "ci-agent", []string{api.ScopeAgent, api.ScopeAppsRead, api.ScopeDeployWrite}); err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodDelete, "/v1/apps/prod-svc", strings.NewReader(""))
	req.Header.Set("Authorization", "Bearer "+plaintext)
	srv.ServeHTTP(rec, req)
	var problem api.Problem
	if rec.Code != http.StatusPreconditionRequired || json.Unmarshal(rec.Body.Bytes(), &problem) != nil || problem.AgentApproval == nil {
		t.Fatalf("agent delete = %d %s, want 428 approval_required", rec.Code, rec.Body)
	}
	id := problem.AgentApproval.ID

	page, cookie, form := apidserver.RenderAgentApprovalForTestAs(t, srv, id, email)
	if page.Code != http.StatusOK || form == "" {
		t.Fatalf("fresh review page = %d, form token %q", page.Code, form)
	}
	apidserver.AgeSignInsForTest(t, 2*api.RecentSignInWindow)
	approve := apidserver.DecideAgentApprovalForTest(t, srv, id, cookie, form, "approve")
	if approve.Code != http.StatusSeeOther || !strings.Contains(approve.Header().Get("Location"), "reauth=1") {
		t.Fatalf("stale approve = %d %q, want a redirect to sign in again", approve.Code, approve.Header().Get("Location"))
	}
	if stale, _, _ := apidserver.RenderAgentApprovalForTestAs(t, srv, id, email); stale.Code != http.StatusSeeOther {
		t.Fatalf("stale review page = %d, want a redirect to sign in again", stale.Code)
	}
	if deny := apidserver.DecideAgentApprovalForTest(t, srv, id, cookie, form, "deny"); deny.Code != http.StatusOK {
		t.Fatalf("stale deny = %d %s, want 200", deny.Code, deny.Body)
	}
}
