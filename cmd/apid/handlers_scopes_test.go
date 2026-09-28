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
	"github.com/onebox-faas/faas/pkg/session"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/wire"

	"github.com/google/uuid"
)

// setupWithScopes is the scope-aware twin of the package-level setup()
// helper in server_test.go. It mints a single API key with the given
// scopes and returns a testEnv that uses it. ACL/auth middleware does
// not care about plan here — the scope check happens after auth, so
// any plan works.
func setupWithScopes(t *testing.T, scopes []string) testEnv {
	t.Helper()
	store := state.NewMemStore()
	acct, err := store.CreateAccount(context.Background(), "scopes@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	pt, hash, err := api.GenerateAPIKey()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateAPIKey(context.Background(), acct.ID, hash, "scopes-test", scopes); err != nil {
		t.Fatal(err)
	}
	ops := wire.NewOpsMetrics("apid_scopes_test")
	srv := newServer(store, slog.New(slog.NewTextHandler(io.Discard, nil)), "gregale.dev", noopNotifier{}).WithOpsMetrics(context.Background(), ops)
	return testEnv{h: srv.handler(), store: store, key: pt, acct: acct, ops: ops}
}

// setupWithSession is the session-cookie twin of setupWithScopes. It
// uses newServerWithDeps so the session.Manager is reachable from
// outside the server (newServer wraps a fresh ephemeral manager that
// can't be issued from the test). The returned testEnv.key is empty —
// callers MUST build their own httptest.NewRequest and attach the
// cookie with req.AddCookie; the bearer-only e.do helper does not
// carry cookies.
//
// IAM-3 (ADR-039): the cookie must carry a sid backed by a live
// sessions row, otherwise requireSessionCookie rejects it with
// CodeSessionExpired. Mirrors setupMW in mfa_middleware_test.go.
func setupWithSession(t *testing.T) (testEnv, *http.Cookie) {
	t.Helper()
	store := state.NewMemStore()
	acct, err := store.CreateAccount(context.Background(),
		"session-cookie@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	mgr, err := session.NewEphemeralManager(sessionCookieLifetime)
	if err != nil {
		t.Fatal(err)
	}
	sid := uuid.NewString()
	if _, err := store.CreateSession(context.Background(), sid, acct.ID,
		"192.0.2.30", "session-test-ua"); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	token, err := mgr.IssueWithSession(sid, acct.ID, false)
	if err != nil {
		t.Fatal(err)
	}
	ops := wire.NewOpsMetrics("apid_session_test")
	srv := newServerWithDeps(store, slog.New(slog.NewTextHandler(io.Discard, nil)),
		"example.com", noopNotifier{}, "", noopMailer{}, stubGithubdClient{}, mgr,
		nil, 15*60_000_000_000, "").WithOpsMetrics(context.Background(), ops)
	return testEnv{h: srv.handler(), store: store, key: "", acct: acct, ops: ops},
		&http.Cookie{Name: sessionCookie, Value: token}
}

// TestScopeMatrix is the IAM-1 (ADR-034 rev2) regression net for the
// per-route scope check. Each row exercises one combination of
// (method, key-scope) against a representative route and asserts the
// expected HTTP status:
//
//   - admin key + allowed op            → 2xx
//   - apps:read key + GET               → 2xx
//   - apps:read key + POST              → 403
//   - deploy:write key + POST           → 2xx
//   - deploy:write key + GET            → 403 (write is not enough for read)
//   - usage:read key + GET /v1/usage    → 2xx
//   - usage:read key + GET /v1/apps     → 403
//   - secrets:write key + PUT secret    → 2xx
//   - secrets:write key + POST deploy   → 403
//   - admin-only route + non-admin      → 403 (compute-nodes,
//     /v1/account/plan, DELETE /v1/account, POST/DELETE /v1/keys)
//   - unknown scope on mint              → 400 validation
//   - empty scope on mint               → defaults to [admin] (legacy behavior)
//
// The fine-grained vocabulary replaces the coarse admin|read|write
// from rev1. See ADR-034 rev2 for the rationale.
func TestScopeMatrix(t *testing.T) {
	t.Run("events-publish-key/cannot-deploy", func(t *testing.T) {
		e := setupWithScopes(t, []string{api.ScopeEventsPublish})
		rec := e.do(t, http.MethodPost, "/v1/events:publish", api.PublishEventRequest{
			ID: "scoped-event", Source: "test", Type: "created", Data: json.RawMessage(`{}`),
		}, nil)
		if rec.Code != http.StatusAccepted {
			t.Fatalf("publish: %d %s", rec.Code, rec.Body.String())
		}
		rec = e.do(t, http.MethodPost, "/v1/apps", api.CreateAppRequest{Slug: "forbidden-deploy"}, nil)
		assertProblem(t, rec, http.StatusForbidden, api.CodeForbidden)
	})
	t.Run("queues-send-key/cannot-publish-event", func(t *testing.T) {
		e := setupWithScopes(t, []string{api.ScopeQueuesSend})
		rec := e.do(t, http.MethodPost, "/v1/events:publish", api.PublishEventRequest{
			ID: "forbidden-event", Source: "test", Type: "created", Data: json.RawMessage(`{}`),
		}, nil)
		assertProblem(t, rec, http.StatusForbidden, api.CodeForbidden)
	})
	t.Run("environment-preflight-scope-is-narrow", func(t *testing.T) {
		e := setupWithScopes(t, []string{api.ScopeProjectEnvironmentRead, api.ScopeProjectEnvironmentQualify})
		for _, request := range []struct {
			method string
			path   string
			body   any
		}{
			{method: http.MethodGet, path: "/v1/projects/missing/environments/staging/state"},
			{method: http.MethodGet, path: "/v1/projects/missing/environments/production/promotion-preview?from=staging"},
			{method: http.MethodGet, path: "/v1/deployments/missing/url"},
			{method: http.MethodPost, path: "/v1/projects/missing/environments/staging/qualifications", body: map[string]any{}},
		} {
			rec := e.do(t, request.method, request.path, request.body, nil)
			if rec.Code == http.StatusForbidden {
				t.Errorf("preflight scope rejected %s %s: %s", request.method, request.path, rec.Body)
			}
		}
		for _, request := range []struct {
			method string
			path   string
			body   any
		}{
			{method: http.MethodGet, path: "/v1/apps"},
			{method: http.MethodGet, path: "/v1/apps/shop/secrets"},
			{method: http.MethodPost, path: "/v1/apps", body: api.CreateAppRequest{Slug: "not-authorized"}},
			{method: http.MethodPost, path: "/v1/projects/shop/environments", body: map[string]any{"slug": "new-env"}},
		} {
			rec := e.do(t, request.method, request.path, request.body, nil)
			assertProblem(t, rec, http.StatusForbidden, api.CodeForbidden)
		}
	})
	t.Run("admin-key/GET-allowed", func(t *testing.T) {
		e := setupWithScopes(t, api.ScopesAdminOnly)
		rec := e.do(t, http.MethodGet, "/v1/apps", nil, nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("admin GET /v1/apps: %d %s", rec.Code, rec.Body)
		}
	})

	t.Run("admin-key/POST-allowed", func(t *testing.T) {
		e := setupWithScopes(t, api.ScopesAdminOnly)
		rec := e.do(t, http.MethodPost, "/v1/apps", api.CreateAppRequest{Slug: "admin-app"}, nil)
		if rec.Code != http.StatusCreated {
			t.Fatalf("admin POST /v1/apps: %d %s", rec.Code, rec.Body)
		}
	})

	t.Run("apps-read-key/GET-allowed", func(t *testing.T) {
		e := setupWithScopes(t, api.ScopesReadSurface)
		rec := e.do(t, http.MethodGet, "/v1/apps", nil, nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("apps:read GET /v1/apps: %d %s", rec.Code, rec.Body)
		}
	})

	t.Run("apps-read-only-key/POST-forbidden", func(t *testing.T) {
		// A key with the bare apps:read scope (NOT admin) cannot
		// mutate; using api.ScopesReadSurface here would mint a key
		// that ALSO carries admin — defeating the matrix's whole
		// point. So this case mints a single-scope apps:read key.
		e := setupWithScopes(t, []string{api.ScopeAppsRead})
		rec := e.do(t, http.MethodPost, "/v1/apps", api.CreateAppRequest{Slug: "apps-read-app"}, nil)
		assertProblem(t, rec, http.StatusForbidden, api.CodeForbidden)
	})

	t.Run("deploy-write-key/POST-allowed", func(t *testing.T) {
		e := setupWithScopes(t, api.ScopesDeployWriteSurface)
		rec := e.do(t, http.MethodPost, "/v1/apps", api.CreateAppRequest{Slug: "deploy-app"}, nil)
		if rec.Code != http.StatusCreated {
			t.Fatalf("deploy:write POST /v1/apps: %d %s", rec.Code, rec.Body)
		}
	})

	t.Run("deploy-write-only-key/GET-forbidden", func(t *testing.T) {
		e := setupWithScopes(t, []string{api.ScopeDeployWrite})
		rec := e.do(t, http.MethodGet, "/v1/apps", nil, nil)
		assertProblem(t, rec, http.StatusForbidden, api.CodeForbidden)
	})

	t.Run("deploy-write-only-key/account-plan-forbidden", func(t *testing.T) {
		// /v1/account/plan is admin-only — deploy:write is not enough.
		e := setupWithScopes(t, []string{api.ScopeDeployWrite})
		rec := e.do(t, http.MethodPatch, "/v1/account/plan", map[string]string{"plan": "scale"}, nil)
		assertProblem(t, rec, http.StatusForbidden, api.CodeForbidden)
	})

	t.Run("apps-read-only-key/account-plan-forbidden", func(t *testing.T) {
		e := setupWithScopes(t, []string{api.ScopeAppsRead})
		rec := e.do(t, http.MethodPatch, "/v1/account/plan", map[string]string{"plan": "scale"}, nil)
		assertProblem(t, rec, http.StatusForbidden, api.CodeForbidden)
	})

	t.Run("usage-read-only-key/GET-usage-allowed", func(t *testing.T) {
		e := setupWithScopes(t, []string{api.ScopeUsageRead})
		rec := e.do(t, http.MethodGet, "/v1/usage", nil, nil)
		if rec.Code == http.StatusForbidden {
			t.Fatalf("usage:read GET /v1/usage was 403: %s", rec.Body)
		}
	})

	t.Run("narrow-keys/session-inventory-forbidden", func(t *testing.T) {
		// The session list exposes the owner's sign-in IPs and user
		// agents; only admin keys (and cookie sessions) may read it.
		for _, scope := range []string{api.ScopeUsageRead, api.ScopeAppsRead, api.ScopeDeployWrite} {
			e := setupWithScopes(t, []string{scope})
			if _, err := e.store.CreateSession(context.Background(),
				"11111111-1111-4111-8111-111111111111", e.acct.ID, "203.0.113.9", "Firefox"); err != nil {
				t.Fatal(err)
			}
			rec := e.do(t, http.MethodGet, "/v1/auth/sessions", nil, nil)
			assertProblem(t, rec, http.StatusForbidden, api.CodeForbidden)
			if strings.Contains(rec.Body.String(), "203.0.113.9") {
				t.Fatalf("%s key leaked a session IP: %s", scope, rec.Body)
			}
		}
		admin := setupWithScopes(t, []string{api.ScopeAdmin})
		if rec := admin.do(t, http.MethodGet, "/v1/auth/sessions", nil, nil); rec.Code != http.StatusOK {
			t.Fatalf("admin key GET /v1/auth/sessions = %d, want 200: %s", rec.Code, rec.Body)
		}
	})

	t.Run("usage-read-only-key/billing-cancel-forbidden", func(t *testing.T) {
		// Cancelling downgrades the plan at period end; a read-only
		// usage key (a cost dashboard) must not be able to do it.
		e := setupWithScopes(t, []string{api.ScopeUsageRead})
		rec := e.do(t, http.MethodPost, "/v1/billing/cancel", nil, nil)
		assertProblem(t, rec, http.StatusForbidden, api.CodeForbidden)
	})

	t.Run("usage-read-only-key/GET-apps-forbidden", func(t *testing.T) {
		e := setupWithScopes(t, []string{api.ScopeUsageRead})
		rec := e.do(t, http.MethodGet, "/v1/apps", nil, nil)
		assertProblem(t, rec, http.StatusForbidden, api.CodeForbidden)
	})

	t.Run("secrets-write-key/PUT-secret-scope-allowed", func(t *testing.T) {
		// The seal-and-persist path requires a recipient that the
		// setupWithScopes harness doesn't wire, but the
		// requireScope middleware fires BEFORE the handler — so
		// any non-403 result (a 400 validation, a 5xx seal error,
		// or the success 204) confirms the scope check passed.
		// Asserting != 403 pins "secrets:write satisfied the route".
		e := setupWithScopes(t, []string{api.ScopeSecretsWrite})
		rec := e.do(t, http.MethodPut, "/v1/apps/missing-app/secrets/API_TOKEN",
			api.PutAppSecretRequest{Value: "hush"}, nil)
		if rec.Code == http.StatusForbidden {
			t.Fatalf("secrets:write PUT secret was 403: %s", rec.Body)
		}
	})

	t.Run("secrets-write-key/DELETE-secret-scope-allowed", func(t *testing.T) {
		// Symmetric to PUT: secrets:write covers DELETE too per
		// ADR-034 rev2. The requireScope middleware fires before
		// loadApp, so any non-403 result (404 from a missing app
		// is the most likely) confirms the scope check passed.
		// This case pins the wiring bug from PR #232 review:
		// before the fix, DELETE was gated by ScopesDeployWriteSurface
		// which leaked admin/deploy rights into the secrets surface.
		e := setupWithScopes(t, []string{api.ScopeSecretsWrite})
		rec := e.do(t, http.MethodDelete, "/v1/apps/missing-app/secrets/API_TOKEN", nil, nil)
		if rec.Code == http.StatusForbidden {
			t.Fatalf("secrets:write DELETE secret was 403: %s", rec.Body)
		}
	})

	t.Run("deploy-write-only-key/DELETE-secret-forbidden", func(t *testing.T) {
		// Symmetric negative: deploy:write must NOT grant DELETE
		// on /v1/apps/{slug}/secrets/{key}. ADR-034 rev2 reserves
		// that surface for secrets:write (and admin). A deploy-only
		// CI key cannot rotate secrets by deletion.
		e := setupWithScopes(t, []string{api.ScopeDeployWrite})
		rec := e.do(t, http.MethodDelete, "/v1/apps/some-app/secrets/API_TOKEN", nil, nil)
		assertProblem(t, rec, http.StatusForbidden, api.CodeForbidden)
	})

	t.Run("secrets-write-only-key/POST-deploy-forbidden", func(t *testing.T) {
		e := setupWithScopes(t, []string{api.ScopeSecretsWrite})
		rec := e.do(t, http.MethodPost, "/v1/apps", api.CreateAppRequest{Slug: "elevator-app"}, nil)
		assertProblem(t, rec, http.StatusForbidden, api.CodeForbidden)
	})

	t.Run("mint-rejects-unknown-scope", func(t *testing.T) {
		e := setupWithScopes(t, api.ScopesAdminOnly)
		rec := e.do(t, http.MethodPost, "/v1/keys", api.CreateKeyRequest{Label: "bad", Scopes: []string{"banana"}}, nil)
		assertProblem(t, rec, http.StatusBadRequest, api.CodeValidation)
	})

	t.Run("apps-read-only-key/list-secrets-allowed", func(t *testing.T) {
		// GET /v1/apps/{slug}/secrets is on the read surface. For
		// the read assertion, we only need the scope check to
		// fire — a missing app surfaces as 404, not 403.
		e := setupWithScopes(t, []string{api.ScopeAppsRead})
		rec := e.do(t, http.MethodGet, "/v1/apps/foo/secrets", nil, nil)
		assertProblem(t, rec, http.StatusNotFound, api.CodeNotFound) // 404, not 403
	})

	t.Run("deploy-write-only-key/list-keys-403", func(t *testing.T) {
		// GET /v1/keys is on the read surface. A deploy-only key has
		// neither scope and gets 403 from requireScope before the
		// handler runs.
		e := setupWithScopes(t, []string{api.ScopeDeployWrite})
		rec := e.do(t, http.MethodGet, "/v1/keys", nil, nil)
		assertProblem(t, rec, http.StatusForbidden, api.CodeForbidden)
	})

	t.Run("deploy-write-only-key/mint-key-403", func(t *testing.T) {
		// POST /v1/keys is admin-only (minting a key from a scoped key
		// would be a self-elevation primitive).
		e := setupWithScopes(t, []string{api.ScopeDeployWrite})
		rec := e.do(t, http.MethodPost, "/v1/keys",
			api.CreateKeyRequest{Label: "try"}, nil)
		assertProblem(t, rec, http.StatusForbidden, api.CodeForbidden)
	})

	t.Run("deploy-write-only-key/delete-key-403", func(t *testing.T) {
		// DELETE /v1/keys/{id} is admin-only for the same reason.
		e := setupWithScopes(t, []string{api.ScopeDeployWrite})
		rec := e.do(t, http.MethodDelete, "/v1/keys/some-id", nil, nil)
		assertProblem(t, rec, http.StatusForbidden, api.CodeForbidden)
	})

	t.Run("session-cookie/admin-route-allowed", func(t *testing.T) {
		// The load-bearing branch of principalHasScope: a session-cookie
		// principal has Key==nil and is implicitly admin. GET
		// /v1/account/keys/grace_window_days is admin-only; without
		// the Key==nil branch, the dashboard user would be 403'd.
		// We assert != 403 because the handler may 200/404 on lookup
		// (grace_window_days is gated by a feature flag at the
		// handler) — what we care about is that requireScope did
		// NOT 403.
		//
		// Note: PATCH /v1/account/plan was the original route here
		// but the IAM-hardening mega-PR commit 6 added step-up MFA
		// to that route (5-min freshness); the cookie minted by
		// setupWithSession doesn't carry a recent StepUpAt stamp
		// so requireStepUp would 403 even when requireScope would
		// have allowed it. GET /grace_window_days sidesteps the
		// step-up gate without losing the "admin-scope, no-key" test
		// coverage.
		e, cookie := setupWithSession(t)
		req := httptest.NewRequest(http.MethodGet, "/v1/account/keys/grace_window_days", nil)
		req.AddCookie(cookie)
		rec := httptest.NewRecorder()
		e.h.ServeHTTP(rec, req)
		if rec.Code == http.StatusForbidden {
			t.Fatalf("session cookie was 403'd on admin route: %s", rec.Body)
		}
	})
}

// TestMintKeyScopes covers the lifecycle of a freshly-minted key: the
// response carries the requested scopes, listing the key returns them,
// and a key with the listed scopes can authenticate.
func TestMintKeyScopes(t *testing.T) {
	t.Run("mint-with-scopes", func(t *testing.T) {
		e := setupWithScopes(t, api.ScopesAdminOnly)
		rec := e.do(t, http.MethodPost, "/v1/keys",
			api.CreateKeyRequest{Label: "deploy-only", Scopes: []string{"apps:read", "deploy:write"}}, nil)
		if rec.Code != http.StatusCreated {
			t.Fatalf("mint: %d %s", rec.Code, rec.Body)
		}
		var out api.APIKeyResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if got := fmt.Sprintf("%v", out.Scopes); got != "[apps:read deploy:write]" {
			t.Errorf("scopes = %s, want [apps:read deploy:write]", got)
		}
	})

	t.Run("mint-dedupes-repeated-scopes", func(t *testing.T) {
		e := setupWithScopes(t, api.ScopesAdminOnly)
		rec := e.do(t, http.MethodPost, "/v1/keys",
			api.CreateKeyRequest{Label: "dup", Scopes: []string{"apps:read", "apps:read", "deploy:write"}}, nil)
		if rec.Code != http.StatusCreated {
			t.Fatalf("mint: %d %s", rec.Code, rec.Body)
		}
		var out api.APIKeyResponse
		_ = json.Unmarshal(rec.Body.Bytes(), &out)
		if got := fmt.Sprintf("%v", out.Scopes); got != "[apps:read deploy:write]" {
			t.Errorf("deduped scopes = %s, want [apps:read deploy:write]", got)
		}
	})

	t.Run("mint-omitted-scopes-defaults-to-admin", func(t *testing.T) {
		e := setupWithScopes(t, api.ScopesAdminOnly)
		rec := e.do(t, http.MethodPost, "/v1/keys", api.CreateKeyRequest{Label: "defaulted"}, nil)
		if rec.Code != http.StatusCreated {
			t.Fatalf("mint: %d %s", rec.Code, rec.Body)
		}
		var out api.APIKeyResponse
		_ = json.Unmarshal(rec.Body.Bytes(), &out)
		if len(out.Scopes) != 1 || out.Scopes[0] != "admin" {
			t.Errorf("omitted scopes = %v, want [admin]", out.Scopes)
		}
	})

	t.Run("list-keys-shows-scopes", func(t *testing.T) {
		e := setupWithScopes(t, api.ScopesAdminOnly)
		rec := e.do(t, http.MethodGet, "/v1/keys", nil, nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("list: %d %s", rec.Code, rec.Body)
		}
		var out []api.APIKeyResponse
		_ = json.Unmarshal(rec.Body.Bytes(), &out)
		if len(out) != 1 {
			t.Fatalf("got %d keys, want 1", len(out))
		}
		if got := fmt.Sprintf("%v", out[0].Scopes); got != "[admin]" {
			t.Errorf("listed scopes = %s, want [admin]", got)
		}
	})

	t.Run("mint-empty-scopes-defaults-to-admin", func(t *testing.T) {
		// Explicit empty array is operationally identical to omitted:
		// both fall through the empty-input branch in
		// NormalizeCreateKeyScopes. Pin the contract so a future
		// "deny empty" change is loud.
		e := setupWithScopes(t, api.ScopesAdminOnly)
		rec := e.do(t, http.MethodPost, "/v1/keys",
			api.CreateKeyRequest{Label: "empty", Scopes: []string{}}, nil)
		if rec.Code != http.StatusCreated {
			t.Fatalf("mint with empty scopes: %d %s", rec.Code, rec.Body)
		}
		var out api.APIKeyResponse
		_ = json.Unmarshal(rec.Body.Bytes(), &out)
		if len(out.Scopes) != 1 || out.Scopes[0] != "admin" {
			t.Errorf("empty scopes defaulted to %v, want [admin]", out.Scopes)
		}
	})
}

// httptest helper that's already available in the test package is
// httptest.NewRecorder; importing it here keeps the per-file imports
// self-contained.
var _ = httptest.NewRecorder
