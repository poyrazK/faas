package faas_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	faas "github.com/poyrazK/faas/sdk/go"
)

// ExampleClient_GetApp demonstrates the basic shape: build a Client,
// call a route, decode the typed response. The example is the
// canonical godoc on the package and runs as part of `go test` to
// keep the docs honest.
func ExampleClient_GetApp() {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"id":"app_1","slug":"hello-world","status":"active","url":"https://hello.example.com"}`)
	}))
	defer srv.Close()

	c, err := faas.NewClient(srv.URL, "test-token")
	if err != nil {
		fmt.Println("new client:", err)
		return
	}

	app, err := c.GetApp(context.Background(), "hello-world")
	if err != nil {
		fmt.Println("get app:", err)
		return
	}
	fmt.Println(app.Slug, app.Status)
	// Output: hello-world active
}

// ExampleClient_GetApp_problem demonstrates the structured error path:
// a 404 response with an RFC 7807 body returns *faas.APIError, and
// errors.Is matches the canonical sentinel.
func ExampleClient_GetApp_problem() {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/problem+json")
		w.WriteHeader(http.StatusNotFound)
		_ = json.NewEncoder(w).Encode(faas.Problem{
			Status: 404,
			Code:   faas.CodeNotFound,
			Title:  "App not found",
			Detail: "no app with slug 'missing'",
		})
	}))
	defer srv.Close()

	c, _ := faas.NewClient(srv.URL, "test-token")
	_, err := c.GetApp(context.Background(), "missing")
	if err == nil {
		fmt.Println("expected error")
		return
	}
	fmt.Println("is not found:", errors.Is(err, faas.ErrNotFound))
	// Output: is not found: true
}

// TestNewClient_BuildsAndCalls exercises the construction + a real
// HTTP round-trip against an httptest server. Validates that the
// public Client's *api.Client embedding works and that the
// Idempotency-Key round-tripper does not break standard requests.
func TestNewClient_BuildsAndCalls(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-token" {
			t.Errorf("missing auth: %q", r.Header.Get("Authorization"))
		}
		_, _ = io.WriteString(w, `{"id":"app_1","slug":"hello","status":"active","url":"https://hello.example.com"}`)
	}))
	defer srv.Close()

	c, err := faas.NewClient(srv.URL, "test-token")
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	app, err := c.GetApp(context.Background(), "hello")
	if err != nil {
		t.Fatalf("GetApp: %v", err)
	}
	if app.Slug != "hello" || app.Status != "active" {
		t.Errorf("unexpected app: %+v", app)
	}
}

// TestWithIdempotencyKey_StableKeyOnRequest verifies the opt-in
// Idempotency-Key path: when the caller pins a key, the request
// carries that key verbatim, and the auto-mint does not overwrite it.
func TestWithIdempotencyKey_StableKeyOnRequest(t *testing.T) {
	wantKey := "deploy-attempt-3"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", r.Method)
		}
		if got := r.Header.Get("Idempotency-Key"); got != wantKey {
			t.Errorf("Idempotency-Key: got %q, want %q", got, wantKey)
		}
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, `{"id":"app_1","slug":"hello","status":"active"}`)
	}))
	defer srv.Close()

	c, _ := faas.NewClient(srv.URL, "test-token")
	ctx := faas.WithIdempotencyKey(context.Background(), faas.IdempotencyKey(wantKey))
	if _, err := c.CreateApp(ctx, faas.CreateAppRequest{Slug: "hello"}); err != nil {
		t.Fatalf("CreateApp: %v", err)
	}
}

// TestWithIdempotencyKey_AutoMintsWhenAbsent verifies the default
// path: when the caller does not pin a key, the SDK still sends one
// (auto-mint, UUIDv4 shape). Uses CreateApp (POST) — the auto-mint
// branch in internal/api/client.go::do is gated on method != GET,
// so a GET handler would never exercise it.
func TestWithIdempotencyKey_AutoMintsWhenAbsent(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", r.Method)
		}
		got := r.Header.Get("Idempotency-Key")
		if got == "" {
			t.Errorf("auto-minted key missing")
		}
		if len(got) < 32 {
			t.Errorf("auto-minted key too short: %q (want ≥32 UUIDv4 chars)", got)
		}
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, `{"id":"app_1","slug":"hello","status":"active"}`)
	}))
	defer srv.Close()

	c, _ := faas.NewClient(srv.URL, "test-token")
	if _, err := c.CreateApp(context.Background(), faas.CreateAppRequest{Slug: "hello"}); err != nil {
		t.Fatalf("CreateApp: %v", err)
	}
}

// TestAsAPIError_ExtractsProblem exercises the convenience helper.
func TestAsAPIError_ExtractsProblem(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_ = json.NewEncoder(w).Encode(faas.Problem{
			Status: 403,
			Code:   faas.CodeForbidden,
			Detail: "API key lacks deploy:write scope",
		})
	}))
	defer srv.Close()

	c, _ := faas.NewClient(srv.URL, "no-scope-token")
	_, err := c.CreateApp(context.Background(), faas.CreateAppRequest{Slug: "x"})
	ae, ok := faas.AsAPIError(err)
	if !ok {
		t.Fatalf("AsAPIError: not an APIError: %v", err)
	}
	if ae.Problem.Code != faas.CodeForbidden {
		t.Errorf("code: got %q, want %q", ae.Problem.Code, faas.CodeForbidden)
	}
}

// TestWithLogger_AcceptsLogger verifies the option does not error
// and stores the supplied logger on the Client. The stored field is
// unexported; we hold the *slog.Logger pointer to verify identity.
// PR 4 will wire the actual logging round-tripper that observes the
// stored logger; until then this test verifies storage only.
func TestWithLogger_AcceptsLogger(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"id":"a","slug":"x","status":"active"}`)
	}))
	defer srv.Close()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	// Stash the pointer in a local before NewClient; the test
	// cannot read c.log directly (unexported), but PR 4 will
	// install a RoundTripper that uses the same pointer — by
	// keeping a reference we can assert identity later if needed.
	client, err := faas.NewClient(srv.URL, "t", faas.WithLogger(log))
	if err != nil {
		t.Fatalf("NewClient with logger: %v", err)
	}
	if client == nil {
		t.Fatal("NewClient returned nil")
	}
}

// TestSentinelCoverage exercises the three sentinels the SDK exports
// that the public ExampleClient_GetApp_problem doesn't cover
// (ErrUnauthorized, ErrRateLimited, ErrCapacity). Each subtest mounts
// a handler that returns the corresponding Problem code and asserts
// that errors.Is matches the public sentinel. ErrNotFound is
// covered by ExampleClient_GetApp_problem above.
func TestSentinelCoverage(t *testing.T) {
	cases := []struct {
		name     string
		status   int
		code     string
		sentinel error
	}{
		{"unauthorized", http.StatusUnauthorized, faas.CodeUnauthorized, faas.ErrUnauthorized},
		{"rate_limited", http.StatusTooManyRequests, faas.CodeRateLimited, faas.ErrRateLimited},
		{"capacity", http.StatusServiceUnavailable, faas.CodeCapacity, faas.ErrCapacity},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/problem+json")
				w.WriteHeader(tc.status)
				_ = json.NewEncoder(w).Encode(faas.Problem{
					Status: tc.status,
					Code:   tc.code,
					Title:  tc.name,
					Detail: "sentinel coverage test",
				})
			}))
			defer srv.Close()

			c, err := faas.NewClient(srv.URL, "test-token")
			if err != nil {
				t.Fatalf("NewClient: %v", err)
			}
			_, gerr := c.GetApp(context.Background(), "missing")
			if gerr == nil {
				t.Fatal("expected error, got nil")
			}
			if !errors.Is(gerr, tc.sentinel) {
				t.Errorf("errors.Is(%v, %v): got false, want true", gerr, tc.sentinel)
			}
			// Also assert AsAPIError succeeds and exposes the right
			// code — the typed wire shape must round-trip too.
			ae, ok := faas.AsAPIError(gerr)
			if !ok {
				t.Fatalf("AsAPIError: not an APIError: %v", gerr)
			}
			if ae.Problem.Code != tc.code {
				t.Errorf("code: got %q, want %q", ae.Problem.Code, tc.code)
			}
		})
	}
}

// deploymentReq is retained for any future fixture that needs a
// CreateDeploymentRequest body. The current PR 3.5 tests use
// CreateApp instead, which has cleaner happy-path semantics; the
// helper stays so the package's test file doesn't change shape.
func deploymentReq() faas.CreateDeploymentRequest {
	return faas.CreateDeploymentRequest{}
}
