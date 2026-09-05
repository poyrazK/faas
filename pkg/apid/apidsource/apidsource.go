// Package apidsource centralises the "create deployment + build + notify"
// flow that every apid-side source deploy path needs.
//
// Three callers today all do roughly the same dance:
//
//   - cmd/apid/deploy_inputs.go::createDeploymentMultipart — the
//     customer-facing multipart upload (kind=tarball|dockerfile).
//   - cmd/apid/githubd_bridge.go::EnqueueBuild — the githubd → apid
//     gRPC bridge for push-triggered builds (kind=github).
//   - the post-ADR-050 provision apply path — reposcan decomposes a
//     repo into N workloads and each workload becomes one source
//     deploy (kind=tarball). This is the gap PR-A in the mega-PR
//     closes; the apply loop calls Enqueue in a per-app loop and
//     keeps partial-failure semantics.
//
// The shared ~40-line sequence is:
//
//  1. Read LatestDeployment so the prior row's id can ride the
//     supersede notify.
//  2. CreateDeployment with the caller-supplied Kind/SourcePath/
//     SourceBytes/SourceURL/CommitSHA/Handler.
//  3. Create the build.log spool dir + an empty file so builderd
//     can write to it before the row flips to building.
//  4. UpdateDeploymentStatus(deployment_id, DeployBuilding, "") so
//     dashboards see the in-flight state.
//  5. CreateBuild with the same kind + source_bytes.
//  6. Notify(build_queued) — best-effort; the durable net is the
//     build row + ClaimNextQueuedBuild.
//  7. Notify(deployment_changed, status=superseded) if a prior row
//     existed — imaged's F5-cleanup handler drops the prior snapshot.
//
// Each caller keeps its own auth preamble (HTTP session + scope vs
// unix-socket DAC vs reposcan/apply ACLs) and its own error mapping
// (RFC 7807 vs gRPC asGRPC vs reposcan Problem). The helper returns
// plain wrapped errors so callers can decide how to surface them.
//
// The Store + Notifier interfaces here are the canonical minimal
// slice for the deploy+build flow; cmd/apid/githubd_bridge.go
// defines an equivalent local interface for its unit-test seam.
// pkg/state.Store satisfies Store via structural typing; the
// apid-side cmd/apid/pgNotifier satisfies Notifier structurally.
package apidsource

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/storage"
)

// Store is the minimal state.Store surface the deploy+build flow
// needs. Mirrors cmd/apid/githubd_bridge.go::githubdBridgeStore —
// the bridge's interface set was already the canonical seam for
// this flow (state.Store satisfies it structurally). The helper
// does not import state.Store directly to keep the seam pointable
// in tests and to make the dependency one-way.
type Store interface {
	LatestDeployment(ctx context.Context, appID string) (state.Deployment, error)
	CreateDeployment(ctx context.Context, d state.Deployment) (state.Deployment, error)
	UpdateDeploymentStatus(ctx context.Context, id string, status state.DeploymentStatus, logPath string) error
	CreateBuild(ctx context.Context, deploymentID string, kind state.DeploymentKind, sourceBytes int64, logPath string) (state.Build, error)
}

// Notifier is the minimal pg_notify surface the deploy+build flow
// needs. Mirrors cmd/apid/githubd_bridge.go::githubdBridgeNotifier.
// The cmd/apid pgNotifier satisfies it structurally.
type Notifier interface {
	Notify(ctx context.Context, channel, payload string) error
}

// EnqueueParams carries everything the deploy+build flow needs to
// produce one (deployment, build) pair + the two notifications.
//
// Fields map 1:1 onto state.Deployment + the bridge's proto:
//
//	Kind        — state.DeploymentKind (image|tarball|dockerfile|github).
//	              Provision flows use tarball per the Plan (§3.4.2 —
//	              tarball is in both deployments_kind_check and
//	              builds_kind_check).
//	SourcePath  — absolute path to the staged tarball on disk.
//	              builderd reads it directly; the path must be
//	              readable by the builderd process user.
//	SourceBytes — declared size of the tarball (cross-checked by
//	              the githubd bridge; the apid tarball path uses
//	              the value written by validateAndSpool).
//	SourceURL   — provenance-only; builderd reads SourcePath not
//	              this. Empty for the apid tarball path; populated
//	              by the githubd bridge with the upstream archive URL.
//	CommitSHA   — upstream commit SHA when known. Empty for the
//	              apid tarball path; populated by the githubd bridge.
//	Handler     — function handler when Type=function. Empty for
//	              all other paths.
//	Source      — the JSON `"source"` payload value (the kind of
//	              source that triggered the build). Optional: when
//	              empty, the helper derives it from Kind so the
//	              payload stays consistent across callers. Pass a
//	              non-empty value only to preserve a legacy
//	              wire-contract quirk (none today; field is
//	              derived-from-Kind by default).
//	LogSpool    — absolute path to the build-spool root. The helper
//	              writes <LogSpool>/<deployment_id>/build.log. Same
//	              value as cmd/apid.deployInputs.spoolRoot() and
//	              cmd/apid/githubdBridge.spool.
//	Log         — slog.Logger. Required (must not be nil).
//
// The four `Actor*` fields are the issue #606 / SAFE-RELEASES-E.1
// server-stamped attribution columns (deployed_by_user_id,
// deployed_via, deployed_from_ip, pusher_login). The closed-set
// vocabulary for DeployedVia is enforced at the schema layer via
// the migrations/00303_deployments_actor.sql CHECK constraint;
// pgstore.CreateDeployment's INSERT path coalesces empty strings
// to NULL/” via the nullif()+coalesce() chain. Empty actor fields
// on EnqueueParams render pre-#606 rows unchanged.
//
// DeployedByUserID is the deploying account's UUID (FK →
// accounts.id, ON DELETE SET NULL). The apid tarball / dockerfile
// paths populate it from session-resolved acct.ID; the githubd
// bridge path resolves it from req.Pusher → local account via the
// existing GH install→account helper (cmd/apid/handlers_github.go).
//
// DeployedVia is the closed-set classifier of how this deployment
// was submitted. The apid paths compute it from the HTTP request
// shape via cmd/apid.deploy_actor.routeKindForRequest; the bridge
// stamps "github" directly.
//
// DeployedFromIP is the trusted remote IP captured by
// pkg/middleware.ClientIP at handler entry (the githubd bridge
// path stamps the daemon's local IP since the proto carries no
// per-request IP).
//
// PusherLogin is the raw GitHub login of the pusher when
// DeployedVia == "github". Empty for all other via values. The
// bridge stamps it from req.Pusher.Login; the apid paths leave
// it empty.
type EnqueueParams struct {
	AppID       string
	Kind        state.DeploymentKind
	SourcePath  string
	SourceBytes int64
	SourceRoot  string
	SourceURL   string
	CommitSHA   string
	Handler     string
	Source      string
	LogSpool    string
	Log         *slog.Logger
	// Issue #606 / SAFE-RELEASES-E.1 actor columns. ActorVia
	// must be one of the closed-set values enforced by the
	// deployments.deployed_via CHECK constraint; ActorUserID
	// is the local-account FK (the app's owning account for
	// the githubd bridge path); ActorFromIP is the trusted
	// remote IP captured at handler entry; ActorPusherLogin
	// is the raw GitHub login when DeployedVia == "github".
	ActorUserID      string
	ActorVia         string
	ActorFromIP      string
	ActorPusherLogin string
	// Annotation fields (issue #977 / ADR-116). Optional;
	// empty/zero values mean "no annotation" and the pgstore
	// collapses them to NULL on the row. PRNumber=0 → NULL via
	// nullif(0) at the INSERT.
	Reason     string
	Tag        string
	DeployedBy string
	PRNumber   int
	// Workflows is the validated definition set carried by a multipart source
	// deploy and stored with the deployment for run snapshotting.
	Workflows json.RawMessage
}

// EnqueueResult is the durable artifact the caller writes back to
// the client. Both DeploymentID and BuildID are always non-empty on
// success; the caller can shape them however the wire contract
// needs (REST JSON, gRPC response, reposcan response body).
type EnqueueResult struct {
	DeploymentID string
	BuildID      string
}

// sourceBackendFromEnv enables the split-box source handoff only when the
// deployment explicitly selects the OCI backend. Single-box/local installs
// keep the historical shared filesystem contract and do not upload a second
// copy of the source archive.
func sourceBackendFromEnv() (storage.StorageBackend, error) {
	if os.Getenv("FAAS_STORAGE_BACKEND") != "oci" {
		return nil, nil
	}
	be, err := storage.BackendFromEnv()
	if err != nil {
		return nil, fmt.Errorf("source storage: %w", err)
	}
	return be, nil
}

func publishSource(ctx context.Context, be storage.StorageBackend, buildID, path string) error {
	if be == nil {
		return nil
	}
	// SourcePath is a server-created spool path, not customer-supplied input;
	// the apid upload/github bridge has already validated and materialized it.
	//nolint:forbidigo // vetted server-created source spool path.
	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open source archive: %w", err)
	}
	defer func() { _ = f.Close() }()
	if err := be.Put(ctx, "sources/"+buildID+".tar.gz", f); err != nil {
		return fmt.Errorf("publish source archive: %w", err)
	}
	return nil
}

// Enqueue runs the canonical "create deployment + build + notify"
// flow described in this file's header.
//
// Returns wrapped errors that callers may map to their own wire
// shape (RFC 7807 / gRPC / reposcan). The two Notify calls are
// best-effort and never bubble up; the build row is durable and
// builderd's poll-recovery files missing notifies.
//
// On success, EnqueueResult.DeploymentID and BuildID are populated.
// On failure the deployment row may or may not have been written —
// state.Store.CreateDeployment is its own transaction. The caller
// should treat the error as "nothing was durably enqueued" and let
// the caller decide whether to retry or skip-and-continue (the
// provision apply path does the latter; the githubd bridge does the
// former).
//
// Enqueue never deletes the staged SourcePath. The caller owns that
// file's lifetime — the apid tarball path lets builderd GC it after
// the build completes, the githubd bridge overwrites in place per
// commit, and the provision apply path stages under
// <FAAS_SPOOL_ROOT>/projects/<acct>/<project>/<appID>.tar.gz (see
// cmd/apid/scan_service.go + apply helper).
func Enqueue(ctx context.Context, store Store, notif Notifier, p EnqueueParams) (EnqueueResult, error) {
	if p.Log == nil {
		return EnqueueResult{}, fmt.Errorf("apidsource.Enqueue: log is required")
	}
	if p.LogSpool == "" {
		return EnqueueResult{}, fmt.Errorf("apidsource.Enqueue: LogSpool is required")
	}
	if p.AppID == "" {
		return EnqueueResult{}, fmt.Errorf("apidsource.Enqueue: AppID is required")
	}
	if p.Kind == "" {
		return EnqueueResult{}, fmt.Errorf("apidsource.Enqueue: Kind is required (got empty; check state.DeploymentKind)")
	}
	if p.SourcePath == "" {
		return EnqueueResult{}, fmt.Errorf("apidsource.Enqueue: SourcePath is required")
	}
	sourceStorage, err := sourceBackendFromEnv()
	if err != nil {
		return EnqueueResult{}, fmt.Errorf("apidsource.Enqueue: %w", err)
	}

	// Step 1: read prior deployment so the supersede notify can
	// carry the right deployment_id.
	prev, _ := store.LatestDeployment(ctx, p.AppID)

	// Step 2: create the deployment row. SourceURL + CommitSHA are
	// provenance-only on the apid tarball path; the bridge sets them.
	// The four Actor* fields (issue #606 / SAFE-RELEASES-E.1) are
	// server-stamped here — the pgstore INSERT path coalesces empty
	// strings to NULL/'' via the migrations/00303 nullif()+coalesce()
	// chain, so pre-#606 callers that don't pass actor fields render
	// identical wire shapes.
	d, err := store.CreateDeployment(ctx, state.Deployment{
		AppID:       p.AppID,
		Kind:        p.Kind,
		SourcePath:  p.SourcePath,
		SourceBytes: p.SourceBytes,
		SourceRoot:  p.SourceRoot,
		SourceURL:   p.SourceURL,
		CommitSHA:   p.CommitSHA,
		Handler:     p.Handler,
		Status:      state.DeployPending,
		// Issue #606 / SAFE-RELEASES-E.1: actor columns propagated
		// onto the deployment row at INSERT time. The pgstore
		// nullString helper collapses "" → NULL for the nullable
		// text/INET columns.
		DeployedByUserID: p.ActorUserID,
		DeployedVia:      p.ActorVia,
		DeployedFromIP:   p.ActorFromIP,
		PusherLogin:      p.ActorPusherLogin,
		// Issue #977 / ADR-116: stamp the annotation surface from
		// the upstream caller (CLI multipart, JSON body, githubd
		// bridge). pgstore.CreateDeployment collapses "" to NULL
		// via nullString and PRNumber=0 to NULL via nullif(0).
		Reason:     p.Reason,
		Tag:        p.Tag,
		DeployedBy: p.DeployedBy,
		PRNumber:   p.PRNumber,
		Workflows:  append(json.RawMessage(nil), p.Workflows...),
	})
	if err != nil {
		return EnqueueResult{}, fmt.Errorf("apidsource.Enqueue: create deployment: %w", err)
	}

	// Step 3: build.log spool. Same shape as cmd/apid/deploy_inputs.go
	// and cmd/apid/githubd_bridge.go — the helper does not own the
	// choice of root, only where to drop the file under it.
	logDir := filepath.Join(p.LogSpool, d.ID)
	if err := os.MkdirAll(logDir, 0o755); err != nil {
		// The deployment row already exists; the caller decides
		// whether to treat this as fatal (apid tarball does) or
		// continue (the bridge logs + continues, since builderd
		// can still write to a path it creates on demand).
		p.Log.Warn("apidsource.Enqueue: mkdir log spool (builderd will create on demand)",
			"deployment", d.ID, "app", p.AppID, "dir", logDir, "err", err)
	} else {
		logPath := filepath.Join(logDir, "build.log")
		if _, err := os.Create(logPath); err != nil {
			p.Log.Warn("apidsource.Enqueue: create build.log (builderd will create on demand)",
				"deployment", d.ID, "app", p.AppID, "path", logPath, "err", err)
		}
	}

	// Step 4: flip to 'building' so dashboards see in-flight. The
	// pre-helper apid code ignored the UpdateDeploymentStatus
	// error (PR-B comment at deploy_inputs.go:196-200); we
	// preserve that — the next step (CreateBuild) will surface
	// the actual durable-state failure.
	_ = store.UpdateDeploymentStatus(ctx, d.ID, state.DeployBuilding, "")

	// Step 5: create the build row. Same kind as the deployment;
	// builderd's railpack/dockerfile/tarball detector picks the
	// pipeline at build time.
	build, err := store.CreateBuild(ctx, d.ID, p.Kind, p.SourceBytes, filepath.Join(logDir, "build.log"))
	if err != nil {
		return EnqueueResult{}, fmt.Errorf("apidsource.Enqueue: create build: %w", err)
	}

	// Split-box source handoff. The build row carries the local source path
	// for compatibility, while compute-side builderd retrieves the same
	// archive from the shared OCI namespace keyed by the durable build ID.
	// Publish before the NOTIFY so a fast builderd cannot claim a row before
	// its source is visible remotely. A failed publish deliberately leaves the
	// row queued; the durable builder worker can retry after a transient
	// registry or source-spool failure.
	if err := publishSource(ctx, sourceStorage, build.ID, p.SourcePath); err != nil {
		p.Log.Warn("apidsource.Enqueue: source handoff failed (build remains queued for retry)",
			"build", build.ID, "deployment", d.ID, "app", p.AppID, "err", err)
		return EnqueueResult{}, fmt.Errorf("apidsource.Enqueue: source handoff: %w", err)
	}

	// Resolve the wire "source" field. Default to Kind so the
	// build_queued payload's `source` field tracks `kind` and
	// dashboards split-by-source stay consistent across the three
	// callers. An explicit Source is honored for legacy wire-
	// contract quirks (none today; the field is derivable from
	// Kind for every caller).
	source := p.Source
	if source == "" {
		source = string(p.Kind)
	}

	// Step 6: NotifyBuildQueued. Best-effort. builderd's poll-
	// recovery (state.Store.ClaimNextQueuedBuild, FOR UPDATE SKIP
	// LOCKED) files missing notifies.
	queuedPayload, _ := json.Marshal(map[string]any{
		"build":      build.ID,
		"deployment": d.ID,
		"app":        p.AppID,
		"kind":       string(p.Kind),
		"source":     source,
	})
	if err := notif.Notify(ctx, db.NotifyBuildQueued, string(queuedPayload)); err != nil {
		p.Log.Warn("apidsource.Enqueue: notify build_queued (durable recovery will pick it up)",
			"build", build.ID, "deployment", d.ID, "app", p.AppID, "err", err)
	}

	// Step 7: supersede notify for the prior non-terminal row.
	// Skipped on first deploy (no prev).
	if prev.ID != "" {
		supPayload, _ := json.Marshal(map[string]any{
			"kind":          source,
			"status":        "superseded",
			"app_id":        p.AppID,
			"deployment_id": prev.ID,
			"to":            prev.ID,
		})
		if err := notif.Notify(ctx, db.NotifyDeploymentChanged, string(supPayload)); err != nil {
			p.Log.Warn("apidsource.Enqueue: notify superseded (imaged F5 will recover)",
				"app", p.AppID, "prev_deployment", prev.ID, "err", err)
		}
	}

	p.Log.Info("apidsource.Enqueue: build enqueued",
		"deployment", d.ID, "app", p.AppID, "kind", p.Kind, "build", build.ID, "source", source)

	return EnqueueResult{DeploymentID: d.ID, BuildID: build.ID}, nil
}
