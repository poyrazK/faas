package apidserver_test

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
)

// Each `gregale login` mints a 30-day full-access key; an account keeps only
// the newest api.CLILoginKeysMax of them (F11-2, ADR-20261114120000).
func TestCLILoginKeysAreCapped(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	acct, err := store.CreateAccount(ctx, "cli-cap@example.com", api.PlanHobby)
	if err != nil {
		t.Fatal(err)
	}
	h := apidserver.NewServer(store, slog.New(slog.NewTextHandler(io.Discard, nil)), "gregale.dev", apidserver.NoopNotifier{}).Handler()
	post := func(i int, path string, body any) *httptest.ResponseRecorder {
		raw, _ := json.Marshal(body)
		req := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(raw))
		req.Header.Set("Content-Type", "application/json")
		req.RemoteAddr = fmt.Sprintf("192.0.2.%d:1234", 10+i)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec
	}
	logins := api.CLILoginKeysMax + 2
	var last string
	for i := range logins {
		rec := post(i, "/v1/cli-auth/code", api.CliAuthCodeRequest{Device: true})
		var minted api.CliAuthCodeResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &minted); err != nil || rec.Code != http.StatusOK {
			t.Fatalf("login %d mint: %d %s", i, rec.Code, rec.Body)
		}
		short, err := hex.DecodeString(strings.ReplaceAll(minted.Code, "-", ""))
		if err != nil {
			t.Fatal(err)
		}
		if err := store.ClaimCliAuthCode(ctx, api.HashToken(short), acct.ID); err != nil {
			t.Fatalf("login %d approve: %v", i, err)
		}
		rec = post(i, "/v1/cli-auth/exchange", api.CliAuthExchangeRequest{DeviceCode: minted.DeviceCode})
		var out api.CliAuthExchangeResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil || rec.Code != http.StatusOK {
			t.Fatalf("login %d exchange: %d %s", i, rec.Code, rec.Body)
		}
		last = out.Plaintext
	}
	keys, err := store.ListAPIKeys(ctx, acct.ID)
	if err != nil {
		t.Fatal(err)
	}
	live := 0
	var newest state.APIKey
	for _, k := range keys {
		if k.Label != "cli-login" {
			continue
		}
		if k.Status != "revoked" {
			live++
		}
		if k.CreatedAt.After(newest.CreatedAt) || newest.ID == "" {
			newest = k
		}
	}
	if live != api.CLILoginKeysMax {
		t.Fatalf("live cli-login keys after %d logins = %d, want %d", logins, live, api.CLILoginKeysMax)
	}
	if newest.Status == "revoked" || last == "" {
		t.Fatalf("the newest login's key was revoked: %+v", newest)
	}
}

// Rotating a leaked key can revoke the old one at once (F11-3); a grace
// window outside 0..APIKeyGraceWindowMaxDays is refused.
func TestRotateKeyGraceWindowOverride(t *testing.T) {
	e := apidserver.Setup(t, api.PlanHobby)
	rec := e.Do(t, http.MethodPost, "/v1/keys", api.CreateKeyRequest{Label: "leaked", Scopes: []string{api.ScopeAppsRead}}, nil)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", rec.Code, rec.Body)
	}
	var created api.APIKeyResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	if rec := e.Do(t, http.MethodPost, "/v1/keys/"+created.ID+"/rotate",
		api.RotateKeyRequest{GraceWindowDays: new(api.APIKeyGraceWindowMaxDays + 1)}, nil); rec.Code != http.StatusBadRequest {
		t.Fatalf("grace beyond the cap = %d %s, want 400", rec.Code, rec.Body)
	}
	rec = e.Do(t, http.MethodPost, "/v1/keys/"+created.ID+"/rotate", api.RotateKeyRequest{GraceWindowDays: new(0)}, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("rotate with grace 0: %d %s", rec.Code, rec.Body)
	}
	old, err := (*e.Store()).GetAPIKey(t.Context(), e.Acct().ID, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if old.Status != "revoked" {
		t.Fatalf("old key after rotate with grace 0 = %q, want revoked", old.Status)
	}
}

// Billing runs, their schedule and invoice exports can charge the
// customer's own end customers, so a deploy:write key cannot reach them
// (F13-1, ADR-20261114120000).
func TestMoneyMovingRoutesRefuseDeployWriteKeys(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	rec := e.Do(t, http.MethodPost, "/v1/keys", api.CreateKeyRequest{Label: "ci", Scopes: []string{api.ScopeDeployWrite}}, nil)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create deploy key: %d %s", rec.Code, rec.Body)
	}
	var created api.APIKeyResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil || created.Plaintext == "" {
		t.Fatalf("create deploy key response: %s %v", rec.Body, err)
	}
	if _, err := (*e.Store()).CreateApp(t.Context(), state.App{AccountID: e.Acct().ID, Slug: "app1", Type: state.AppTypeApp}); err != nil {
		t.Fatal(err)
	}
	for _, route := range []struct{ method, path string }{
		{http.MethodPost, "/v1/account/billing-runs"},
		{http.MethodPut, "/v1/account/billing-run-schedule"},
		{http.MethodPost, "/v1/account/platform-tenants/t1/usage-statements/s1/invoice"},
		{http.MethodPost, "/v1/apps/app1/consumers/c1/usage-statements/s1/invoice"},
	} {
		rec := e.DoAs(t, route.method, route.path, map[string]any{}, map[string]string{"Idempotency-Key": "k-" + route.path}, created.Plaintext)
		if rec.Code != http.StatusForbidden {
			t.Errorf("%s %s with a deploy:write key = %d %s, want 403", route.method, route.path, rec.Code, rec.Body)
		}
	}
}

// Two keys minted within the same minute both mail the owner, so they learn
// of the second one too (F14-2).
func TestCredentialNoticesAreNotSpacedOut(t *testing.T) {
	srv, mailer, store := apidserver.V1AuthTestHarness(t)
	h := srv.Handler()
	acct, key := flowAccountKey(t, store, "two-keys@example.com", true)
	for _, label := range []string{"first-key", "second-key"} {
		if rec := flowBearer(h, http.MethodPost, "/v1/keys", key, `{"label":"`+label+`","scopes":["apps:read"]}`); rec.Code != http.StatusCreated {
			t.Fatalf("create %s: %d %s", label, rec.Code, rec.Body)
		}
	}
	waitForMail(t, mailer, acct.Email, "first-key")
	waitForMail(t, mailer, acct.Email, "second-key")
}
