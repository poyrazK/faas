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
	"github.com/onebox-faas/faas/pkg/apid/sales"
	"github.com/onebox-faas/faas/pkg/referrals"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestSalesRoutesDefaultOff(t *testing.T) {
	srv := apidserver.NewServer(state.NewMemStore(), slog.New(slog.NewTextHandler(io.Discard, nil)), "gregale.dev", apidserver.NoopNotifier{})
	h := srv.Handler()
	for _, path := range []string{"/v1/sales/templates", "/v1/sales/templates/11111111-1111-1111-1111-111111111111", "/v1/sales/leads/11111111-1111-1111-1111-111111111111/templates/11111111-1111-1111-1111-111111111111/preview?version=1", "/v1/sales/qualification", "/v1/sales/leads/11111111-1111-1111-1111-111111111111/qualification", "/v1/sales/alerts", "/v1/sales/leads/11111111-1111-1111-1111-111111111111/activities", "/v1/sales/inquiries", "/v1/sales/inquiries/challenge", "/v1/sales/reports", "/dashboard/sales", "/dashboard/referrals", "/v1/sales/me", "/v1/sales/team", "/v1/sales/leads", "/v1/sales/tasks", "/v1/referrals/me", "/v1/referrals/selection", "/dashboard/referral-assets/portal.js"} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("GET", "https://sales.gregale.dev"+path, nil))
		if w.Code != 404 {
			t.Fatalf("%s: %d %s", path, w.Code, w.Body.String())
		}
	}
}

func TestSalesRejectsKeysAndUnenrolledSessions(t *testing.T) {
	e := apidserver.NewObsEnv(t, api.ScopesAdminOnly, "operator@example.com", "rep@example.com")
	(*e.S()).WithSales(sales.Config{Enabled: true, Members: map[string]bool{e.Acct().Email: true}}, referrals.NewPostgresStore(nil))
	for _, path := range []string{"/v1/sales/templates", "/v1/sales/templates/11111111-1111-1111-1111-111111111111", "/v1/sales/leads/11111111-1111-1111-1111-111111111111/templates/11111111-1111-1111-1111-111111111111/preview?version=1", "/v1/sales/qualification", "/v1/sales/leads/11111111-1111-1111-1111-111111111111/qualification", "/v1/sales/alerts", "/v1/sales/leads/11111111-1111-1111-1111-111111111111/activities", "/v1/sales/inquiries", "/v1/sales/reports", "/v1/sales/me", "/v1/sales/team", "/v1/sales/leads", "/v1/sales/tasks", "/v1/referrals/me"} {
		w := e.Do(t, "GET", path, nil, nil)
		if w.Code != http.StatusForbidden {
			t.Fatalf("key read %s: %d %s", path, w.Code, w.Body.String())
		}
	}
	for _, write := range []struct{ method, path string }{{"POST", "/v1/sales/templates"}, {"PUT", "/v1/sales/templates/11111111-1111-1111-1111-111111111111"}} {
		w := e.Do(t, write.method, write.path, strings.NewReader(`{}`), nil)
		if w.Code != http.StatusForbidden {
			t.Fatalf("key write %s: %d %s", write.path, w.Code, w.Body.String())
		}
	}
	w := e.DoSession(t, "", "GET", "/v1/sales/me", nil, nil)
	if w.Code != http.StatusForbidden {
		t.Fatalf("unenrolled session read staff data: %d %s", w.Code, w.Body.String())
	}
}

func TestSalesIntakeAnonymousOriginAndSeparateLimit(t *testing.T) {
	srv := apidserver.NewServer(state.NewMemStore(), slog.New(slog.NewTextHandler(io.Discard, nil)), "gregale.dev", apidserver.NoopNotifier{})
	srv.WithSales(sales.Config{Enabled: true, IntakeEnabled: true}, referrals.NewPostgresStore(nil))
	h := srv.Handler()
	send := func(origin, body string) int {
		r := httptest.NewRequest("POST", "https://api.gregale.dev/v1/sales/inquiries", strings.NewReader(body))
		r.Header.Set("Origin", origin)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w.Code
	}
	if code := send("https://customer.gregale.dev", `{"website":"spam"}`); code != 403 {
		t.Fatal("untrusted origin", code)
	}
	if code := send("https://gregale.dev", `{"website":"spam"}`); code != 202 {
		t.Fatal("public form required authentication", code)
	}
	for range api.SalesIntakeRequestsPerMinute - 2 {
		if code := send("https://gregale.dev", `{}`); code != 400 {
			t.Fatal("unexpected form response", code)
		}
	}
	if code := send("https://gregale.dev", `{}`); code != 429 {
		t.Fatal("intake did not throttle", code)
	}
	// The public form bucket must not exhaust the authenticated API/login budget.
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "https://api.gregale.dev/v1/sales/inquiries", nil))
	if w.Code == 429 || w.Code == 200 {
		t.Fatal("private inbox auth or independent rate limit failed", w.Code)
	}
}
