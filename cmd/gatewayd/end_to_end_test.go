// End-to-end integration test for slice 7 (gatewayd → githubd proxy):
//
// Recorded push_event.json (cmd/githubd/testdata/push_event.json) is
// HMAC-signed at the gatewayd edge → proxied through newGithubdProxy
// to a fake githubd loopback listener → Service.HandlePushRequest
// dispatches via recording seams → asserts the deployment row was
// created with the recorded repo/branch/SHA.
//
// The fake upstream handler is intentionally written in cmd/gatewayd
// (not cmd/githubd) because that's where the proxy lives and where
// the round-trip has to be observed from the public surface. The
// githubd-side Service + binding store are real packages — only the
// HTTP listener is fake.
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
	"github.com/onebox-faas/faas/pkg/githubdgrpc"
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
		depID, err := f.svc.HandlePushRequest(r.Context(), body)
		if err != nil {
			if githubd.IsNoBinding(err) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte(`{"status":"ignored","reason":"no_binding"}`))
				return
			}
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"queued","deployment_id":"` + depID + `"}`))
	})
}

type stubBindings struct {
	by map[string]githubdgrpc.AppBinding
}

func (s *stubBindings) GetAppBinding(_ context.Context, repo, branch string) (githubdgrpc.AppBinding, error) {
	return s.by[repo+"|"+branch], nil
}

func TestEndToEnd_RecordedPushToDeployment(t *testing.T) {
	secret := []byte("end-to-end-webhook-secret")

	svc := githubd.NewService(slog.New(slog.NewTextHandler(io.Discard, nil)))
	svc.Bindings = &stubBindings{
		by: map[string]githubdgrpc.AppBinding{
			"octo/api|main": {BindingID: "b-1", RepoFullName: "octo/api", ProductionBranch: "main"},
		},
	}
	var gotRepo, gotBranch, gotSHA string
	svc.CreateDeployment = func(_ context.Context, repo, branch, sha string) (string, error) {
		gotRepo, gotBranch, gotSHA = repo, branch, sha
		return "dep-from-recording", nil
	}

	var hits atomic.Int32
	upstream := &fakeGithubd{svc: svc, hits: &hits}
	upstreamSrv := httptest.NewServer(upstream.handler())
	t.Cleanup(upstreamSrv.Close)

	proxy := newGithubdProxy(upstreamSrv.URL, secret, http.NewServeMux(), slog.New(slog.NewTextHandler(io.Discard, nil)))

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
	if !bytes.Contains(rr.Body.Bytes(), []byte("dep-from-recording")) {
		t.Errorf("response body missing deployment id; got %s", rr.Body.String())
	}
	if gotRepo != "octo/api" {
		t.Errorf("CreateDeployment repo = %q, want octo/api", gotRepo)
	}
	if gotBranch != "main" {
		t.Errorf("CreateDeployment branch = %q, want main", gotBranch)
	}
	if gotSHA != "deadbeefcafebabe1234567890abcdef12345678" {
		t.Errorf("CreateDeployment sha = %q, want deadbeef...", gotSHA)
	}
}

func TestEndToEnd_NoBindingReturnsIgnored200(t *testing.T) {
	secret := []byte("end-to-end-webhook-secret")
	svc := githubd.NewService(slog.New(slog.NewTextHandler(io.Discard, nil)))
	svc.Bindings = &stubBindings{by: map[string]githubdgrpc.AppBinding{}}

	var hits atomic.Int32
	upstream := &fakeGithubd{svc: svc, hits: &hits}
	upstreamSrv := httptest.NewServer(upstream.handler())
	t.Cleanup(upstreamSrv.Close)

	proxy := newGithubdProxy(upstreamSrv.URL, secret, http.NewServeMux(), slog.New(slog.NewTextHandler(io.Discard, nil)))

	body := []byte(`{"ref":"refs/heads/main","after":"deadbeef","repository":{"full_name":"unknown/repo","name":"repo"},"pusher":{"name":"x"}}`)
	req := httptest.NewRequest(http.MethodPost, "/webhooks/github", bytes.NewReader(body))
	req.Header.Set("X-Hub-Signature-256", signE2E(body, secret))

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
	svc.Bindings = &stubBindings{by: map[string]githubdgrpc.AppBinding{
		"octo/api|main": {BindingID: "b-1", RepoFullName: "octo/api", ProductionBranch: "main"},
	}}

	var hits atomic.Int32
	upstream := &fakeGithubd{svc: svc, hits: &hits}
	upstreamSrv := httptest.NewServer(upstream.handler())
	t.Cleanup(upstreamSrv.Close)

	proxy := newGithubdProxy(upstreamSrv.URL, secret, http.NewServeMux(), slog.New(slog.NewTextHandler(io.Discard, nil)))

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

// TestEndToEnd_M75_RecordedPushLandsDeployment is the slice-9
// acceptance gate (spec §14 M7.5 row 1): "push to main auto-deploys
// via the normal build pipeline." The recorded push_event.json is
// replayed through the full gatewayd → githubd proxy stack; the
// upstream githubd's Service.HandlePushRequest creates the
// deployment row from the recorded (repo, branch, sha). The
// deployment-id surfaces back to the caller via the proxy's
// 200 response.
//
// This test pins the contract that the founder-facing flow relies
// on: `faas connect github` (slice 8 dashboard) → `faas deploy
// --repo owner/name` (slice 9 CLI) → push → live.
func TestEndToEnd_M75_RecordedPushLandsDeployment(t *testing.T) {
	secret := []byte("m7.5-acceptance-secret")

	svc := githubd.NewService(slog.New(slog.NewTextHandler(io.Discard, nil)))
	svc.Bindings = &stubBindings{by: map[string]githubdgrpc.AppBinding{
		"octo/api|main": {BindingID: "b-m75", RepoFullName: "octo/api", ProductionBranch: "main"},
	}}
	var gotRepo, gotBranch, gotSHA string
	svc.CreateDeployment = func(_ context.Context, repo, branch, sha string) (string, error) {
		gotRepo, gotBranch, gotSHA = repo, branch, sha
		return "dep-m75-acceptance", nil
	}

	var hits atomic.Int32
	upstream := &fakeGithubd{svc: svc, hits: &hits}
	upstreamSrv := httptest.NewServer(upstream.handler())
	t.Cleanup(upstreamSrv.Close)

	proxy := newGithubdProxy(upstreamSrv.URL, secret, http.NewServeMux(), slog.New(slog.NewTextHandler(io.Discard, nil)))

	body, err := os.ReadFile("../githubd/testdata/push_event.json")
	if err != nil {
		t.Fatalf("read push_event.json: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/webhooks/github", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Hub-Signature-256", signE2E(body, secret))
	req.Header.Set("X-GitHub-Event", "push")
	rec := httptest.NewRecorder()
	proxy.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %s", rec.Code, rec.Body.String())
	}
	if hits.Load() != 1 {
		t.Errorf("upstream hits = %d, want 1", hits.Load())
	}
	if gotRepo != "octo/api" || gotBranch != "main" || gotSHA != "deadbeefcafebabe1234567890abcdef12345678" {
		t.Errorf("CreateDeployment args = (%q, %q, %q); want (octo/api, main, deadbeef...)", gotRepo, gotBranch, gotSHA)
	}
	if !bytes.Contains(rec.Body.Bytes(), []byte("dep-m75-acceptance")) {
		t.Errorf("response missing deployment_id; body = %s", rec.Body.String())
	}
}
