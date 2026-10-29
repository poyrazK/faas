package apidserver

import (
	"io"
	"log/slog"
	"net/http"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/managedpostgres"
	"github.com/onebox-faas/faas/pkg/session"
	"github.com/onebox-faas/faas/pkg/state"
)

const DashboardPostgresOverageCSRFCookie = dashboardPostgresOverageCSRFCookie

// NewManagedPostgresDashboardServer is a dashboard handler with a stepped-up
// session for a new account on plan. install puts a managed PostgreSQL
// service on the server.
func NewManagedPostgresDashboardServer(t *testing.T, plan api.Plan) (http.Handler, *http.Cookie, state.Account, func(*managedpostgres.Service)) {
	t.Helper()
	store := state.NewMemStore()
	acct, err := store.CreateAccount(t.Context(), "overage@example.com", plan)
	if err != nil {
		t.Fatalf("seed account: %v", err)
	}
	mgr, err := session.NewEphemeralManager(sessionCookieLifetime)
	if err != nil {
		t.Fatalf("session manager: %v", err)
	}
	const sid = "overage-sid"
	if _, err := store.CreateSession(t.Context(), sid, acct.ID, "192.0.2.10", "overage-ua"); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	stepped, err := mgr.IssueWithSessionAndBindingHashAndStepUp(sid, acct.ID, "", time.Now(), false)
	if err != nil {
		t.Fatalf("issue stepped-up cookie: %v", err)
	}
	srv := newServerWithDeps(store, slog.New(slog.NewTextHandler(io.Discard, nil)),
		"gregale.dev", noopNotifier{}, "", noopMailer{}, stubGithubdClient{}, mgr, nil, 15*time.Minute, "")
	install := func(service *managedpostgres.Service) { srv.managedPostgres = service }
	return srv.handler(), &http.Cookie{Name: sessionCookie, Value: stepped}, acct, install
}
