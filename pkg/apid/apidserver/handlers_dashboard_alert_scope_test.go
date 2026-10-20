package apidserver_test

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/session"
	"github.com/onebox-faas/faas/pkg/state"
)

// The dashboard router has no operator gate, so /dashboard/alerts/{id} and
// /dashboard/safe-releases must show a customer session only its own
// account's alert rules.
func TestDashboardAlertPagesAreAccountScoped(t *testing.T) {
	store := state.NewMemStore()
	viewer, err := store.CreateAccount(t.Context(), "viewer@example.com", api.PlanPro)
	if err != nil {
		t.Fatalf("CreateAccount viewer: %v", err)
	}
	other, err := store.CreateAccount(t.Context(), "other@example.com", api.PlanPro)
	if err != nil {
		t.Fatalf("CreateAccount other: %v", err)
	}
	own, err := store.CreateAlertRule(t.Context(), state.AlertRule{
		AccountID: viewer.ID, Name: "viewer-canary-rule", Enabled: true,
		Metric: state.AlertMetricCanaryStuckStep, Comparison: state.AlertGt, Threshold: 1,
	})
	if err != nil {
		t.Fatalf("CreateAlertRule own: %v", err)
	}
	foreign, err := store.CreateAlertRule(t.Context(), state.AlertRule{
		AccountID: other.ID, Name: "other-tenant-secret-rule", Enabled: true,
		Metric: state.AlertMetricCanaryStuckStep, Comparison: state.AlertGt, Threshold: 1,
	})
	if err != nil {
		t.Fatalf("CreateAlertRule foreign: %v", err)
	}

	sessions, err := session.NewEphemeralManager(apidserver.SessionCookieLifetime)
	if err != nil {
		t.Fatalf("session manager: %v", err)
	}
	token, err := apidserver.MintDashboardSession(t.Context(), store, sessions, viewer.ID)
	if err != nil {
		t.Fatalf("issue session: %v", err)
	}
	h := apidserver.NewServerWithDeps(store, slog.New(slog.NewTextHandler(io.Discard, nil)), "gregale.dev",
		apidserver.NoopNotifier{}, "", apidserver.NoopMailer{}, apidserver.StubGithubdClient{},
		sessions, nil, 15*60_000_000_000, "").Handler()
	cookie := &http.Cookie{Name: apidserver.SessionCookie, Value: token}

	if rec := scopedDashboardGet(h, "/dashboard/alerts/"+own.ID, cookie); rec.Code != http.StatusOK {
		t.Fatalf("own rule: status %d, want 200", rec.Code)
	}
	rec := scopedDashboardGet(h, "/dashboard/alerts/"+foreign.ID, cookie)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("other account's rule: status %d, want 404", rec.Code)
	}
	if strings.Contains(rec.Body.String(), foreign.Name) {
		t.Fatal("other account's rule name rendered on the 404 page")
	}

	rec = scopedDashboardGet(h, "/dashboard/safe-releases", cookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("safe-releases: status %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, own.Name) {
		t.Fatalf("safe-releases omits the viewer's own rule %q", own.Name)
	}
	if strings.Contains(body, foreign.Name) {
		t.Fatalf("safe-releases shows another account's rule %q", foreign.Name)
	}
}

func scopedDashboardGet(h http.Handler, path string, cookie *http.Cookie) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, path, nil)
	r.AddCookie(cookie)
	h.ServeHTTP(rec, r)
	return rec
}
