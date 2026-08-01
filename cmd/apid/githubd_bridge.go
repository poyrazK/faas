// githubd_bridge.go — apid-side gRPC receiver for the githubd → apid
// per-app build enqueue (issue #432 phase 5).
//
// Direction: githubd → apid only. githubd dials /run/faas/apid-githubd.sock
// after the dispatcher fans out the touched apps and stages each app's
// RootDir subtree into githubd's build-sources dir as a per-app .tar.gz.
// The apid handler creates the deployment row (Kind=DeploymentKindGitHub),
// the build row, and emits the build_queued pg_notify that builderd
// LISTENs on (cmd/builderd/main.go:151).
//
// Depguard: the .golangci.yml apid-control-plane-only deny list keeps
// apid from importing githubd directly. The bridge is via the wire
// types only — no shared internal state.
//
// Auth: the unix-socket 0660/group-`faas` DAC is the only auth in v1.0
// (ADR-015). The transport is insecure credentials over a trusted
// local path; see pkg/githubdgrpc.Dial. Wire encryption is a
// multi-box follow-up (ADR-052).
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	githubdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/githubd/v1"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/grpcerr"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/wire"
)

// githubdBridgeStore is the minimal slice of state.Store the receiver
// needs. Defined as an interface so the receiver can be unit-tested
// without spinning up a real pgxpool (state.Store is already the
// canonical seam — same pattern as advisory_receiver.go).
type githubdBridgeStore interface {
	AppByID(ctx context.Context, id string) (state.App, error)
	LatestDeployment(ctx context.Context, appID string) (state.Deployment, error)
	CreateDeployment(ctx context.Context, d state.Deployment) (state.Deployment, error)
	UpdateDeploymentStatus(ctx context.Context, id string, status state.DeploymentStatus, logPath string) error
	CreateBuild(ctx context.Context, deploymentID string, kind state.DeploymentKind, sourceBytes int64, logPath string) (state.Build, error)
}

// githubdBridgeNotifier is the minimal Notifier surface the receiver
// needs. The cmd/apid pgNotifier satisfies this; unit tests pass a stub.
type githubdBridgeNotifier interface {
	Notify(ctx context.Context, channel, payload string) error
}

// githubdBridge is the in-package server implementation of
// githubdpb.GithubdServer (just the EnqueueBuild RPC; the rest of
// the githubdpb surface is unused on the apid side). Wired by
// registerGithubdBridge onto a *grpc.Server that runGithubdBridgeServer
// in main.go owns.
type githubdBridge struct {
	githubdpb.UnimplementedGithubdServer
	store githubdBridgeStore
	notif githubdBridgeNotifier
	log   *slog.Logger
	ops   *wire.OpsMetrics
	spool string // build-spool root for the build.log path
}

// maxSourceBytes caps the per-app tarball at 256 MB — the App-layer
// quota for the Free plan (pkg/api/limits.go). Larger plans can ship
// up to 2 GB (Scale); the limit check is per-app below.
const githubdBridgeMaxSourceBytes = 2 << 30 // 2 GB upper bound (Scale plan)

// minSourceBytes guards against a 0-byte tarball that bug-fixed
// staging would land. Below this we reject — the source is unreadable
// or the staging step silently produced an empty file.
const githubdBridgeMinSourceBytes = 1

// notifySourceGithub is the `source` / `kind` payload value the
// bridge stamps on its build_queued + supersede notifies. The
// value matches the GitHub bridge's existing taxonomy
// (DeploymentKindGitHub = "github"); using a constant here
// keeps the JSON payload + the state enum on the same
// vocabulary so a future dashboard parser doesn't have to
// second-guess the spelling.
const notifySourceGithub = "github"

// notifyAppField is the JSON payload key that carries the app
// id. The bridge emits it in both the build_queued payload
// and the slog Warn lines so the operator can grep for it
// without first decoding the payload body.
const notifyAppField = "app"

// EnqueueBuild creates the deployment + build rows for one (app,
// commit_sha) and emits the build_queued pg_notify. githubd stages
// the per-app tarball on its own workdir and the path on disk is
// passed in source_path — builderd reads it directly (pkg/builderd/
// builderd.go:321 No URL fetch path).
//
// The flow mirrors cmd/apid/deploy_inputs.go:170-220 (the apid-side
// tarball deploy handler) but is driven by the gRPC payload instead
// of a multipart upload. Differences from the apid-side handler:
//
//  1. There's no plan-gate / IAM / session check here — the unix-socket
//     DAC is the only auth in v1.0 (ADR-015). Multi-box mTLS work
//     (ADR-052) raises the bar in a follow-up.
//  2. The source_path is operator-blessed (githubd stages it under
//     <FAAS_GITHUBD_WORK_DIR>/build-sources/<account_id>/<app_id>/<commit_sha>/).
//     We still validate the suffix (.tar.gz) per builderd's gzip
//     detector (pkg/builderd/detect.go:48).
//  3. The Kind is hard-coded to DeploymentKindGitHub (vs the apid
//     tarball path which is DeploymentKindTarball / Dockerfile).
//  4. There's no IAM-2 MFA flip (issue #186) — that's a customer-facing
//     deploy concern, not a daemons-to-daemons bridge concern.
//
// Returns empty build_id / deployment_id and a NotFound status when
// the app_id is unknown (so the githubd dispatcher can skip-and-
// continue past a stale binding). Returns ResourceExhausted when the
// plan's SourceTarballMaxMB is exceeded — the githubd dispatcher logs
// + skips the affected app.
func (g *githubdBridge) EnqueueBuild(ctx context.Context, req *githubdpb.EnqueueBuildRequest) (*githubdpb.EnqueueBuildResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "EnqueueBuild: nil request")
	}
	// Required-field checks. The proto3 "no required" rule is
	// deliberately bypassed here so a githubd-side bug that emits
	// an empty account_id or app_id surfaces as a clear error
	// instead of a downstream NOT NULL violation.
	if req.AccountId == "" || req.AppId == "" || req.CommitSha == "" {
		return nil, status.Errorf(codes.InvalidArgument,
			"EnqueueBuild: account_id, app_id, commit_sha are required (got %q,%q,%q)",
			req.AccountId, req.AppId, req.CommitSha)
	}
	if req.SourcePath == "" || req.SourceUrl == "" {
		return nil, status.Errorf(codes.InvalidArgument,
			"EnqueueBuild: source_path and source_url are required (got %q,%q)",
			req.SourcePath, req.SourceUrl)
	}
	if !strings.HasSuffix(req.SourcePath, ".tar.gz") {
		return nil, status.Errorf(codes.InvalidArgument,
			"EnqueueBuild: source_path must end in .tar.gz (got %q)", req.SourcePath)
	}
	if req.SourceBytes < githubdBridgeMinSourceBytes {
		return nil, status.Errorf(codes.InvalidArgument,
			"EnqueueBuild: source_bytes must be > 0 (got %d)", req.SourceBytes)
	}
	if req.SourceBytes > githubdBridgeMaxSourceBytes {
		return nil, status.Errorf(codes.ResourceExhausted,
			"EnqueueBuild: source_bytes=%d exceeds the 2 GB ceiling", req.SourceBytes)
	}

	// Look up the app. The app_id MUST exist (githubd resolves it
	// from the binding rows + repo scan; a missing app is a stale
	// binding or a githubd-side bug). NotFound surfaces it
	// deterministically.
	app, err := g.store.AppByID(ctx, req.AppId)
	if err != nil {
		if grpcIsNotFound(err) {
			return nil, status.Errorf(codes.NotFound,
				"EnqueueBuild: app %q not found (stale binding?)", req.AppId)
		}
		return nil, status.Errorf(codes.Internal, "EnqueueBuild: app lookup: %v", err)
	}
	// Account guard: the app must belong to the claimed account.
	// A mismatch is a hard fail (NotFound, not PermissionDenied,
	// so a forged call can't enumerate which apps belong to
	// other accounts).
	if app.AccountID != req.AccountId {
		return nil, status.Errorf(codes.NotFound,
			"EnqueueBuild: app %q not found in account %q", req.AppId, req.AccountId)
	}
	// Active-app gate: reject if the app is in any non-active
	// status (soft-deleted, suspended, etc.). Mirrors the apid-side
	// CREATE-ingest check (pkg/state/pgstore.go::CreateDeployment
	// the SELECT 1 FROM apps WHERE id=$1 AND status='active').
	if app.Status != state.AppActive {
		return nil, status.Errorf(codes.FailedPrecondition,
			"EnqueueBuild: app %q is not active (status=%q)", req.AppId, app.Status)
	}

	// Source-path validation: must exist on disk and be a regular
	// file. The githubd dispatcher stages the tarball before the
	// gRPC call, so a missing file is a githubd-side bug —
	// surface it as Internal so the dispatcher logs and skips.
	st, statErr := os.Stat(req.SourcePath)
	if statErr != nil {
		return nil, status.Errorf(codes.Internal,
			"EnqueueBuild: source_path %q stat: %v (githubd staging bug?)",
			req.SourcePath, statErr)
	}
	if !st.Mode().IsRegular() {
		return nil, status.Errorf(codes.InvalidArgument,
			"EnqueueBuild: source_path %q is not a regular file", req.SourcePath)
	}
	// Cross-check declared bytes against the on-disk size — a
	// mismatch is a githubd-side bug (the dispatcher should size
	// the tarball before the gRPC call).
	if st.Size() != req.SourceBytes {
		return nil, status.Errorf(codes.InvalidArgument,
			"EnqueueBuild: source_path size=%d != declared source_bytes=%d",
			st.Size(), req.SourceBytes)
	}

	// Create the deployment row. The kind is hard-coded to the
	// githubd path so ADR-048 metering can split it from the
	// apid-side tarball/deploy builds. The previous non-terminal
	// deployment row is superseded inside store.CreateDeployment's
	// tx (pkg/state/pgstore.go::CreateDeployment — the in-tx
	// supersede is a Phase 5 PR-B feature).
	//
	// SourceURL carries the upstream archive URL (provenance-only;
	// builderd reads source_path, not source_url — see
	// pkg/builderd/builderd.go:644). CommitSHA is the upstream
	// commit (deployments_commit_sha_len_chk, migrations/00047).
	// The Pusher / Ref / Branch / RepoFullName fields are
	// intentionally NOT in pkg/state.Deployment yet — they're
	// carried in the proto for forward-compat and stashed in
	// SourceURL if/when a future migration adds columns.
	prev, _ := g.store.LatestDeployment(ctx, app.ID)
	d, err := g.store.CreateDeployment(ctx, state.Deployment{
		AppID:       app.ID,
		Kind:        state.DeploymentKindGitHub,
		SourcePath:  req.SourcePath,
		SourceBytes: req.SourceBytes,
		SourceURL:   req.SourceUrl,
		Handler:     "",
		Status:      state.DeployPending,
		CommitSHA:   req.CommitSha,
	})
	if err != nil {
		// Map the platform's RFC 7807 Problem to a gRPC status
		// (pkg/grpcerr.ToStatus). The bridge respects the same
		// Code-to-gRPC-code mapping as the rest of the apid
		// control-plane surface.
		return nil, g.asGRPC("create deployment", err)
	}

	// Create the build row. The log path is siphoed to the
	// build-spool root so builderd can write to it directly
	// (mirrors cmd/apid/deploy_inputs.go:192-201).
	logDir := filepath.Join(g.spool, d.ID)
	_ = os.MkdirAll(logDir, 0o755)
	logPath := filepath.Join(logDir, "build.log")
	_, _ = os.Create(logPath)
	// Mark the deployment as 'building' so the dashboard + the
	// Deployments API surface the row in the in-flight state
	// before builderd claims it. apid-side deploy_inputs.go:196
	// does the same; the pattern is the tracked-row state
	// machine, not the optional post-write.
	_ = g.store.UpdateDeploymentStatus(ctx, d.ID, state.DeployBuilding, "")

	build, err := g.store.CreateBuild(ctx, d.ID, state.DeploymentKindGitHub, req.SourceBytes, logPath)
	if err != nil {
		return nil, g.asGRPC("create build", err)
	}

	// Emit build_queued. builderd LISTENs on this exact channel
	// (cmd/builderd/main.go:151, 226). The payload shape is the
	// same as the apid-side deploy_inputs.go:207-209 emit — json
	// with {build, deployment, app, kind} — so any future
	// shape via the test fixtures lands both sides at once.
	payload, _ := json.Marshal(map[string]any{
		"build":        build.ID,
		"deployment":   d.ID,
		notifyAppField: app.ID,
		"kind":         string(state.DeploymentKindGitHub),
		"source":       notifySourceGithub,
	})
	if err := g.notif.Notify(ctx, db.NotifyBuildQueued, string(payload)); err != nil {
		// Notify is best-effort: the build row is durable and
		// builderd's poll-recovery (pkg/state/pgstore.go:2386
		// ClaimNextQueuedBuild) files missing notifies. Log + skip.
		g.log.Warn("githubd bridge: notify build_queued failed (durable recovery will pick it up)",
			"build", build.ID, "deployment", d.ID, notifyAppField, app.ID, "err", err)
	}

	// Phase 5 PR-B: emit a second NotifyDeploymentChanged so the
	// imaged F5-cleanup handler drops the prior snapshot. Skipped
	// on first deploy (no prev).
	if prev.ID != "" {
		supPayload, _ := json.Marshal(map[string]any{
			"kind":          notifySourceGithub,
			"status":        "superseded",
			"app_id":        app.ID,
			"deployment_id": prev.ID,
			"to":            prev.ID,
		})
		_ = g.notif.Notify(ctx, db.NotifyDeploymentChanged, string(supPayload))
	}

	// §12 metric: apid_githubd_bridge_enqueued_total. The
	// accessor is nil-receiver safe; when ops is nil (test path)
	// the metric stays zero.
	g.ops.IncGithubdBridgeEnqueued(wire.GithubdBridgeKindGitHub)

	g.log.Info("githubd bridge: build enqueued",
		"build", build.ID, "deployment", d.ID, notifyAppField, app.ID,
		"commit_sha", req.CommitSha, "repo", req.RepoFullName, "branch", req.Branch)
	return &githubdpb.EnqueueBuildResponse{
		BuildId:      build.ID,
		DeploymentId: d.ID,
		AppId:        app.ID,
	}, nil
}

// asGRPC maps a state.Store error (or any error wrapping a
// pkg/api.Problem) to a gRPC status. The pkg/grpcerr.ToStatus
// bridge is the spec'd seam (ADR-013); the apid-side handlers
// route every error through it so the wire codes stay aligned
// across the REST + gRPC surface.
func (g *githubdBridge) asGRPC(op string, err error) error {
	if err == nil {
		return nil
	}
	// First try the RFC 7807 path. The cmd/apid pgstore wraps
	// platform errors with errors.Join, so we walk the chain
	// for a *api.Problem. errors.As handles Join + Unwrap
	// chains transparently (per memory
	// errorlint-non-wrapping-errors-join) — the manual
	// unwrapErrors walk would be both redundant and a
	// false-negative on joined errors whose middle wrapper
	// IS the Problem.
	var prob *api.Problem
	if errors.As(err, &prob) {
		return grpcerr.ToStatus(prob)
	}
	// Fallback: pass-through with the op string so the operator
	// log carries the step name. The gRPC code is Internal since
	// we cannot classify the error.
	return status.Error(codes.Internal, fmt.Sprintf("githubd bridge: %s: %v", op, err))
}

// grpcIsNotFound is a small helper that peeks at the error chain
// for state.ErrNotFound (pkg/state/errors.go). Mirrors the
// advisory_receiver.go pattern (which uses errors.Is internally).
// errors.Is handles both single-wrap (fmt.Errorf %w) and Join
// chains — the manual unwrap walk would be both redundant and
// a false-negative on joined errors whose middle wrapper
// equals state.ErrNotFound.
func grpcIsNotFound(err error) bool {
	if err == nil {
		return false
	}
	return errors.Is(err, state.ErrNotFound)
}

// registerGithubdBridge binds the GithubdServer (only EnqueueBuild
// is implemented; the rest is UnimplementedGithubdServer) onto a
// gRPC server. Called from runGithubdBridgeServer in main.go
// alongside the HTTP server lifecycle.
func registerGithubdBridge(s *grpc.Server, store githubdBridgeStore, notif githubdBridgeNotifier, log *slog.Logger, ops *wire.OpsMetrics, spool string) {
	githubdpb.RegisterGithubdServer(s, &githubdBridge{
		store: store,
		notif: notif,
		log:   log,
		ops:   ops,
		spool: spool,
	})
}
