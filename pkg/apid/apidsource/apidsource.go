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
// The shared sequence uploads the source under a reserved build ID before
// creating a deployment. CreateBuildWithID then publishes the queue row and
// marks the deployment building in one transaction, guarded against cancel.
// Notifications are best-effort; workers recover from the durable queue.
// An enqueue error marks an unqueued deployment failed when the database is
// reachable, without overwriting cancellation or an uncertain successful commit.
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
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/google/uuid"

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
	FailSourceDeployment(ctx context.Context, id, message string) error
	CreateBuildWithID(ctx context.Context, id, deploymentID string, kind state.DeploymentKind, sourceBytes int64, logPath string) (state.Build, error)
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
	// DeliveryID is the authenticated GitHub webhook delivery ID. When set,
	// Enqueue derives stable deployment/build UUIDs from (delivery, app) and
	// recovers existing rows after retries or ambiguous commit responses.
	DeliveryID string
	// RetryOf preserves the original deployment and copies its input settings.
	// RetryFrom records the requested stage; retained source is rebuilt when
	// intermediate stage checkpoints are unavailable.
	RetryOf       string
	RetryFrom     state.StageName
	SourceBuildID string // original build's immutable source object, for retries
	AppID         string
	Kind          state.DeploymentKind
	SourcePath    string
	SourceBytes   int64
	SourceRoot    string
	SourceURL     string
	CommitSHA     string
	Scope         string
	Handler       string
	Source        string
	LogSpool      string
	Log           *slog.Logger
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

	return enqueueWithSourceStorage(ctx, store, notif, p, sourceStorage)
}

func enqueueWithSourceStorage(ctx context.Context, store Store, notif Notifier, p EnqueueParams, sourceStorage storage.StorageBackend) (EnqueueResult, error) {
	// Publish before creating any durable work. Polling workers can claim a
	// queued row without receiving its notification.
	id, err := uuid.NewV7()
	if err != nil {
		return EnqueueResult{}, fmt.Errorf("apidsource.Enqueue: build ID: %w", err)
	}
	buildID := id.String()
	deploymentID := ""
	if p.DeliveryID != "" {
		deploymentID, buildID = githubDeliveryIDs(p.DeliveryID, p.AppID)
	}
	var sourceErr error
	if p.RetryOf != "" {
		sourceErr = publishRetrySource(ctx, sourceStorage, buildID, p)
	} else {
		sourceErr = publishSource(ctx, sourceStorage, buildID, p.SourcePath)
	}
	if err := sourceErr; err != nil {
		return EnqueueResult{}, fmt.Errorf("apidsource.Enqueue: source handoff: %w", err)
	}

	// Step 1: read prior deployment so the supersede notify can
	// carry the right deployment_id.
	var prev state.Deployment
	if p.RetryOf == "" {
		if scoped, ok := store.(interface {
			LatestDeploymentForScope(context.Context, string, string) (state.Deployment, error)
		}); ok {
			prev, _ = scoped.LatestDeploymentForScope(ctx, p.AppID, p.Scope)
		} else {
			prev, _ = store.LatestDeployment(ctx, p.AppID)
		}
	}

	// Step 2: create the deployment row. SourceURL + CommitSHA are
	// provenance-only on the apid tarball path; the bridge sets them.
	// The four Actor* fields (issue #606 / SAFE-RELEASES-E.1) are
	// server-stamped here — the pgstore INSERT path coalesces empty
	// strings to NULL/'' via the migrations/00303 nullif()+coalesce()
	// chain, so pre-#606 callers that don't pass actor fields render
	// identical wire shapes.
	d, err := createDeployment(ctx, store, p, state.Deployment{
		ID:          deploymentID,
		AppID:       p.AppID,
		Kind:        p.Kind,
		SourcePath:  p.SourcePath,
		SourceBytes: p.SourceBytes,
		SourceRoot:  p.SourceRoot,
		SourceURL:   p.SourceURL,
		CommitSHA:   p.CommitSHA,
		Scope:       p.Scope,
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
		// A deterministic ID conflict means this delivery/app pair crossed
		// the commit boundary during an earlier attempt. Recover the durable
		// row rather than creating duplicate customer work.
		if p.DeliveryID == "" || !errors.Is(err, state.ErrConflict) {
			return EnqueueResult{}, fmt.Errorf("apidsource.Enqueue: create deployment: %w", err)
		}
		idempotent, ok := store.(interface {
			DeploymentByID(context.Context, string) (state.Deployment, error)
		})
		if !ok {
			return EnqueueResult{}, fmt.Errorf("apidsource.Enqueue: recover deployment: store lacks DeploymentByID")
		}
		d, err = idempotent.DeploymentByID(ctx, deploymentID)
		if err != nil {
			return EnqueueResult{}, fmt.Errorf("apidsource.Enqueue: recover deployment %s: %w", deploymentID, err)
		}
		if d.AppID != p.AppID {
			return EnqueueResult{}, fmt.Errorf("apidsource.Enqueue: deployment id collision %s", deploymentID)
		}
		// The original create already performed any supersede transition.
		// Do not emit a false supersede notification for the recovered row.
		prev = state.Deployment{}
	}

	if p.DeliveryID != "" {
		if reader, ok := store.(interface {
			BuildByID(context.Context, string) (state.Build, error)
		}); ok {
			existing, readErr := reader.BuildByID(ctx, buildID)
			switch {
			case readErr == nil && existing.DeploymentID == d.ID:
				return EnqueueResult{DeploymentID: d.ID, BuildID: existing.ID}, nil
			case readErr == nil:
				return EnqueueResult{}, fmt.Errorf("apidsource.Enqueue: build id collision %s", buildID)
			case !errors.Is(readErr, state.ErrNotFound):
				return EnqueueResult{}, fmt.Errorf("apidsource.Enqueue: recover build %s: %w", buildID, readErr)
			}
		}
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
		if f, err := os.Create(logPath); err != nil {
			p.Log.Warn("apidsource.Enqueue: create build.log (builderd will create on demand)",
				"deployment", d.ID, "app", p.AppID, "path", logPath, "err", err)
		} else {
			_ = f.Close()
		}
	}

	// Publish the build and building status together. Same kind as the deployment;
	// builderd's railpack/dockerfile/tarball detector picks the
	// pipeline at build time.
	build, err := store.CreateBuildWithID(ctx, buildID, d.ID, p.Kind, p.SourceBytes, filepath.Join(logDir, "build.log"))
	if err != nil {
		if p.DeliveryID != "" && errors.Is(err, state.ErrConflict) {
			if reader, ok := store.(interface {
				BuildByID(context.Context, string) (state.Build, error)
			}); ok {
				if existing, readErr := reader.BuildByID(ctx, buildID); readErr == nil && existing.DeploymentID == d.ID {
					return EnqueueResult{DeploymentID: d.ID, BuildID: existing.ID}, nil
				}
			}
		}
		// Delivery-backed work remains pending for the durable inbox to
		// retry. Non-webhook callers keep the historical terminal cleanup.
		if p.DeliveryID != "" {
			return EnqueueResult{}, fmt.Errorf("apidsource.Enqueue: create build: %w", err)
		}
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		if cleanupErr := store.FailSourceDeployment(cleanupCtx, d.ID, "create build: "+err.Error()); cleanupErr != nil {
			p.Log.Warn("apidsource.Enqueue: mark source deployment failed", "deployment", d.ID, "err", cleanupErr)
		}
		return EnqueueResult{}, fmt.Errorf("apidsource.Enqueue: create build: %w", err)
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

var githubDeliveryNamespace = uuid.NewSHA1(uuid.NameSpaceURL, []byte("gregale.dev/github-delivery/v1"))

func githubDeliveryIDs(deliveryID, appID string) (deploymentID, buildID string) {
	key := deliveryID + "\x00" + appID
	return uuid.NewSHA1(githubDeliveryNamespace, []byte(key+"\x00deployment")).String(),
		uuid.NewSHA1(githubDeliveryNamespace, []byte(key+"\x00build")).String()
}

func createDeployment(ctx context.Context, store Store, p EnqueueParams, input state.Deployment) (state.Deployment, error) {
	if p.RetryOf == "" {
		return store.CreateDeployment(ctx, input)
	}
	retries, ok := store.(interface {
		RetryDeploymentFromStage(context.Context, string, state.StageName) (state.Deployment, error)
	})
	if !ok {
		return state.Deployment{}, fmt.Errorf("apidsource: store does not support deployment retry")
	}
	return retries.RetryDeploymentFromStage(ctx, p.RetryOf, p.RetryFrom)
}
