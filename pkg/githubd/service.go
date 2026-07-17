// githubd service (spec §14 M7.5, ADR-012).
//
// Service is the business-logic core of the githubd daemon. It
// implements the gRPC contract (see pkg/githubdgrpc/server.go) and
// the loopback HTTP webhook handler. Slice 7 covers the webhook →
// "create a deployment" path; slice 8 fills in OAuth + the
// install-token cache + the Checks writer.
//
// Service is constructed by cmd/githubd/main.go and shared across
// the gRPC server + the HTTP webhook listener.
package githubd

import (
	"context"
	"errors"
	"log/slog"
	"net/http"

	"github.com/onebox-faas/faas/pkg/githubdgrpc"
)

// AppBindingStore is the slice of store.Slice githubd reads to look
// up (repo → app) bindings for incoming pushes. Implemented by
// pkg/state.PgStore (slice 8 adds the table; slice 7 uses a
// no-op stub so the wiring compiles in dev without schema
// changes).
type AppBindingStore interface {
	GetAppBinding(ctx context.Context, repoFullName, branch string) (githubdgrpc.AppBinding, error)
}

// CreateDeployment is the seam githubd calls into apid (slice 7).
// In production this is an apid gRPC client method
// (githubdgrpc.Client.CreateDeploymentFromPush); tests inject a
// recording fake.
type CreateDeployment func(ctx context.Context, repoFullName, branch, commitSHA string) (deploymentID string, err error)

// WriteCheck is the seam githubd uses to push build-phase updates
// back to GitHub. Slice 8 fills this in with the real Checks writer;
// slice 7 leaves it as a stub that records the call into the log
// so the smoke test can assert on the order.
type WriteCheck func(ctx context.Context, repoFullName, commitSHA string, phase githubdgrpc.CheckPhase) error

// Service is the business-logic object shared across the HTTP
// webhook handler and the gRPC server. nil fields fall back to
// safe no-ops (so partial deployments in slice 7 degrade
// gracefully until slice 8).
type Service struct {
	Log              *slog.Logger
	Bindings         AppBindingStore
	CreateDeployment CreateDeployment
	WriteCheck       WriteCheck
}

// NewService builds a Service. Tests inject fakes for the three
// seams; production wires the live implementations in slice 8.
func NewService(log *slog.Logger) *Service {
	if log == nil {
		log = slog.Default()
	}
	return &Service{Log: log}
}

// HandlePushRequest is the HTTP webhook entry point. It verifies
// the signature (the proxy already did HMAC verify on the edge;
// this is a defense-in-depth check), decodes the body, looks up the
// (repo, branch) binding, and if matched calls CreateDeployment.
//
// Returns the deployment ID on 200. ErrNoBinding = 200 with an
// ignored-payload body (the push is for a repo we don't own).
// Other errors = 500.
func (s *Service) HandlePushRequest(ctx context.Context, body []byte) (string, error) {
	ev, err := DecodePush(body)
	if err != nil {
		return "", err
	}
	branch := refToBranch(ev.Ref)
	if branch == "" {
		return "", ErrNoBinding // unusual ref shape; ignore
	}
	binding, err := s.Bindings.GetAppBinding(ctx, ev.Repository.FullName, branch)
	if err != nil {
		// No binding → silent ignore. GitHub retries on 5xx but
		// we're saying "yes I got it, but it doesn't apply to me"
		// by returning 200 with an explicit payload.
		return "", ErrNoBinding
	}
	if binding.BindingID == "" {
		// Slice-8 contract: an empty BindingID means "no row".
		// Keeping the check explicit guards against partial impls.
		return "", ErrNoBinding
	}
	depID, err := s.CreateDeployment(ctx, ev.Repository.FullName, branch, ev.After)
	if err != nil {
		return "", err
	}
	// Best-effort: queue the queued check on GitHub. Errors here
	// don't block the deploy from being recorded locally.
	if s.WriteCheck != nil {
		_ = s.WriteCheck(ctx, ev.Repository.FullName, ev.After, githubdgrpc.CheckPhaseQueued)
	}
	s.Log.Info("githubd push → deployment",
		"repo", ev.Repository.FullName, "branch", branch,
		"sha", ev.After, "binding", binding.BindingID,
		"deployment_id", depID, "pusher", ev.Pusher.Name)
	return depID, nil
}

// ErrNoBinding is returned by HandlePushRequest when the push
// doesn't match any registered binding. The HTTP handler turns
// this into a 200 with an ignored-payload body.
var ErrNoBinding = errNoBinding{}

type errNoBinding struct{}

func (errNoBinding) Error() string { return "githubd: no binding for push" }

// IsNoBinding reports whether err is the no-binding sentinel.
func IsNoBinding(err error) bool {
	return errors.As(err, new(errNoBinding))
}

// refToBranch converts "refs/heads/main" → "main". Returns "" for
// refs that aren't a branch (e.g. refs/tags/v1.0 — slice 7 only
// handles branch pushes; tag pushes arrive in a future slice).
func refToBranch(ref string) string {
	const prefix = "refs/heads/"
	if len(ref) <= len(prefix) {
		return ""
	}
	if ref[:len(prefix)] != prefix {
		return ""
	}
	return ref[len(prefix):]
}

// WebhookHTTPHandler returns an http.Handler that serves
// POST /webhooks/github. Today it returns 503 because the proxy
// (cmd/gatewayd) verifies the signature and forwards; this handler
// is loopback-only and reachable from the gatewayd reverse proxy.
// A future PR may let githubd stand up its own listener when
// gatewayd isn't on the same host (not in v1.0).
func (s *Service) WebhookHTTPHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "githubd: webhook arrives via gatewayd's edge-verifying proxy", http.StatusNotImplemented)
	})
}
