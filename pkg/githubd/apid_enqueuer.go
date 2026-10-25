// apid_enqueuer.go — the BuildEnqueuer that hands each staged build to the
// apid gRPC bridge (issue #432 phase 5).
//
// The apid handler (pkg/apid/apidserver/githubd_bridge.go) creates the
// deployment + build rows and emits the build_queued pg_notify that builderd
// LISTENs on. The bridge direction is githubd → apid; the apid-side githubd
// gRPC client is the opposite direction and unrelated to this file.
//
// On EnqueueBuild RPC failure the durable delivery worker retries. DeliveryID
// makes successful app enqueues stable across attempts, so a later pass fills
// only missing work without duplicating deployments or builds.
//
// It lives in this package, not in cmd/githubd, so the webhook-to-deployment
// acceptance test drives the production mapping rather than a copy.
package githubd

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	githubdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/githubd/v1"
	"github.com/onebox-faas/faas/pkg/state"
)

// ApidBuildClient is the apid bridge's build-enqueue call. cmd/githubd's
// ApidBridgeClient (stub or live socket client) satisfies it.
type ApidBuildClient interface {
	EnqueueBuild(ctx context.Context, in *githubdpb.EnqueueBuildRequest) (*githubdpb.EnqueueBuildResponse, error)
}

// errApidBuildClientMissing is returned when no bridge client was wired.
var errApidBuildClientMissing = errors.New("githubd: apid bridge client is not configured")

type apidEnqueuer struct {
	client ApidBuildClient
	log    *slog.Logger
}

// NewApidEnqueuer returns a BuildEnqueuer that passes each BuildSpec to the
// apid gRPC bridge. A nil client makes every Enqueue fail, so the dispatcher
// logs and skips instead of crashing.
func NewApidEnqueuer(client ApidBuildClient, log *slog.Logger) BuildEnqueuer {
	if log == nil {
		log = slog.Default()
	}
	return &apidEnqueuer{client: client, log: log}
}

// Enqueue translates the BuildSpec into the apid gRPC EnqueueBuildRequest,
// calls the bridge, and converts the response back into a state.Build for the
// dispatcher to embed in the webhook response body. The translation is
// field-for-field.
//
// SourcePath is mandatory. An empty SourcePath means the staging step
// (staging.go) didn't run or failed; the dispatcher already logs and skips on
// staging failure before reaching this Enqueue, so a missing SourcePath here
// is a caller bug.
func (a *apidEnqueuer) Enqueue(ctx context.Context, spec BuildSpec) (state.Build, error) {
	if a.client == nil {
		return state.Build{}, errApidBuildClientMissing
	}
	if spec.SourcePath == "" {
		a.log.Warn("apid enqueuer: empty source path (staging step missed); skipping",
			"app_id", spec.App.ID, "commit_sha", spec.CommitSHA)
		return state.Build{}, errors.New("apid enqueuer: empty source path (staging step missed)")
	}
	if spec.App.ID == "" || spec.App.AccountID == "" {
		// Both are required by the apid-side handler; pre-check so the
		// dispatcher gets a clear error before the gRPC round-trip.
		return state.Build{}, fmt.Errorf("apid enqueuer: app_id and account_id are required (got %q,%q)",
			spec.App.ID, spec.App.AccountID)
	}

	req := &githubdpb.EnqueueBuildRequest{
		AccountId:            spec.App.AccountID,
		AppId:                spec.App.ID,
		DeliveryId:           spec.DeliveryID,
		CommitSha:            spec.CommitSHA,
		SourcePath:           spec.SourcePath,
		SourceUrl:            spec.SourceURL,
		SourceBytes:          spec.SourceBytes,
		RepoFullName:         spec.RepoFullName,
		Ref:                  spec.Ref,
		Tag:                  spec.Tag,
		Branch:               spec.Branch,
		DeploymentScope:      spec.Scope,
		GithubSourceRef:      spec.GitHubSourceRef,
		GithubInstallationId: spec.GitHubInstallationID,
		Pusher:               spec.Pusher,
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
		// The bridge returns gRPC errors that the apid handler maps via
		// pkg/grpcerr. The dispatcher logs and skips per the
		// partial-success contract; the audit row carries the status.
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
