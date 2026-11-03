package apidserver_test

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
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
	for _, path := range []string{"/v1/sales/reports", "/dashboard/sales", "/dashboard/referrals", "/v1/sales/me", "/v1/sales/team", "/v1/sales/leads", "/v1/sales/tasks", "/v1/referrals/me", "/v1/referrals/selection", "/dashboard/referral-assets/portal.js"} {
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
	for _, path := range []string{"/v1/sales/reports", "/v1/sales/me", "/v1/sales/team", "/v1/sales/leads", "/v1/sales/tasks", "/v1/referrals/me"} {
		w := e.Do(t, "GET", path, nil, nil)
		if w.Code != http.StatusForbidden {
			t.Fatalf("key read %s: %d %s", path, w.Code, w.Body.String())
		}
	}
	w := e.DoSession(t, "", "GET", "/v1/sales/me", nil, nil)
	if w.Code != http.StatusForbidden {
		t.Fatalf("unenrolled session read staff data: %d %s", w.Code, w.Body.String())
	}
}
