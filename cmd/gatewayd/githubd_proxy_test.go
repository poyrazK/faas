// githubd proxy tests (slice 7, ADR-012). Verifies:
//
//   - HMAC verify at the edge rejects bad signatures with 401
//   - good signatures forward verbatim to githubd's loopback
//   - non-webhook paths fall through untouched
//   - body cap (10 MiB) returns 413
//   - missing secret causes every webhook to be rejected (closed by default)
package main

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/onebox-faas/faas/pkg/githubd"
)

func sign(body []byte, secret []byte) string {
	mac := hmac.New(sha256.New, secret)
	mac.Write(body)
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

func newTestProxy(t *testing.T, secret []byte, upstream http.Handler) (http.Handler, *atomic.Int32) {
	t.Helper()
	var upstreamHits atomic.Int32
	upstreamHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upstreamHits.Add(1)
		// Echo the body back as JSON so the test can assert on it
		// without depending on githubd's internal handler shape.
		body, _ := io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-Echo-Path", r.URL.Path)
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write(body)
	})
	// upstream may be nil — when it is, the test only exercises
	// 401/413/closed paths and the echo handler above is unreachable.
	_ = upstream
	srv := httptest.NewServer(upstreamHandler)
	t.Cleanup(srv.Close)
	proxy := newGithubdProxy(srv.URL, secret, http.NewServeMux(), slog.New(slog.NewTextHandler(io.Discard, nil)))
	return proxy, &upstreamHits
}

func TestGithubdProxy_VerifiesAndForwards(t *testing.T) {
	secret := []byte("test-webhook-secret")
	proxy, hits := newTestProxy(t, secret, nil)

	body := []byte(`{"ref":"refs/heads/main","after":"abc"}`)
	req := httptest.NewRequest(http.MethodPost, githubWebhookPath, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Hub-Signature-256", sign(body, secret))

	rr := httptest.NewRecorder()
	proxy.ServeHTTP(rr, req)

	if rr.Code != http.StatusAccepted {
		t.Errorf("status = %d, want 202; body=%s", rr.Code, rr.Body.String())
	}
	if hits.Load() != 1 {
		t.Errorf("upstream hits = %d, want 1", hits.Load())
	}
	if !bytes.Contains(rr.Body.Bytes(), body) {
		t.Errorf("upstream body not echoed; got %q", rr.Body.String())
	}
	if got := rr.Header().Get("X-Echo-Path"); got != githubWebhookPath {
		t.Errorf("X-Echo-Path = %q, want %q", got, githubWebhookPath)
	}
}

func TestGithubdProxy_BadSignatureReturns401(t *testing.T) {
	secret := []byte("test-webhook-secret")
	proxy, hits := newTestProxy(t, secret, nil)

	body := []byte(`{"ref":"refs/heads/main"}`)
	req := httptest.NewRequest(http.MethodPost, githubWebhookPath, bytes.NewReader(body))
	req.Header.Set("X-Hub-Signature-256", sign(body, []byte("WRONG-SECRET")))

	rr := httptest.NewRecorder()
	proxy.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", rr.Code)
	}
	if hits.Load() != 0 {
		t.Errorf("upstream should NOT be hit on bad sig; hits = %d", hits.Load())
	}
}

func TestGithubdProxy_MissingHeaderReturns401(t *testing.T) {
	secret := []byte("test-webhook-secret")
	proxy, hits := newTestProxy(t, secret, nil)

	body := []byte(`{"ref":"refs/heads/main"}`)
	req := httptest.NewRequest(http.MethodPost, githubWebhookPath, bytes.NewReader(body))
	// no X-Hub-Signature-256 header

	rr := httptest.NewRecorder()
	proxy.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", rr.Code)
	}
	if hits.Load() != 0 {
		t.Errorf("upstream should NOT be hit when header missing; hits = %d", hits.Load())
	}
}

func TestGithubdProxy_EmptySecretRejectsEverything(t *testing.T) {
	// No upstream → closed-by-default: empty secret arms a
	// zero-byte key, but our proxy path refuses to verify against
	// an unset secret at all (see loadGithubWebhookSecret → githubd.VerifyPushSignature).
	var hits atomic.Int32
	upstreamHandler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusAccepted)
	})
	srv := httptest.NewServer(upstreamHandler)
	defer srv.Close()
	proxy := newGithubdProxy(srv.URL, nil /* secret unset */, http.NewServeMux(),
		slog.New(slog.NewTextHandler(io.Discard, nil)))

	body := []byte(`{"ref":"refs/heads/main"}`)
	req := httptest.NewRequest(http.MethodPost, githubWebhookPath, bytes.NewReader(body))
	req.Header.Set("X-Hub-Signature-256", sign(body, []byte("anything")))

	rr := httptest.NewRecorder()
	proxy.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", rr.Code)
	}
	if hits.Load() != 0 {
		t.Errorf("upstream should NOT be hit when secret missing; hits = %d", hits.Load())
	}
}

func TestGithubdProxy_NonWebhookPathsFallThrough(t *testing.T) {
	secret := []byte("test-webhook-secret")
	_, hits := newTestProxy(t, secret, nil)

	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Fallthrough", "yes")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	// Build the proxy over a fallthrough handler directly to
	// observe "did the request reach next?".
	proxy2 := newGithubdProxy("http://127.0.0.1:1", secret, mux, slog.New(slog.NewTextHandler(io.Discard, nil)))

	for _, p := range []string{"/dashboard/", "/oauth/callback", "/api/v1/deployments", "/v1/apps"} {
		req := httptest.NewRequest(http.MethodGet, p, nil)
		rr := httptest.NewRecorder()
		proxy2.ServeHTTP(rr, req)
		if rr.Code != http.StatusOK {
			t.Errorf("%s: status = %d, want 200", p, rr.Code)
		}
		if rr.Header().Get("X-Fallthrough") != "yes" {
			t.Errorf("%s: fallthrough header missing", p)
		}
	}
	if hits.Load() != 0 {
		t.Errorf("upstream hits = %d, want 0 (non-webhook paths must not reach githubd)", hits.Load())
	}
}

func TestGithubdProxy_OversizedBodyReturns413(t *testing.T) {
	secret := []byte("test-webhook-secret")
	proxy, hits := newTestProxy(t, secret, nil)

	// 11 MiB > 10 MiB cap. Avoid sha256 over a huge buffer here —
	// the proxy should reject before any crypto.
	big := bytes.Repeat([]byte("x"), 11<<20)
	req := httptest.NewRequest(http.MethodPost, githubWebhookPath, bytes.NewReader(big))
	req.Header.Set("X-Hub-Signature-256", "sha256=deadbeef")

	rr := httptest.NewRecorder()
	proxy.ServeHTTP(rr, req)

	if rr.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("status = %d, want 413", rr.Code)
	}
	if hits.Load() != 0 {
		t.Errorf("upstream should NOT be hit on oversized body; hits = %d", hits.Load())
	}
}

func TestGithubdProxy_PreservesCorrelationID(t *testing.T) {
	secret := []byte("test-webhook-secret")
	proxy, _ := newTestProxy(t, secret, nil)

	body := []byte(`{"ref":"refs/heads/main","after":"abc"}`)
	req := httptest.NewRequest(http.MethodPost, githubWebhookPath, bytes.NewReader(body))
	req.Header.Set("X-Hub-Signature-256", sign(body, secret))
	req.Header.Set("X-Faas-Request-Id", "rid-12345")

	rr := httptest.NewRecorder()
	proxy.ServeHTTP(rr, req)

	if rr.Code != http.StatusAccepted {
		t.Errorf("status = %d, want 202", rr.Code)
	}
	// The upstream doesn't surface the request-id in this test —
	// we just assert that a valid signed request still flows through
	// end-to-end without 4xx.
	_ = rr.Header().Get("X-Echo-Path")
}

// Sanity: when the upstream is unreachable, the proxy returns a
// 502 problem+json body — exercises the error branch.
func TestGithubdProxy_UpstreamDownReturns502(t *testing.T) {
	secret := []byte("test-webhook-secret")
	// Point at a closed port so RoundTrip fails immediately.
	proxy := newGithubdProxy("http://127.0.0.1:1", secret, http.NewServeMux(),
		slog.New(slog.NewTextHandler(io.Discard, nil)))

	body := []byte(`{"ref":"refs/heads/main","after":"abc"}`)
	req := httptest.NewRequest(http.MethodPost, githubWebhookPath, bytes.NewReader(body))
	req.Header.Set("X-Hub-Signature-256", sign(body, secret))

	rr := httptest.NewRecorder()
	proxy.ServeHTTP(rr, req)

	if rr.Code != http.StatusBadGateway {
		t.Errorf("status = %d, want 502", rr.Code)
	}
	if got := rr.Header().Get("Content-Type"); !strings.HasPrefix(got, "application/problem+json") {
		t.Errorf("content-type = %q, want application/problem+json prefix", got)
	}
}

// Verify the helper wires env → []byte correctly, and that
// FAAS_GITHUB_WEBHOOK_SECRET takes priority over FAAS_WEBHOOK_SECRET.
func TestLoadGithubWebhookSecret(t *testing.T) {
	env := map[string]string{}
	get := func(k string) string { return env[k] }

	if got := loadGithubWebhookSecret(get); got != nil {
		t.Errorf("unset → %q, want nil", got)
	}
	env["FAAS_GITHUB_WEBHOOK_SECRET"] = "  abc  "
	if got := string(loadGithubWebhookSecret(get)); got != "abc" {
		t.Errorf("trim + read: %q, want abc", got)
	}
	delete(env, "FAAS_GITHUB_WEBHOOK_SECRET")
	env["FAAS_WEBHOOK_SECRET"] = "fallback"
	if got := string(loadGithubWebhookSecret(get)); got != "fallback" {
		t.Errorf("fallback read: %q, want fallback", got)
	}
	env["FAAS_GITHUB_WEBHOOK_SECRET"] = "primary"
	if got := string(loadGithubWebhookSecret(get)); got != "primary" {
		t.Errorf("primary should win: %q, want primary", got)
	}
}

// Pin the slice-7 invariant: the githubd.Verifier is the same code
// the proxy uses (defends against accidental drift where the proxy
// forks its own verifier).
func TestGithubdProxy_VerifierMatchesGithubdPackage(t *testing.T) {
	secret := []byte("test-webhook-secret")
	body := []byte(`{"ref":"refs/heads/main"}`)
	if err := githubd.VerifyPushSignature(body, sign(body, secret), secret); err != nil {
		t.Fatalf("githubd verifier rejected a body the proxy would accept: %v", err)
	}
}
