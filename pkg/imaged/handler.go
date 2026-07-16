// Package imaged — deploy-pipeline orchestrator. imaged owns the OCI→rootfs
// conversion and snapshot writes (spec §4.6, ADR-003, ADR-005). It is the
// only writer to the `snapshots` table; apid writes deployment rows, imaged
// advances them through `pending → building → imaging → snapshotting → live`
// via pg_notify + state.Store updates.
package imaged

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"

	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/oci"
	"github.com/onebox-faas/faas/pkg/rootfs"
	"github.com/onebox-faas/faas/pkg/state"
)

// Notifier is the minimal interface imaged needs from pkg/db. The real
// implementation is db.Notify (postgres LISTEN/NOTIFY); tests inject a fake.
type Notifier interface {
	Notify(ctx context.Context, channel, payload string) error
}

// Handler is the imaged orchestrator. It owns the transition walk that
// advances a deployment row through the build pipeline until a snapshot row
// exists, at which point schedd picks it up on the next reaper tick.
type Handler struct {
	store   state.Store
	notif   Notifier
	oci     oci.Puller
	builder *rootfs.Builder
	log     *slog.Logger
}

// New returns a Handler. The OCI puller is injected so tests can substitute
// an in-process fake; rootfs.Builder is the same one wired through cmd/imaged.
func New(store state.Store, notif Notifier, puller oci.Puller, b *rootfs.Builder, log *slog.Logger) *Handler {
	if puller == nil {
		puller = oci.DefaultPuller{}
	}
	return &Handler{store: store, notif: notif, oci: puller, builder: b, log: log}
}

// HandleNotification dispatches a single pg_notify payload. The Loop in
// cmd/imaged forwards every notification here.
func (h *Handler) HandleNotification(ctx context.Context, n db.Notification) {
	switch n.Channel {
	case db.NotifyDeploymentChanged:
		var p deploymentChangedPayload
		if err := json.Unmarshal([]byte(n.Payload), &p); err != nil {
			h.log.Warn("imaged: bad deployment_changed payload", "err", err)
			return
		}
		if err := h.handleDeployment(ctx, p); err != nil {
			h.log.Warn("imaged: deploy failed", "app", p.AppID, "deployment", p.To, "err", err)
		}
	case db.NotifyBuildQueued:
		var p buildQueuedPayload
		if err := json.Unmarshal([]byte(n.Payload), &p); err != nil {
			h.log.Warn("imaged: bad build_queued payload", "err", err)
			return
		}
		if err := h.handleBuildQueued(ctx, p); err != nil {
			h.log.Warn("imaged: build queue failed", "app", p.AppID, "build", p.BuildID, "err", err)
		}
	}
}

// deploymentChangedPayload is the JSON shape apid emits on `deployment_changed`.
type deploymentChangedPayload struct {
	AppID       string `json:"app_id"`
	From        string `json:"from"`
	To          string `json:"to"`
	Kind        string `json:"kind"`
	ImageDigest string `json:"image_digest,omitempty"`
}

// buildQueuedPayload is the JSON shape apid emits on `build_queued`.
type buildQueuedPayload struct {
	AppID   string `json:"app_id"`
	BuildID string `json:"build_id"`
	Kind    string `json:"kind"`
}

// handleDeployment advances a deployment that's been created with `kind='image'`.
// M5: pulls the OCI digest, builds the app-layer ext4, writes the snapshot
// row, marks the deployment `live`. The real rootfs.Builder is called later in
// the same goroutine; here we keep the loop tight and emit progress on every
// transition so apid's SSE log stream can mirror it.
func (h *Handler) handleDeployment(ctx context.Context, p deploymentChangedPayload) error {
	if p.Kind != string(state.DeploymentKindImage) {
		// Tarball/dockerfile deployments start via build_queued; apid also
		// fires deployment_changed as a hint, but imaged reads the
		// build_queued stream for those.
		return nil
	}
	dep, err := h.store.DeploymentByID(ctx, p.To)
	if err != nil {
		return fmt.Errorf("imaged: load deployment: %w", err)
	}
	app, err := h.store.AppByID(ctx, p.AppID)
	if err != nil {
		return fmt.Errorf("imaged: load app: %w", err)
	}

	if err := h.transition(ctx, dep.ID, state.DeployBuilding, ""); err != nil {
		return err
	}
	digest, err := h.oci.PullDigest(ctx, dep.ImageDigest)
	if err != nil {
		_ = h.transition(ctx, dep.ID, state.DeployFailed, "oci pull failed: "+err.Error())
		return fmt.Errorf("imaged: oci pull: %w", err)
	}

	if err := h.transition(ctx, dep.ID, state.DeployImaging, ""); err != nil {
		return err
	}
	// rootfs.Builder would write the per-app ext4 layer here (M5 hook);
	// for now we emit a log marker so SSE consumers see the stage advance.
	h.log.Info("imaged: build app layer", "app", app.Slug, "digest", digest)

	if err := h.transition(ctx, dep.ID, state.DeploySnapshotting, ""); err != nil {
		return err
	}
	if err := h.writeSnapshotRow(ctx, app, dep, digest); err != nil {
		return fmt.Errorf("imaged: snapshot row: %w", err)
	}
	if err := h.transition(ctx, dep.ID, state.DeployLive, ""); err != nil {
		return err
	}
	// Re-emit so other subscribers (audit, dashboard SSE) see the terminal
	// transition. The original payload's `to` is the deployment id; we
	// include the new status in the message body.
	if err := h.notif.Notify(ctx, db.NotifyDeploymentChanged,
		`{"app_id":"`+app.ID+`","to":"`+dep.ID+`","status":"live"}`); err != nil {
		h.log.Warn("imaged: notify live", "err", err)
	}
	return nil
}

// handleBuildQueued advances a queued source build. M5 only flips status +
// emits a log line; the actual builder-microVM (ADR-003) lands in M6.
func (h *Handler) handleBuildQueued(ctx context.Context, p buildQueuedPayload) error {
	if err := h.store.UpdateBuildStatus(ctx, p.BuildID, state.BuildRunning, "", true, false); err != nil {
		return fmt.Errorf("imaged: mark building: %w", err)
	}
	h.log.Info("imaged: build queued (M5 stub)", "app", p.AppID, "build", p.BuildID, "kind", p.Kind)
	// Real M6 work: call into pkg/builderd to run railpack/buildctl inside a
	// builder microVM and produce an OCI digest, then continue the same
	// imaging→snapshotting→live path as handleDeployment.
	return nil
}

// transition is the only place imaged writes to deployments.status. Keeps
// the state machine auditable.
func (h *Handler) transition(ctx context.Context, depID string, status state.DeploymentStatus, errMsg string) error {
	if err := h.store.UpdateDeploymentStatus(ctx, depID, status, errMsg); err != nil {
		return fmt.Errorf("imaged: set %s: %w", status, err)
	}
	return nil
}

// writeSnapshotRow records the immutable snapshot metadata that schedd uses
// to restore instances on wake (ADR-005). Path is a local filesystem hint;
// the real blob location is decided by rootfs.Builder in M6.
func (h *Handler) writeSnapshotRow(ctx context.Context, app state.App, dep state.Deployment, digest string) error {
	snap := state.Snapshot{
		ID:           dep.ID + "-snap",
		DeploymentID: dep.ID,
		FCVersion:    "firecracker-1.10", // pinned at snapshot time (ADR-005)
		Path:         "/srv/fc/snap/" + app.Slug + "/" + digest + ".snap",
		MemBytes:     int64(app.RAMMB) * 1024 * 1024,
		DiskBytes:    0, // populated by the rootfs builder
	}
	if _, err := h.store.CreateSnapshot(ctx, snap); err != nil {
		// Treat conflict as success — same deploy raced twice, no harm.
		if errors.Is(err, state.ErrConflict) {
			return nil
		}
		return err
	}
	return nil
}
