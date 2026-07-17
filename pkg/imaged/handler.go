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
	"io"
	"log/slog"
	"path/filepath"

	"github.com/onebox-faas/faas/pkg/api"
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

// LayerBuilder is the slice of rootfs.Builder that imaged uses. Defining it
// here keeps the production *rootfs.Builder seamless while letting tests
// substitute a fake without dragging in a host mkfs binary.
type LayerBuilder interface {
	Build(ctx context.Context, in rootfs.BuildInput) (rootfs.BuildResult, error)
}

// Handler is the imaged orchestrator. It owns the transition walk that
// advances a deployment row through the build pipeline until a snapshot row
// exists, at which point schedd picks it up on the next reaper tick.
type Handler struct {
	store   state.Store
	notif   Notifier
	oci     oci.Puller
	builder LayerBuilder
	log     *slog.Logger

	// guestInitPath is the absolute path to the static guest-init binary
	// injected as /sbin/init in every per-app ext4 (spec §4.8). Wired from
	// cmd/imaged so tests can point at a temp file.
	guestInitPath string
	// appsRoot is the directory under which per-app layer-{deployment}.ext4
	// files are written. Defaults to FAAS_APPS_ROOT or /var/lib/faas/apps.
	appsRoot string
}

// New returns a Handler. The OCI puller is injected so tests can substitute
// an in-process fake; the builder is the same *rootfs.Builder wired through
// cmd/imaged (or a fake for tests). guestInitPath and appsRoot are required:
// guest-init must exist at the path (Builder.Build asserts it), and appsRoot
// must be writable for the production path.
func New(store state.Store, notif Notifier, puller oci.Puller, b LayerBuilder,
	guestInitPath, appsRoot string, log *slog.Logger) *Handler {
	if puller == nil {
		puller = oci.DefaultPuller{}
	}
	return &Handler{
		store: store, notif: notif, oci: puller, builder: b,
		guestInitPath: guestInitPath, appsRoot: appsRoot, log: log,
	}
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
	case db.NotifySnapshotWritten:
		var p snapshotWrittenPayload
		if err := json.Unmarshal([]byte(n.Payload), &p); err != nil {
			h.log.Warn("imaged: bad snapshot_written payload", "err", err)
			return
		}
		if err := h.handleSnapshotWritten(ctx, p); err != nil {
			h.log.Warn("imaged: record snapshot failed", "deployment", p.DeploymentID, "err", err)
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

// snapshotWrittenPayload is the JSON shape schedd emits on `snapshot_written`
// after a Prime/Park writes the blob via vmmd (ADR-018, see pkg/db.NotifyChannels).
// imaged is the sole writer to the snapshots table, so it records the row.
type snapshotWrittenPayload struct {
	DeploymentID string `json:"deployment_id"`
	MemPath      string `json:"mem_path"`
	VMStatePath  string `json:"vmstate_path"`
	MemBytes     int64  `json:"mem_bytes"`
	VMStateBytes int64  `json:"vmstate_bytes"`
	FCVersion    string `json:"fc_version"`
}

// handleDeployment advances a deployment created with `kind='image'` up to the
// point where a snapshot is needed. It pulls the OCI digest, builds the app-layer
// ext4, moves the row to `snapshotting`, and hands off to schedd via a
// `snapshot_prime` notification (ADR-018): schedd cold-boots the layer once,
// snapshots + parks it, and replies with `snapshot_written`, which drives
// handleSnapshotWritten to record the row and flip the deployment `live`. imaged
// never boots a VM or marks the deploy live on its own — that's the handshake.
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
	// Retry/idempotency guard: pg_notify may redeliver; once we've transitioned
	// past `pending` we don't redo the build (the state machine CHECK in
	// UpdateDeploymentStatus would refuse the transition anyway, but a clean
	// early return here avoids loading the deployment row twice).
	if dep.Status != state.DeployPending {
		return nil
	}
	app, err := h.store.AppByID(ctx, p.AppID)
	if err != nil {
		return fmt.Errorf("imaged: load app: %w", err)
	}
	acct, err := h.store.AccountByID(ctx, app.AccountID)
	if err != nil {
		return fmt.Errorf("imaged: load account: %w", err)
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

	// Stream the layer blobs + image config from the registry. Each ReadCloser
	// MUST be closed; we wrap them in io.NopCloser-shaping io.Reader so the
	// rootfs.Builder can apply them as gzip tarballs.
	pulled, err := h.oci.PullLayers(ctx, digest)
	if err != nil {
		_ = h.transition(ctx, dep.ID, state.DeployFailed, "oci pull: "+err.Error())
		return fmt.Errorf("imaged: pull layers: %w", err)
	}
	defer func() {
		for _, r := range pulled.Layers {
			_ = r.Close()
		}
	}()

	// App config (per-field override) wins over image config per the deploy
	// contract. For now the only per-deploy override is Entrypoint, since the
	// deployments table has no structured config column yet — richer overrides
	// arrive with M5.1 / a new manifest jsonb column.
	manifest := manifestFromImageConfig(pulled.Config)
	if dep.Handler != "" {
		manifest.Entrypoint = []string{dep.Handler}
	}
	if err := manifest.Validate(); err != nil {
		_ = h.transition(ctx, dep.ID, state.DeployFailed, "manifest invalid: "+err.Error())
		return fmt.Errorf("imaged: validate manifest: %w", err)
	}

	outImage := filepath.Join(h.appsRoot, app.Slug, dep.ID+".ext4")
	result, err := h.builder.Build(ctx, rootfs.BuildInput{
		Layers:        layersAsReaders(pulled.Layers),
		Manifest:      manifest,
		GuestInitPath: h.guestInitPath,
		Plan:          acct.Plan,
		OutImage:      outImage,
	})
	if err != nil {
		_ = h.transition(ctx, dep.ID, state.DeployFailed, "build app layer: "+err.Error())
		return fmt.Errorf("imaged: build app layer: %w", err)
	}

	// Stamp the row so schedd's cold-boot on snapshot_prime knows where drive1
	// lives (spec §4.6, ADR-018). Stamped AFTER the layer file is written —
	// SetDeploymentRootfs is the source of truth for "this deployment has an
	// ext4 at this path"; the bytes match the file on disk.
	if err := h.store.SetDeploymentRootfs(ctx, dep.ID, result.ImagePath, result.ContentBytes); err != nil {
		_ = h.transition(ctx, dep.ID, state.DeployFailed, "stamp rootfs: "+err.Error())
		return fmt.Errorf("imaged: stamp rootfs: %w", err)
	}

	if err := h.transition(ctx, dep.ID, state.DeploySnapshotting, ""); err != nil {
		return err
	}
	// Hand off to schedd: boot the freshly-built layer once, snapshot it, park
	// it (spec §5 step 6). The deployment stays in `snapshotting` until
	// snapshot_written comes back — imaged does not mark it live here.
	primePayload, _ := json.Marshal(map[string]string{"app_id": app.ID, "deployment_id": dep.ID})
	if err := h.notif.Notify(ctx, db.NotifySnapshotPrime, string(primePayload)); err != nil {
		return fmt.Errorf("imaged: notify snapshot_prime: %w", err)
	}
	return nil
}

// manifestFromImageConfig maps an OCI ImageConfig to an api.AppManifest. The
// Cmd→Entrypoint mapping is per spec §4.6; WorkingDir + Env carry across
// unchanged. Validation is left to AppManifest.Validate so it can emit
// consistent error codes. Per-deploy overrides apply on top of this in
// handleDeployment.
func manifestFromImageConfig(cfg oci.ImageConfig) api.AppManifest {
	manifest := api.AppManifest{
		WorkingDir: cfg.WorkingDir,
		Env:        cloneEnv(cfg.Env),
	}
	if len(cfg.Cmd) > 0 {
		manifest.Entrypoint = append(manifest.Entrypoint, cfg.Cmd...)
	}
	return manifest
}

// cloneEnv returns a defensive copy of the env map. The caller (handleDeployment
// or its caller) may apply per-deploy overrides without mutating the shared
// ImageConfig the puller returned.
func cloneEnv(m map[string]string) map[string]string {
	if len(m) == 0 {
		return nil
	}
	out := make(map[string]string, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

// layersAsReaders returns a fresh []io.Reader borrowing each ReadCloser's
// Read side. The rootfs.Builder consumes via Read; the defer in the caller
// still owns the Close side. Treating the same ReadCloser as both a Reader
// (to Builder) and a Closer (in defer) is the streaming idiom Builder.Build
// already expects — BuildInput.Layers is []io.Reader.
func layersAsReaders(rcs []io.ReadCloser) []io.Reader {
	out := make([]io.Reader, len(rcs))
	for i, rc := range rcs {
		out[i] = rc
	}
	return out
}

// handleSnapshotWritten records the snapshot row schedd's Prime/Park produced and
// flips the deployment `live` (spec §5, ADR-018). imaged is the sole writer to
// the snapshots table, so this is the only place the row is inserted. Idempotent:
// a duplicate emission (same deployment_id) collapses to ErrConflict and the
// deployment is (re-)marked live regardless, so a redelivered notification is safe.
func (h *Handler) handleSnapshotWritten(ctx context.Context, p snapshotWrittenPayload) error {
	if p.DeploymentID == "" {
		return errors.New("imaged: snapshot_written missing deployment_id")
	}
	dep, err := h.store.DeploymentByID(ctx, p.DeploymentID)
	if err != nil {
		return fmt.Errorf("imaged: load deployment: %w", err)
	}

	snap := state.Snapshot{
		DeploymentID: p.DeploymentID,
		FCVersion:    p.FCVersion, // pins restore compatibility (ADR-005)
		Path:         p.MemPath,
		MemBytes:     p.MemBytes,
		DiskBytes:    p.VMStateBytes,
	}
	if _, err := h.store.CreateSnapshot(ctx, snap); err != nil && !errors.Is(err, state.ErrConflict) {
		return fmt.Errorf("imaged: create snapshot: %w", err)
	}

	if err := h.store.MarkDeploymentLive(ctx, dep.ID); err != nil {
		return fmt.Errorf("imaged: mark live: %w", err)
	}
	// Fan out so audit / dashboard SSE see the terminal transition.
	if err := h.notif.Notify(ctx, db.NotifyDeploymentChanged,
		`{"app_id":"`+dep.AppID+`","to":"`+dep.ID+`","status":"live"}`); err != nil {
		h.log.Warn("imaged: notify live", "err", err)
	}
	return nil
}

// handleBuildQueued advances a queued source build. M5 only flips status +
// emits a log line; the actual builder-microVM (ADR-003) lands in M6.
func (h *Handler) handleBuildQueued(ctx context.Context, p buildQueuedPayload) error {
	// failure_class is "" while the build is in-flight; both MemStore and
	// PgStore treat the empty string as "preserve prior value" via a
	// `case when $3 = ''` guard in the UPDATE. There is no FailureNone
	// constant — empty string is the canonical no-class sentinel.
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
