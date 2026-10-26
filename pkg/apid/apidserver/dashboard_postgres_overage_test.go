// adr: 20261025094915 — the dashboard's compute overage form is the same
// change as PUT /v1/postgres/overage, behind its own named CSRF action.
package apidserver_test

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
)

var overageTokenPattern = regexp.MustCompile(`action="/dashboard/postgres/overage">\s*<input type="hidden" name="csrf_token" value="([^"]+)"`)

func renderPostgresDashboard(t *testing.T, h http.Handler, sid *http.Cookie, path string) (string, *http.Cookie) {
	t.Helper()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	req.AddCookie(sid)
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET %s = %d\n%s", path, rec.Code, rec.Body.String())
	}
	for _, cookie := range rec.Result().Cookies() {
		if cookie.Name == apidserver.DashboardPostgresOverageCSRFCookie {
			return rec.Body.String(), cookie
		}
	}
	return rec.Body.String(), nil
}

func postOverage(t *testing.T, h http.Handler, sid, csrf *http.Cookie, token, choice string) *httptest.ResponseRecorder {
	t.Helper()
	form := url.Values{"csrf_token": {token}, "enabled": {choice}}
	req := httptest.NewRequest(http.MethodPost, "/dashboard/postgres/overage", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(sid)
	if csrf != nil {
		req.AddCookie(csrf)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestDashboardPostgresOverageForm(t *testing.T) {
	h, sid, acct, install := apidserver.NewManagedPostgresDashboardServer(t, api.PlanPro)
	service, _, _ := resizeAPIService(t, acct.ID)
	install(service)

	body, csrf := renderPostgresDashboard(t, h, sid, "/dashboard/postgres")
	match := overageTokenPattern.FindStringSubmatch(body)
	if csrf == nil || match == nil || match[1] != csrf.Value || !strings.Contains(body, "Overage is <strong>off</strong>") || !strings.Contains(body, "75 compute-unit hours") {
		t.Fatalf("overage form missing or unbound:\n%s", body)
	}
	if rec := postOverage(t, h, sid, csrf, "wrong", "on"); rec.Code != http.StatusBadRequest {
		t.Fatalf("bad csrf = %d, want 400", rec.Code)
	}
	rec := postOverage(t, h, sid, csrf, match[1], "on")
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/dashboard/postgres?overage=on" {
		t.Fatalf("turn on = %d %q\n%s", rec.Code, rec.Header().Get("Location"), rec.Body.String())
	}
	if on, err := service.Overage(t.Context(), acct.ID); err != nil || !on {
		t.Fatalf("stored overage = %v, %v; want on", on, err)
	}
	body, _ = renderPostgresDashboard(t, h, sid, "/dashboard/postgres?overage=on")
	if !strings.Contains(body, "Compute overage is on.") || !strings.Contains(body, "150 compute-unit hours") || !strings.Contains(body, "Turn overage off") {
		t.Fatalf("page after turning overage on:\n%s", body)
	}
}

func TestDashboardPostgresOverageFormIsAbsentOnFree(t *testing.T) {
	h, sid, acct, install := apidserver.NewManagedPostgresDashboardServer(t, api.PlanFree)
	service, _, _ := resizeAPIService(t, acct.ID)
	install(service)
	body, csrf := renderPostgresDashboard(t, h, sid, "/dashboard/postgres")
	if csrf != nil || strings.Contains(body, "Compute overage") {
		t.Fatalf("free plan shows the overage form:\n%s", body)
	}
}
