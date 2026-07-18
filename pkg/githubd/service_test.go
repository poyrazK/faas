package githubd

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/githubdgrpc"
)

// stubBindings is a hand-rolled fake of AppBindingStore — slice 8
// adds the real table-backed implementation. Today it lets the
// service run end-to-end against the body decode + refToBranch path.
type stubBindings struct {
	byRepo map[string]githubdgrpc.AppBinding // key: "owner/repo|branch"
	err    error
}

func (s *stubBindings) GetAppBinding(_ context.Context, repo, branch string) (githubdgrpc.AppBinding, error) {
	if s.err != nil {
		return githubdgrpc.AppBinding{}, s.err
	}
	return s.byRepo[repo+"|"+branch], nil
}

func TestHandlePushRequest_HappyPath(t *testing.T) {
	bindings := &stubBindings{
		byRepo: map[string]githubdgrpc.AppBinding{
			"octo/api|main": {BindingID: "b-1", RepoFullName: "octo/api", ProductionBranch: "main"},
		},
	}
	var gotRepo, gotBranch, gotSHA string
	svc := NewService(slog.New(slog.NewTextHandler(io.Discard, nil)))
	svc.Bindings = bindings
	svc.CreateDeployment = func(_ context.Context, repo, branch, sha string) (string, error) {
		gotRepo, gotBranch, gotSHA = repo, branch, sha
		return "dep-1", nil
	}
	var checkRepo, checkSHA string
	var checkPhase githubdgrpc.CheckPhase
	svc.WriteCheck = func(_ context.Context, repo, sha string, phase githubdgrpc.CheckPhase) error {
		checkRepo, checkSHA, checkPhase = repo, sha, phase
		return nil
	}

	body := []byte(`{"ref":"refs/heads/main","after":"cafebabe","repository":{"full_name":"octo/api","name":"api"},"pusher":{"name":"alice"}}`)
	depID, err := svc.HandlePushRequest(context.Background(), body)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if depID != "dep-1" {
		t.Errorf("depID = %q, want dep-1", depID)
	}
	if gotRepo != "octo/api" || gotBranch != "main" || gotSHA != "cafebabe" {
		t.Errorf("CreateDeployment args = (%q,%q,%q), want (octo/api,main,cafebabe)", gotRepo, gotBranch, gotSHA)
	}
	if checkRepo != "octo/api" || checkSHA != "cafebabe" || checkPhase != githubdgrpc.CheckPhaseQueued {
		t.Errorf("WriteCheck args = (%q,%q,%v)", checkRepo, checkSHA, checkPhase)
	}
}

func TestHandlePushRequest_NoBindingIsSilent(t *testing.T) {
	bindings := &stubBindings{byRepo: map[string]githubdgrpc.AppBinding{}}
	svc := NewService(slog.New(slog.NewTextHandler(io.Discard, nil)))
	svc.Bindings = bindings
	called := false
	svc.CreateDeployment = func(_ context.Context, _, _, _ string) (string, error) {
		called = true
		return "", nil
	}
	body := []byte(`{"ref":"refs/heads/main","after":"deadbeef","repository":{"full_name":"octo/api","name":"api"},"pusher":{"name":"alice"}}`)
	_, err := svc.HandlePushRequest(context.Background(), body)
	if !IsNoBinding(err) {
		t.Errorf("err = %v, want ErrNoBinding", err)
	}
	if called {
		t.Error("CreateDeployment should NOT be called when binding is missing")
	}
}

func TestHandlePushRequest_CreateErrorBubbles(t *testing.T) {
	bindings := &stubBindings{
		byRepo: map[string]githubdgrpc.AppBinding{
			"octo/api|main": {BindingID: "b-1"},
		},
	}
	svc := NewService(slog.New(slog.NewTextHandler(io.Discard, nil)))
	svc.Bindings = bindings
	want := errors.New("apid exploded")
	svc.CreateDeployment = func(_ context.Context, _, _, _ string) (string, error) {
		return "", want
	}
	body := []byte(`{"ref":"refs/heads/main","after":"x","repository":{"full_name":"octo/api","name":"api"},"pusher":{"name":"alice"}}`)
	_, err := svc.HandlePushRequest(context.Background(), body)
	if !errors.Is(err, want) {
		t.Errorf("err = %v, want %v", err, want)
	}
}

func TestHandlePushRequest_TagIsIgnored(t *testing.T) {
	bindings := &stubBindings{byRepo: map[string]githubdgrpc.AppBinding{}}
	svc := NewService(slog.New(slog.NewTextHandler(io.Discard, nil)))
	svc.Bindings = bindings
	body := []byte(`{"ref":"refs/tags/v1.0","after":"x","repository":{"full_name":"octo/api","name":"api"},"pusher":{"name":"alice"}}`)
	_, err := svc.HandlePushRequest(context.Background(), body)
	if !IsNoBinding(err) {
		t.Errorf("tag push → err = %v, want ErrNoBinding", err)
	}
}

func TestWebhookHTTPHandler_IsLoopbackOnly(t *testing.T) {
	// Today the proxy is the canonical ingress. The direct handler
	// returns 501 — covers the misroute case where a reverse proxy
	// forgets the secret.
	svc := NewService(slog.New(slog.NewTextHandler(io.Discard, nil)))
	req := httptest.NewRequest(http.MethodPost, "/webhooks/github", strings.NewReader("{}"))
	rr := httptest.NewRecorder()
	svc.WebhookHTTPHandler().ServeHTTP(rr, req)
	if rr.Code != http.StatusNotImplemented {
		t.Errorf("direct handler status = %d, want 501", rr.Code)
	}
}
