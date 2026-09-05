// state_enqueue.go — githubd-side apidEnqueuer that bridges the
// githubd push-dispatch seam to the apid gRPC bridge (issue #432
// phase 5).
//
// apidEnqueuer implements the pkg/githubd.BuildEnqueuer interface
// (Enqueue(ctx, BuildSpec) (state.Build, error)) by passing the
// BuildSpec to the apid gRPC bridge via the ApidBridgeClient
// (cmd/githubd/apid_bridge.go). The apid handler
// (cmd/apid/githubd_bridge.go) creates the deployment + build
// rows and emits the build_queued pg_notify that builderd
// LISTENs on.
//
// The bridge direction is githubd → apid. The apid-side githubd
// gRPC client (cmd/apid/githubd_client.go) is the OPPOSITE
// direction (apid → githubd) and is unrelated to this file.
//
// On EnqueueBuild RPC failure:
//   - errApidBridgeNotReady (stub mode): the dispatcher logs +
//     skips the build (the apid daemon isn't running or the
//     socket isn't configured). The webhook still returns 200
//     with the partial build_ids list — partial-success is the
//     contract.
//   - any gRPC error: log + skip the same way. The upstream
//     dispatch path is best-effort (pkg/githubd/enqueuer.go
//     doc-comment: "failing the whole push because one of 50
//     builds was rejected is worse for the customer").
//
// Audit: the apid handler emits auth.deployment.enqueued (or
// similar) via the apid auditor; the githubd-side dispatcher
// emits project.build.enqueued via the githubd auditor (see
// pkg/reconcile/audit.go for the taxonomy). The two events are
// linked by the build_id.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/onebox-faas/faas/pkg/githubd"
	"github.com/onebox-faas/faas/pkg/state"

	githubdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/githubd/v1"
)

// apidEnqueuer is the production implementation of
// githubd.BuildEnqueuer. It dials the apid gRPC bridge via
// the ApidBridgeClient (which itself wraps the generated
// githubdpb.GithubdClient). The dispatcher wires this in
// cmd/githubd/main.go when the FAAS_APID_GITHUBD_BRIDGE_SOCK
// env var is set.
//
// The logger is non-nil (NewApidEnqueuer falls back to
// slog.Default() like the other production wires).
type apidEnqueuer struct {
	client ApidBridgeClient
	log    *slog.Logger
}

// NewApidEnqueuer returns a githubd.BuildEnqueuer that passes
// each BuildSpec to the apid gRPC bridge. The client is the
// ApidBridgeClient returned by newApidBridgeClient (either
// stub or live depending on the FAAS_APID_GITHUBD_BRIDGE_SOCK
// env var). nil client falls back to a stub so the dispatcher
// can still log + skip without crashing.
func NewApidEnqueuer(client ApidBridgeClient, log *slog.Logger) githubd.BuildEnqueuer {
	if log == nil {
		log = slog.Default()
	}
	if client == nil {
		// Defensive: a nil client should never reach
		// here (the constructor in main.go falls back to
		// the stub), but if it does the dispatcher
		// should still log + skip — not panic.
		client = stubApidBridgeClient{}
	}
	return &apidEnqueuer{client: client, log: log}
}

// Enqueue translates the BuildSpec into the apid gRPC
// EnqueueBuildRequest, calls the bridge, and converts the
// response back into a state.Build for the dispatcher to
// embed in the webhook response body.
//
// The translation is field-for-field; the proto field names
// are snake_case (gRPC convention) and the BuildSpec fields
// are CamelCase (Go convention). The dispatcher doesn't need
// to know about the proto package — that's the bridge's
// only reason for existing.
//
// SourcePath is mandatory. An empty SourcePath means the
// staging step (pkg/githubd/staging.go) didn't run or
// failed — the dispatcher already logs + skips on staging
// failure before reaching this Enqueue, so a missing
// SourcePath here is a caller bug. Surface it as Internal
// so the audit row carries "staging step missed".
func (a *apidEnqueuer) Enqueue(ctx context.Context, spec githubd.BuildSpec) (state.Build, error) {
	if spec.SourcePath == "" {
		a.log.Warn("apid enqueuer: empty source path (staging step missed); skipping",
			"app_id", spec.App.ID, "commit_sha", spec.CommitSHA)
		return state.Build{}, errors.New("apid enqueuer: empty source path (staging step missed)")
	}
	if spec.App.ID == "" || spec.App.AccountID == "" {
		// Both are required by the apid-side handler
		// (see EnqueueBuild Request validation in
		// cmd/apid/githubd_bridge.go). We pre-check
		// here so the dispatcher gets a clear error
		// before the gRPC round-trip.
		return state.Build{}, fmt.Errorf("apid enqueuer: app_id and account_id are required (got %q,%q)",
			spec.App.ID, spec.App.AccountID)
	}

	req := &githubdpb.EnqueueBuildRequest{
		AccountId:    spec.App.AccountID,
		AppId:        spec.App.ID,
		CommitSha:    spec.CommitSHA,
		SourcePath:   spec.SourcePath,
		SourceUrl:    spec.SourceURL,
		SourceBytes:  spec.SourceBytes,
		RepoFullName: spec.RepoFullName,
		Ref:          spec.Ref,
		Branch:       spec.Branch,
		Pusher:       spec.Pusher,
		// Issue #977 / ADR-116: thread the annotation surface from
		// the dispatcher. PRNumber is int32 on the wire (the proto3
		// convention); the bridge converts to int for the apidsource.
		// SenderLogin takes precedence over Pusher on the bridge
		// side when present (sender is the actor who opened the
		// webhook; pusher is the commit author for push events).
		PullRequestNumber: spec.PRNumber,
		SenderLogin:       spec.SenderLogin,
		// EventKind drives the deployments.kind stamp (issue #272 /
		// ADR-094): push → DeploymentKindGitHub, pull_request →
		// DeploymentKindPreview. Zero (UNSPECIFIED) is the legacy
		// fallback that keeps stamping DeploymentKindGitHub so
		// older dispatchers / test fixtures stay binary-compatible.
		EventKind: spec.EventKind,
	}

	resp, err := a.client.EnqueueBuild(ctx, req)
	if err != nil {
		// The bridge returns gRPC errors that the
		// apid handler maps via pkg/grpcerr. The
		// dispatcher logs + skips per the
		// partial-success contract; the audit
		// row carries the gRPC status code.
		return state.Build{}, fmt.Errorf("apid enqueuer: EnqueueBuild: %w", err)
	}
	if resp == nil {
		return state.Build{}, errors.New("apid enqueuer: nil response on success")
	}
	kind := state.DeploymentKindGitHub
	if spec.EventKind == githubdpb.EnqueueBuildEventKind_EVENT_KIND_PULL_REQUEST {
		kind = state.DeploymentKindPreview
	}
	return state.Build{
		ID:           resp.BuildId,
		DeploymentID: resp.DeploymentId,
		Kind:         kind,
	}, nil
}
