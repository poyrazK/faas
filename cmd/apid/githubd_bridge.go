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
	"github.com/onebox-faas/faas/pkg/apid/apidsource"
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
	FailSourceDeployment(ctx context.Context, id, message string) error
	CreateBuildWithID(ctx context.Context, id, deploymentID string, kind state.DeploymentKind, sourceBytes int64, logPath string) (state.Build, error)
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
	store       githubdBridgeStore
	notif       githubdBridgeNotifier
	log         *slog.Logger
	ops         *wire.OpsMetrics
	spool       string // build-spool root for the build.log path
	stagingRoot string // allowed prefix for SourcePath (FAAS_GITHUBD_WORK_DIR/build-sources)
	spoolRoot   string // allowed prefix for SourcePath (FAAS_SPOOL_ROOT) — kept for future expansion
}

// stagingPathAllowed reports whether sourcePath is under one of the
// allowed roots after Clean. The unix-socket DAC is the only auth in
// v1.0 (ADR-015), but allowing arbitrary host paths on a deployment
// row would be a foot-gun for a future caller that runs on the same
// box. The check is belt-and-suspenders: githubd's staging step
// already writes under <FAAS_GITHUBD_WORK_DIR>/build-sources/, so a
// legitimate call lands under stagingRoot; a malicious caller would
// need to forge a path that prefix-matches an operator-controlled
// directory. Empty roots disable the check (test-only).
func (g *githubdBridge) stagingPathAllowed(sourcePath string) bool {
	clean := filepath.Clean(sourcePath)
	for _, root := range g.allowedStagingRoots() {
		if root == "" {
			continue
		}
		rootClean := filepath.Clean(root)
		// Must be the root itself or a strict descendant.
		// Trailing separator anchors the prefix so /var/lib/faas/githubd
		// does not match /var/lib/faas/githubd-evil.
		if clean == rootClean {
			return true
		}
		if strings.HasPrefix(clean, rootClean+string(filepath.Separator)) {
			return true
		}
	}
	return false
}

func (g *githubdBridge) allowedStagingRoots() []string {
	return []string{g.stagingRoot, g.spoolRoot}
}

// maxSourceBytes caps the per-app tarball at 256 MB — the App-layer
// quota for the Free plan (pkg/api/limits.go). Larger plans can ship
// up to 2 GB (Scale); the limit check is per-app below.
const githubdBridgeMaxSourceBytes = 2 << 30 // 2 GB upper bound (Scale plan)

// minSourceBytes guards against a 0-byte tarball that bug-fixed
// staging would land. Below this we reject — the source is unreadable
// or the staging step silently produced an empty file.
const githubdBridgeMinSourceBytes = 1

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
	if req.DeploymentScope != "" {
		if prob := api.ValidateScope(req.DeploymentScope); prob != nil {
			return nil, status.Errorf(codes.InvalidArgument, "EnqueueBuild: invalid deployment_scope %q", req.DeploymentScope)
		}
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

	// Source-path allowlist (issue #432 phase 5 review follow-up):
	// the unix-socket DAC is the only auth in v1.0 (ADR-015), but
	// a forged call to this bridge could otherwise stamp any
	// host-readable path onto the deployment row's source_path.
	// builderd's detector (pkg/builderd/detect.go:48) opens the
	// path that lands here. githubd's staging step writes under
	// <FAAS_GITHUBD_WORK_DIR>/build-sources/<account>/<app>/<sha>/;
	// a legitimate call lands under that prefix. The check uses
	// filepath.Clean + a separator-anchored prefix so a sibling
	// directory like /var/lib/faas/githubd-evil does not match.
	// Returned as InvalidArgument (not PermissionDenied) so a
	// legitimate misconfiguration surfaces as a clear "wrong root"
	// rather than a silently-debuggable 403.
	if !g.stagingPathAllowed(req.SourcePath) {
		return nil, status.Errorf(codes.InvalidArgument,
			"EnqueueBuild: source_path %q is not under staging root %q (or spool %q)",
			req.SourcePath, g.stagingRoot, g.spoolRoot)
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

	// The shared apidsource.Enqueue helper handles CreateDeployment +
	// build.log spool + UpdateDeploymentStatus(building) + CreateBuild +
	// NotifyBuildQueued + (optional) NotifyDeploymentChanged (supersede).
	// The bridge keeps its own auth preamble above (DAC + app/account/
	// active gate + source-path allowlist + on-disk stat/size check);
	// the helper takes over once those gates pass.
	//
	// The kind is hard-coded to DeploymentKindGitHub so ADR-048
	// metering can split githubd-triggered builds from apid-side
	// tarball/deploy builds. The previous non-terminal deployment
	// row is superseded inside store.CreateDeployment's tx
	// (pkg/state/pgstore.go::CreateDeployment — the in-tx
	// supersede is a Phase 5 PR-B feature).
	//
	// SourceURL carries the upstream archive URL (provenance-only;
	// builderd reads source_path, not source_url — see
	// pkg/builderd/builderd.go:644). CommitSHA is the upstream
	// commit (deployments_commit_sha_len_chk, migrations/00047).
	//
	// Issue #606 / SAFE-RELEASES-E.1: the Pusher field is now
	// threaded through apidsource.EnqueueParams →
	// store.CreateDeployment onto the deployments.pusher_login +
	// deployed_via="github" + deployed_by_user_id columns. The FK
	// on deployed_by_user_id points at the app's owning account
	// (req.AccountId) — we deliberately do NOT resolve
	// Pusher.Login → a local account here, because a single GH
	// org install can map to many local accounts (PR #291 mirror
	// surface) and the resolution is eventually-consistent on the
	// GH side. The pusher's identity stays attributable via the
	// pusher_login column, distinct from the local-account FK.
	//
	// Ref / Branch / RepoFullName remain intentionally NOT in
	// pkg/state.Deployment — the proto carries them for forward-
	// compat and the audit log (g.log.Info below) carries them
	// on the build_enqueued line.
	//
	// Issue #977 / ADR-116: Pusher + SenderLogin + PullRequestNumber
	// stamp the annotation surface onto the deployment row. We
	// prefer SenderLogin over Pusher as the deployed_by value when
	// present (it's the actor who triggered the webhook — for
	// pull_request events, that's the PR opener). Fall back to
	// Pusher when SenderLogin is empty (push events, pre-feature
	// builds, older githubd). PullRequestNumber is forwarded only
	// when > 0; push events and pre-feature builds pass 0, which
	// the pgstore nullif(0) collapse maps to NULL.
	//
	// Kind dispatch (issue #272 / ADR-094): the proto3 EnqueueBuild
	// carries an event_kind enum (push vs pull_request). Push events
	// keep stamping DeploymentKindGitHub (ADR-048 metering keys on
	// this); pull_request events stamp DeploymentKindPreview so the
	// preview-app builds are separable from production pushes in
	// metered reports + audit logs. EVENT_KIND_UNSPECIFIED falls
	// through to DeploymentKindGitHub so the pre-issue-#272 wire
	// shape stays binary-compatible (older githubd builds + the
	// slice-7 test fixtures don't set the field).
	deployedBy := req.SenderLogin
	if deployedBy == "" {
		deployedBy = req.Pusher
	}
	kind := eventKindToDeploymentKind(req.EventKind)
	res, err := apidsource.Enqueue(ctx, g.store, g.notif, apidsource.EnqueueParams{
		AppID:       app.ID,
		DeliveryID:  req.DeliveryId,
		Kind:        kind,
		SourcePath:  req.SourcePath,
		SourceBytes: req.SourceBytes,
		SourceURL:   req.SourceUrl,
		CommitSHA:   req.CommitSha,
		Scope:       req.DeploymentScope,
		LogSpool:    g.spool,
		Log:         g.log,
		// Issue #606 / SAFE-RELEASES-E.1: bridge-side actor
		// attribution. ActorVia is hard-coded to "github"
		// (the closed-set CHECK on deployments.deployed_via
		// enforces this); ActorUserID points at the app's
		// owning local account (req.AccountId — already
		// validated against app.AccountID at line 199 above,
		// so the FK insertion cannot fail on a missing
		// account row); ActorFromIP is the bridge daemon's
		// listen socket — unix-socket, loopback by
		// construction (the proto carries no per-request
		// IP); ActorPusherLogin is the raw GH login from
		// req.Pusher, suitable for downstream GitHub-API
		// correlation.
		ActorUserID:      req.AccountId,
		ActorVia:         "github",
		ActorFromIP:      "127.0.0.1",
		ActorPusherLogin: req.Pusher,
		// Issue #977 / ADR-116: annotation surface forwarded onto
		// the deployment row. DeployedBy prefers SenderLogin (the
		// actor who triggered the webhook — for pull_request events,
		// the PR opener) and falls back to Pusher for push events
		// and pre-feature builds. PRNumber is forwarded as int; the
		// pgstore nullif(0) collapse maps 0 to NULL.
		DeployedBy: deployedBy,
		PRNumber:   int(req.PullRequestNumber),
	})
	if err != nil {
		return nil, g.asGRPC("enqueue", err)
	}

	// §12 metric: apid_githubd_bridge_enqueued_total. The
	// accessor is nil-receiver safe; when ops is nil (test path)
	// the metric stays zero. Issue #272 / ADR-094: the label
	// reflects the resolved deployment kind (push → github,
	// pull_request → preview), so the dashboard can split
	// preview traffic from production-push traffic.
	g.ops.IncGithubdBridgeEnqueued(deploymentKindToWireLabel(kind))

	g.log.Info("githubd bridge: build enqueued",
		"build", res.BuildID, "deployment", res.DeploymentID, notifyAppField, app.ID,
		"commit_sha", req.CommitSha, "repo", req.RepoFullName, "branch", req.Branch)
	return &githubdpb.EnqueueBuildResponse{
		BuildId:      res.BuildID,
		DeploymentId: res.DeploymentID,
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

// eventKindToDeploymentKind maps the proto3 EnqueueBuildEventKind
// enum (api/proto/onebox/faas/githubd/v1/githubd.proto) to the
// corresponding state.DeploymentKind stamp on the deployment row.
//
// Mapping:
//
//   - EVENT_KIND_UNSPECIFIED → DeploymentKindGitHub (legacy; the
//     pre-PR-A wire shape didn't carry this field at all, so
//     "unset" is treated as "push" for back-compat)
//   - EVENT_KIND_PUSH        → DeploymentKindGitHub
//   - EVENT_KIND_PULL_REQUEST→ DeploymentKindPreview (issue #272 / ADR-094)
//
// Unknown enum values fall through to DeploymentKindGitHub;
// the closed-set switch in IncGithubdBridgeEnqueued drops the
// metric increment in that case so a future enum value won't
// silently inflate the production-push counter.
func eventKindToDeploymentKind(k githubdpb.EnqueueBuildEventKind) state.DeploymentKind {
	switch k {
	case githubdpb.EnqueueBuildEventKind_EVENT_KIND_PULL_REQUEST:
		return state.DeploymentKindPreview
	default:
		// Unspecified + Push + unknown all → GitHub. The
		// distinction between "legacy unset" and "explicit push"
		// doesn't matter for the kind stamp; both produce the
		// same downstream row.
		return state.DeploymentKindGitHub
	}
}

// deploymentKindToWireLabel maps the resolved deployment kind
// to the wire/metric label set used by IncGithubdBridgeEnqueued.
// Mirrors the constants in pkg/wire/metrics.go (GithubdBridgeKind*).
func deploymentKindToWireLabel(k state.DeploymentKind) string {
	switch k {
	case state.DeploymentKindPreview:
		return wire.GithubdBridgeKindPreview
	default:
		return wire.GithubdBridgeKindGitHub
	}
}

// registerGithubdBridge binds the GithubdServer (only EnqueueBuild
// is implemented; the rest is UnimplementedGithubdServer) onto a
// gRPC server. Called from runGithubdBridgeServer in main.go
// alongside the HTTP server lifecycle.
//
// stagingRoot is the githubd-side workdir (FAAS_GITHUBD_WORK_DIR,
// default /var/lib/faas/githubd). The staging path appended by
// githubd's dispatcher is <stagingRoot>/build-sources/<account>/
// <app>/<sha>/source.tar.gz — that's the prefix the handler
// allowlist-checks req.SourcePath against. Empty stagingRoot
// disables the check (test-only path; production wiring MUST set
// it).
func registerGithubdBridge(s *grpc.Server, store githubdBridgeStore, notif githubdBridgeNotifier, log *slog.Logger, ops *wire.OpsMetrics, spool string, stagingRoot string) {
	githubdpb.RegisterGithubdServer(s, &githubdBridge{
		store:       store,
		notif:       notif,
		log:         log,
		ops:         ops,
		spool:       spool,
		stagingRoot: filepath.Join(stagingRoot, "build-sources"),
		spoolRoot:   spool,
	})
}
