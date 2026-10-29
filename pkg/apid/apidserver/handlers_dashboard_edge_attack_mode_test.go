package apidserver_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/middleware"
	"github.com/onebox-faas/faas/pkg/platformflags"
	"github.com/onebox-faas/faas/pkg/state"
)

// adr: 20261021220456 — the Edge protection panel switches attack mode on
// for the chosen time and off again through the API handlers, on the Free
// plan too; the rules table marks the managed rule and offers no edit forms
// for it.
func TestDashboardEdgeAttackMode(t *testing.T) {
	_, cookie, store, sessions := apidserver.NewAuthedDashboardServerFull(t)
	srv := apidserver.NewServerWithDeps(store, apidserver.DiscardLogger(), "gregale.dev", apidserver.NoopNotifier{}, "", apidserver.NoopMailer{}, apidserver.StubGithubdClient{}, sessions, nil, 15*time.Minute, "")
	h := srv.Handler()
	acct, err := store.AccountByEmail(t.Context(), "alice@example.com")
	if err != nil {
		t.Fatalf("AccountByEmail: %v", err)
	}
	app, err := store.CreateApp(t.Context(), state.App{AccountID: acct.ID, Slug: "attack-app", Type: state.AppTypeApp, Runtime: "node22", Status: state.AppActive})
	if err != nil {
		t.Fatalf("CreateApp: %v", err)
	}
	token, err := middleware.IssueForAuthenticatedNamed(sessions, apidserver.DashboardEdgeRulesAction, acct.ID, apidserver.DashboardEdgeRulesCSRFCookie)
	if err != nil {
		t.Fatalf("IssueForAuthenticatedNamed: %v", err)
	}
	csrf := &http.Cookie{Name: apidserver.DashboardEdgeRulesCSRFCookie, Value: token}
	post := func(form map[string]string) *httptest.ResponseRecorder {
		t.Helper()
		form[middleware.FormFieldName] = token
		return apidserver.DashboardPOST(t, h, cookie, "/dashboard/apps/attack-app/edge-protection/attack-mode", form, csrf)
	}
	page := func() string {
		t.Helper()
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/dashboard/apps/attack-app/edge-rules", nil)
		req.AddCookie(cookie)
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("page status = %d", rec.Code)
		}
		return rec.Body.String()
	}

	// adr: 20261021220456 — amendment 1: until attack mode is released the
	// card offers no switch, and the form is refused.
	if body := page(); !strings.Contains(body, "Attack mode is not yet available") || strings.Contains(body, "Turn on attack mode") {
		t.Fatal("unreleased page offers to turn attack mode on")
	}
	if rec := post(map[string]string{"attack_mode": "on", "duration_seconds": "3600"}); rec.Code != http.StatusUnprocessableEntity || !strings.Contains(rec.Body.String(), "edge_feature_unavailable") {
		t.Fatalf("unreleased on: %d %s %s", rec.Code, rec.Header().Get("Location"), rec.Body.String())
	}
	if rules, _ := store.ListEdgeRulesForApp(t.Context(), app.ID); len(rules) != 0 {
		t.Fatalf("unreleased on created %d rules", len(rules))
	}
	for _, flag := range []string{platformflags.EdgeAttackMode, platformflags.EdgeChallenge} {
		srv.SeedPlatformFlag(flag, platformflags.Definition{Enabled: true, Default: true})
	}

	if body := page(); !strings.Contains(body, "Turn on attack mode") || strings.Contains(body, "Turn off attack mode") {
		t.Fatal("off page does not offer to turn attack mode on")
	}
	if rec := post(map[string]string{"attack_mode": "maybe"}); rec.Code != http.StatusSeeOther || !strings.Contains(rec.Header().Get("Location"), "action=error") {
		t.Fatalf("bad form: %d %s", rec.Code, rec.Header().Get("Location"))
	}

	rec := post(map[string]string{"attack_mode": "on", "duration_seconds": "21600"})
	if rec.Code != http.StatusSeeOther || !strings.Contains(rec.Header().Get("Location"), "action=attack_mode_on") {
		t.Fatalf("on: %d %s %s", rec.Code, rec.Header().Get("Location"), rec.Body.String())
	}
	rules, err := store.ListEdgeRulesForApp(t.Context(), app.ID)
	if err != nil || len(rules) != 1 || !rules[0].AttackMode() || rules[0].ExpiresAt == nil ||
		time.Until(*rules[0].ExpiresAt) < 5*time.Hour {
		t.Fatalf("rules after on = %+v, %v; want one six-hour attack-mode rule", rules, err)
	}
	body := page()
	for _, want := range []string{"Attack mode is on", "Extend attack mode", "Turn off attack mode", "managed by attack mode", `href="#attack-mode"`} {
		if !strings.Contains(body, want) {
			t.Errorf("on page missing %q", want)
		}
	}
	if strings.Contains(body, "/edge-rules/"+rules[0].ID+"/mode") {
		t.Error("on page offers a mode form for the attack-mode rule")
	}

	if rec := post(map[string]string{"attack_mode": "off"}); rec.Code != http.StatusSeeOther || !strings.Contains(rec.Header().Get("Location"), "action=attack_mode_off") {
		t.Fatalf("off: %d %s %s", rec.Code, rec.Header().Get("Location"), rec.Body.String())
	}
	if rules, err := store.ListEdgeRulesForApp(t.Context(), app.ID); err != nil || len(rules) != 0 {
		t.Fatalf("rules after off = %d, %v", len(rules), err)
	}
}
