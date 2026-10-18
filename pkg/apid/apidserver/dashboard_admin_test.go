package apidserver_test

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/session"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestDashboardAdmin_TLSCutoverBannerPersistsAfterRollback(t *testing.T) {
	store := state.NewMemStore()
	acct, err := store.CreateAccount(t.Context(), "ops@example.com", "free")
	if err != nil {
		t.Fatal(err)
	}
	mgr, err := session.NewEphemeralManager(apidserver.SessionCookieLifetime)
	if err != nil {
		t.Fatal(err)
	}
	value, err := apidserver.MintDashboardSession(t.Context(), store, mgr, acct.ID)
	if err != nil {
		t.Fatal(err)
	}
	srv := apidserver.NewServerWithDeps(store, slog.New(slog.NewTextHandler(io.Discard, nil)), "gregale.dev", apidserver.NoopNotifier{}, "", apidserver.NoopMailer{}, apidserver.StubGithubdClient{}, mgr, nil, 15*60_000_000_000, "")
	srv.WithAdminAllowlist(acct.Email)

	statePath := filepath.Join(t.TempDir(), "tls-cutover.state")
	if err := os.WriteFile(statePath, []byte("state=rolled_back\nrun_id=run-1\nupdated_at=2026-09-06T12:01:00Z\noperator=ops@example.com\nmessage=rollback verified\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FAAS_TLS_CUTOVER_STATE_FILE", statePath)

	req := httptest.NewRequest(http.MethodGet, "/dashboard/admin", nil)
	req.AddCookie(&http.Cookie{Name: apidserver.SessionCookie, Value: value})
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("operator health page is cacheable")
	}
	for _, want := range []string{"TLS cutover drill rolled back successfully", "run-1", "ops@example.com", "Data repair health", "0 held slots", "Recovery runbook"} {
		if !strings.Contains(rec.Body.String(), want) {
			t.Errorf("body missing %q: %s", want, rec.Body.String())
		}
	}
}
