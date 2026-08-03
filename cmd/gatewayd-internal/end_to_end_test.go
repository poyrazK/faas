// End-to-end integration test for the gatewayd → githubd proxy.
//
// Recorded push_event.json (cmd/githubd/testdata/push_event.json) is
// HMAC-signed at the gatewayd edge → proxied through newGithubdProxy
// to a fake githubd loopback listener → Service.HandlePushRequest
// dispatches via the new reconcile+source path → asserts the
// proxy translates the upstream response back to the caller.
//
// PR-H (mega-PR-GH of repo decomposition Phase 5) rewrites the
// githubd dispatch path through pkg/reconcile.Service; the legacy
// CreateDeployment function-typed seam is retired. The e2e tests
// here pin the proxy contract (HMAC at edge, ignored-body on
// no-binding, 401 on tampered sig) — the githubd internal
// contract is pinned by pkg/githubd/service_test.go.
package main

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"sync/atomic"
	"testing"

	"github.com/onebox-faas/faas/pkg/githubd"
	"github.com/onebox-faas/faas/pkg/state"
)

func signE2E(body []byte, secret []byte) string {
	mac := hmac.New(sha256.New, secret)
	mac.Write(body)
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

type fakeGithubd struct {
	svc  *githubd.Service
	hits *atomic.Int32
}

func (f *fakeGithubd) handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.hits.Add(1)
		body, _ := io.ReadAll(r.Body)
		_, err := f.svc.HandlePushRequest(r.Context(), body)
		if err != nil {
			if githubd.IsNoBinding(err) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte(`{"status":"ignored","reason":"no_binding"}`))
				return
			}
			if githubd.IsIgnored(err) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte(`{"status":"ignored","reason":"feature_branch"}`))
				return
			}
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		// PR-H: the upstream handler mirrors the production githubd
		// server response shape — {status, added, changed, removed}.
		// The proxy test asserts the proxy passes through; the
		// internal reconcile result is opaque to the proxy.
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"queued","added":1,"changed":0,"removed":0}`))
	})
}

type stubBindings struct {
	by map[string]state.GitHubBinding
}

func (s *stubBindings) GetAppBinding(_ context.Context, _, _ string) (state.GitHubBinding, error) {
	return state.GitHubBinding{}, nil
}

func TestEndToEnd_RecordedPushReachesUpstream(t *testing.T) {
	// PR-H retires the CreateDeployment seam; the proxy contract
	// now asserts that the recorded push reaches the upstream
	// githubd listener (the reconcile internals are pinned by
	// pkg/githubd tests, not here). The upstream service is left
	// without reconcile wired → HandlePushRequest short-circuits
	// to a nil-deref-free path. We exercise the no-binding fall-
	// through instead, since wiring reconcile here would mean
	// re-implementing the unit-test rig inside cmd/gatewayd.
	secret := []byte("end-to-end-webhook-secret")

	svc := githubd.NewService(slog.New(slog.NewTextHandler(io.Discard, nil)))
	svc.Bindings = &stubBindings{by: map[string]state.GitHubBinding{}}

	var hits atomic.Int32
	upstream := &fakeGithubd{svc: svc, hits: &hits}
	upstreamSrv := httptest.NewServer(upstream.handler())
	t.Cleanup(upstreamSrv.Close)

	proxy := newGithubdProxy(upstreamSrv.URL, secret, http.NewServeMux(), slog.New(slog.NewTextHandler(io.Discard, nil)), nil)

	body, err := os.ReadFile("../githubd/testdata/push_event.json")
	if err != nil {
		t.Fatalf("read push_event.json: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/webhooks/github", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Hub-Signature-256", signE2E(body, secret))
	req.Header.Set("X-GitHub-Event", "push")
	req.Header.Set("X-GitHub-Delivery", "delivery-rec-1")

	rr := httptest.NewRecorder()
	proxy.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
	if hits.Load() != 1 {
		t.Errorf("upstream hits = %d, want 1", hits.Load())
	}
	if !bytes.Contains(rr.Body.Bytes(), []byte("ignored")) {
		t.Errorf("response should report ignored; got %s", rr.Body.String())
	}
}

func TestEndToEnd_NoBindingReturnsIgnored200(t *testing.T) {
	secret := []byte("end-to-end-webhook-secret")
	svc := githubd.NewService(slog.New(slog.NewTextHandler(io.Discard, nil)))
	svc.Bindings = &stubBindings{by: map[string]state.GitHubBinding{}}

	var hits atomic.Int32
	upstream := &fakeGithubd{svc: svc, hits: &hits}
	upstreamSrv := httptest.NewServer(upstream.handler())
	t.Cleanup(upstreamSrv.Close)

	proxy := newGithubdProxy(upstreamSrv.URL, secret, http.NewServeMux(), slog.New(slog.NewTextHandler(io.Discard, nil)), nil)

	body := []byte(`{"ref":"refs/heads/main","after":"deadbeef","repository":{"full_name":"unknown/repo","name":"repo"},"pusher":{"name":"x"}}`)
	req := httptest.NewRequest(http.MethodPost, "/webhooks/github", bytes.NewReader(body))
	req.Header.Set("X-Hub-Signature-256", signE2E(body, secret))
	req.Header.Set("X-GitHub-Delivery", "delivery-rec-ignored")

	rr := httptest.NewRecorder()
	proxy.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (ignored payload)", rr.Code)
	}
	if !bytes.Contains(rr.Body.Bytes(), []byte("ignored")) {
		t.Errorf("response should report ignored; got %s", rr.Body.String())
	}
	if hits.Load() != 1 {
		t.Errorf("upstream hits = %d, want 1", hits.Load())
	}
}

func TestEndToEnd_TamperedSignatureRejectedAtEdge(t *testing.T) {
	secret := []byte("end-to-end-webhook-secret")
	svc := githubd.NewService(slog.New(slog.NewTextHandler(io.Discard, nil)))
	svc.Bindings = &stubBindings{by: map[string]state.GitHubBinding{
		"octo/api|main": {BindingID: "b-1", RepoFullName: "octo/api", ProductionBranch: "main"},
	}}

	var hits atomic.Int32
	upstream := &fakeGithubd{svc: svc, hits: &hits}
	upstreamSrv := httptest.NewServer(upstream.handler())
	t.Cleanup(upstreamSrv.Close)

	proxy := newGithubdProxy(upstreamSrv.URL, secret, http.NewServeMux(), slog.New(slog.NewTextHandler(io.Discard, nil)), nil)

	body, err := os.ReadFile("../githubd/testdata/push_event.json")
	if err != nil {
		t.Fatalf("read push_event.json: %v", err)
	}
	// Sign with a different secret → must 401 at the edge.
	req := httptest.NewRequest(http.MethodPost, "/webhooks/github", bytes.NewReader(body))
	req.Header.Set("X-Hub-Signature-256", signE2E(body, []byte("WRONG")))

	rr := httptest.NewRecorder()
	proxy.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", rr.Code)
	}
	if hits.Load() != 0 {
		t.Errorf("upstream should NOT be hit on bad sig; hits = %d", hits.Load())
	}
}

// TestEndToEnd_M75_RecordedPushReachesUpstream is the slice-9
// acceptance gate (spec §14 M7.5 row 1): "push to main reaches
// the upstream githubd listener." The recorded push_event.json
// is replayed through the full gatewayd → githubd proxy stack.
// The proxy contract is the load-bearing assertion here; the
// githubd reconcile internals are pinned separately by
// pkg/githubd/service_test.go::TestHandlePushRequest_HappyPath
// and the cmd/githubd source_fetcher_test.go (added in PR-GH.4).
func TestEndToEnd_M75_RecordedPushReachesUpstream(t *testing.T) {
	secret := []byte("m7.5-acceptance-secret")

	svc := githubd.NewService(slog.New(slog.NewTextHandler(io.Discard, nil)))
	svc.Bindings = &stubBindings{by: map[string]state.GitHubBinding{}}

	var hits atomic.Int32
	upstream := &fakeGithubd{svc: svc, hits: &hits}
	upstreamSrv := httptest.NewServer(upstream.handler())
	t.Cleanup(upstreamSrv.Close)

	proxy := newGithubdProxy(upstreamSrv.URL, secret, http.NewServeMux(), slog.New(slog.NewTextHandler(io.Discard, nil)), nil)

	body, err := os.ReadFile("../githubd/testdata/push_event.json")
	if err != nil {
		t.Fatalf("read push_event.json: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/webhooks/github", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Hub-Signature-256", signE2E(body, secret))
	req.Header.Set("X-GitHub-Event", "push")
	req.Header.Set("X-GitHub-Delivery", "delivery-rec-m75")
	rec := httptest.NewRecorder()
	proxy.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %s", rec.Code, rec.Body.String())
	}
	if hits.Load() != 1 {
		t.Errorf("upstream hits = %d, want 1", hits.Load())
	}
}
