package apidserver_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/apid/apidserver"
)

// ADR 20261022145950: the AI gateway's pages are served behind the
// dashboard session, and a signed-in account sees either the pages or why
// the AI gateway is not available to it.
func TestDashboardAIConsoleIsServedBehindTheSession(t *testing.T) {
	h, cookie, _, _ := apidserver.NewAuthedDashboardServerFull(t)
	for _, path := range []string{"/dashboard/ai", "/dashboard/ai/"} {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.AddCookie(cookie)
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "<h1>AI gateway</h1>") {
			t.Fatalf("%s = %d\n%s", path, rec.Code, rec.Body.String())
		}
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/dashboard/ai", nil))
	if rec.Code < 300 || rec.Code >= 400 || !strings.Contains(rec.Header().Get("Location"), "login") {
		t.Fatalf("signed out = %d %v", rec.Code, rec.Header())
	}
}
