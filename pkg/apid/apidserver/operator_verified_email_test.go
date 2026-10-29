package apidserver_test

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
)

// Operator status was "the email is in FAAS_ADMIN_EMAILS", verified or not.
// Anyone who signed up with an allowlisted address nobody had claimed held
// operator access (bug hunt 2026-10-27 H24).
func TestUnverifiedAllowlistedAccountIsNotAnOperator(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	res, err := store.CreateAccountWithPersonalOrg(ctx, state.CreateAccountWithPersonalOrgParams{
		Email: "ops-alias@gregale.dev", Plan: api.PlanPro, RequireEmailVerification: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Account.EmailVerified() {
		t.Fatal("fixture: account should be unverified")
	}
	key, hash, err := api.GenerateAPIKey()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateAPIKey(ctx, res.Account.ID, hash, "probe", api.ScopesAdminOnly); err != nil {
		t.Fatal(err)
	}
	srv := apidserver.NewServer(store, slog.New(slog.NewTextHandler(io.Discard, nil)), "gregale.dev", apidserver.NoopNotifier{})
	srv.WithAdminAllowlist("ops-alias@gregale.dev")
	req := httptest.NewRequest(http.MethodGet, "/v1/admin/obs/capacity", nil)
	req.Header.Set("Authorization", "Bearer "+key)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("unverified allowlisted account on an operator route: %d %s, want 403", rec.Code, rec.Body)
	}
}
