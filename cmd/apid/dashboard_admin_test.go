package main

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/session"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestDashboardAdmin_TLSCutoverBannerPersistsAfterRollback(t *testing.T) {
	store := state.NewMemStore()
	acct, err := store.CreateAccount(t.Context(), "ops@example.com", "free")
	if err != nil {
		t.Fatal(err)
	}
	mgr, err := session.NewEphemeralManager(sessionCookieLifetime)
	if err != nil {
		t.Fatal(err)
	}
	value, err := mgr.Issue(acct.ID)
	if err != nil {
		t.Fatal(err)
	}
	srv := newServerWithDeps(store, slog.New(slog.NewTextHandler(io.Discard, nil)), "gregale.dev", noopNotifier{}, "", noopMailer{}, stubGithubdClient{}, mgr, nil, 15*60_000_000_000, "")
	srv.WithAdminAllowlist(acct.Email)

	statePath := filepath.Join(t.TempDir(), "tls-cutover.state")
	if err := os.WriteFile(statePath, []byte("state=rolled_back\nrun_id=run-1\nupdated_at=2026-09-06T12:01:00Z\noperator=ops@example.com\nmessage=rollback verified\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FAAS_TLS_CUTOVER_STATE_FILE", statePath)

	req := httptest.NewRequest(http.MethodGet, "/dashboard/admin", nil)
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: value})
	rec := httptest.NewRecorder()
	srv.handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	for _, want := range []string{"TLS cutover drill rolled back successfully", "run-1", "ops@example.com"} {
		if !strings.Contains(rec.Body.String(), want) {
			t.Errorf("body missing %q: %s", want, rec.Body.String())
		}
	}
}
