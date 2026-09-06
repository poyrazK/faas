package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestPasswordSignupSendsSingleUseVerificationLink(t *testing.T) {
	srv, mailer, store := v1AuthTestHarness(t)
	h := srv.handler()
	rec := v1AuthJSONRequest(t, h, "/v1/auth/signup",
		`{"email":"verify-me@example.com","password":"correct-horse-battery-staple"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("signup status = %d: %s", rec.Code, rec.Body.String())
	}
	acct, err := store.AccountByEmail(context.Background(), "verify-me@example.com")
	if err != nil {
		t.Fatal(err)
	}
	if acct.EmailVerified() {
		t.Fatal("password signup created a verified account")
	}
	msgs := mailer.snapshot()
	if len(msgs) != 1 {
		t.Fatalf("verification emails = %d, want 1", len(msgs))
	}
	const marker = "/v1/auth/verify-email?token="
	i := strings.Index(msgs[0].TextBody, marker)
	if i < 0 {
		t.Fatalf("verification email missing link: %q", msgs[0].TextBody)
	}
	token := strings.Fields(msgs[0].TextBody[i+len(marker):])[0]

	verifyReq := httptest.NewRequest(http.MethodGet, emailVerificationPath+"?token="+token, nil)
	verifyRec := httptest.NewRecorder()
	h.ServeHTTP(verifyRec, verifyReq)
	if verifyRec.Code != http.StatusOK {
		t.Fatalf("verify status = %d: %s", verifyRec.Code, verifyRec.Body.String())
	}
	acct, err = store.AccountByEmail(context.Background(), "verify-me@example.com")
	if err != nil {
		t.Fatal(err)
	}
	if !acct.EmailVerified() {
		t.Fatal("verification link did not verify account")
	}

	replayReq := httptest.NewRequest(http.MethodGet, emailVerificationPath+"?token="+token, nil)
	replayRec := httptest.NewRecorder()
	h.ServeHTTP(replayRec, replayReq)
	if replayRec.Code != http.StatusGone {
		t.Fatalf("replay status = %d, want 410", replayRec.Code)
	}
}

// TestUnverifiedAccountCannotReachDeployOrBilling_Property exercises the
// authorization invariant over every customer route that can publish code or
// touch payment state. Handler bodies must remain unreachable for any
// unverified account, regardless of which valid bearer key it presents.
func TestUnverifiedAccountCannotReachDeployOrBilling_Property(t *testing.T) {
	for n := 0; n < 12; n++ {
		store := state.NewMemStore()
		res, err := store.CreateAccountWithPersonalOrg(context.Background(), state.CreateAccountWithPersonalOrgParams{
			Email:                    fmt.Sprintf("unverified-%d@example.com", n),
			Plan:                     api.PlanFree,
			RequireEmailVerification: true,
		})
		if err != nil {
			t.Fatal(err)
		}
		plaintext, hash, err := api.GenerateAPIKey()
		if err != nil {
			t.Fatal(err)
		}
		if _, err := store.CreateAPIKey(context.Background(), res.Account.ID, hash, "verification-property", api.ScopesAdminOnly); err != nil {
			t.Fatal(err)
		}
		srv := newServer(store, slog.New(slog.NewTextHandler(io.Discard, nil)), "gregale.dev", noopNotifier{})
		h := srv.handler()
		cases := []struct {
			method string
			path   string
			body   string
		}{
			{http.MethodPost, "/v1/apps", `{"slug":"blocked-app","type":"app","ram_mb":128,"max_concurrency":1}`},
			{http.MethodPost, "/v1/apps/blocked-app/deployments", `{}`},
			{http.MethodPost, "/v1/apps/blocked-app/deployments/dev-source", ""},
			{http.MethodPost, "/v1/apps/blocked-app/deployments/source-ref", `{}`},
			{http.MethodPost, "/v1/apps/blocked-app/deployments/source-tarball", ""},
			{http.MethodPatch, "/v1/account/plan", `{"plan":"hobby"}`},
			{http.MethodGet, "/v1/billing/portal", ""},
			{http.MethodPost, "/v1/billing/retry", ""},
			{http.MethodPost, "/v1/billing/cancel", ""},
		}
		for _, tc := range cases {
			req := httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body))
			req.Header.Set("Authorization", "Bearer "+plaintext)
			req.Header.Set("Content-Type", "application/json")
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			if rec.Code != http.StatusForbidden {
				t.Fatalf("iteration %d %s %s status = %d: %s", n, tc.method, tc.path, rec.Code, rec.Body.String())
			}
			var problem api.Problem
			if err := json.Unmarshal(rec.Body.Bytes(), &problem); err != nil {
				t.Fatal(err)
			}
			if problem.Code != api.CodeEmailVerificationRequired {
				t.Fatalf("iteration %d %s %s code = %q", n, tc.method, tc.path, problem.Code)
			}
		}
		if count, err := store.CountDeployedApps(context.Background(), res.Account.ID); err != nil || count != 0 {
			t.Fatalf("iteration %d deployed apps = %d, err=%v", n, count, err)
		}
	}
}

func TestForgotPasswordSkipsUnverifiedAccount(t *testing.T) {
	store := state.NewMemStore()
	_, err := store.CreateAccountWithPersonalOrg(context.Background(), state.CreateAccountWithPersonalOrgParams{
		Email:                    "unverified-reset@example.com",
		Plan:                     api.PlanFree,
		RequireEmailVerification: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	mailer := &recordingMailer{}
	srv := newServerWithDeps(store, slog.New(slog.NewTextHandler(io.Discard, nil)),
		"example.com", noopNotifier{}, "", mailer, nil, nil, nil, 0, "")
	req := httptest.NewRequest(http.MethodPost, "/login/forgot", strings.NewReader("email=unverified-reset%40example.com"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	srv.handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("forgot status = %d", rec.Code)
	}
	if got := len(mailer.snapshot()); got != 0 {
		t.Fatalf("password reset emails = %d, want 0", got)
	}
}

func TestOAuthProofVerifiesExistingPasswordAccount(t *testing.T) {
	store := state.NewMemStore()
	res, err := store.CreateAccountWithPersonalOrg(context.Background(), state.CreateAccountWithPersonalOrgParams{
		Email:                    "oauth-proof@example.com",
		Plan:                     api.PlanFree,
		RequireEmailVerification: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	srv := newServer(store, slog.New(slog.NewTextHandler(io.Discard, nil)), "gregale.dev", noopNotifier{})
	acct, err := srv.verifyOAuthAccountEmail(context.Background(), res.Account)
	if err != nil {
		t.Fatal(err)
	}
	if !acct.EmailVerified() {
		t.Fatal("OAuth email proof did not verify existing account")
	}
	persisted, err := store.AccountByID(context.Background(), res.Account.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !persisted.EmailVerified() {
		t.Fatal("OAuth email proof was not persisted")
	}
}
