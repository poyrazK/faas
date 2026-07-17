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
	// functionRunnerPath is the absolute path to the static
	// guest/runners/<runtime>/faas-runner binary injected into function
	// layers. Empty in tests; cmd/imaged wires this from config.
	functionRunnerPath string
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

// WithFunctionRunnerPath returns the handler with the runner binary path
// set. Wired from cmd/imaged when the function runner has been compiled.
func (h *Handler) WithFunctionRunnerPath(p string) *Handler {
	h.functionRunnerPath = p
	return h
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

// handleDeployment advances a deployment up to the point where a snapshot
// is needed. Two paths:
//
//   - kind=image + app.Type=app    → pull OCI digest, build app-layer ext4.
//   - kind=image + app.Type=function → apply customer's source tarball +
//                                     copy the function-runner binary
//                                     into the layer; the manifest
//                                     points at the runner.
//
// Both paths share the same imaging→snapshotting→live handshake via
// snapshot_prime (ADR-018). Tarball/dockerfile deployments start via
// build_queued and skip this function.
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

	// Branch on app type. Functions take a different path: the customer
	// uploads a source tarball; the runner binary lives in the layer
	// alongside it; the manifest points at the runner so guest-init execs
	// the right interpreter on wake. Apps use the OCI image path.
	switch app.Type {
	case state.AppTypeFunction:
		if err := h.buildFunctionLayer(ctx, app, dep, acct); err != nil {
			return err
		}
	default:
		if err := h.buildImageLayer(ctx, app, dep, acct); err != nil {
			return err
		}
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

// buildImageLayer is the app-deploy path (app.Type == AppTypeApp):
// pull the OCI image, build the app-layer ext4, stamp
// SetDeploymentRootfs. PullImageConfig runs first as a cheap fail-fast
// (review issue #6 — a no-Cmd image shouldn't trigger dozens of MB of
// layer pulls); PullLayers streams the blobs only after validation
// succeeds. The per-deploy Handler override wins over the image's Cmd,
// per the deploy contract.
func (h *Handler) buildImageLayer(ctx context.Context, app state.App, dep state.Deployment, acct state.Account) error {
	digest, err := h.oci.PullDigest(ctx, dep.ImageDigest)
	if err != nil {
		_ = h.transition(ctx, dep.ID, state.DeployFailed, "oci pull failed: "+err.Error())
		return fmt.Errorf("imaged: oci pull: %w", err)
	}

	if err := h.transition(ctx, dep.ID, state.DeployImaging, ""); err != nil {
		return err
	}

	imageCfg, err := h.oci.PullImageConfig(ctx, digest)
	if err != nil {
		_ = h.transition(ctx, dep.ID, state.DeployFailed, "oci pull config: "+err.Error())
		return fmt.Errorf("imaged: pull image config: %w", err)
	}

	manifest := manifestFromImageConfig(imageCfg)
	if dep.Handler != "" {
		manifest.Entrypoint = []string{dep.Handler}
	}
	if err := manifest.Validate(); err != nil {
		_ = h.transition(ctx, dep.ID, state.DeployFailed, "manifest invalid: "+err.Error())
		return fmt.Errorf("imaged: validate manifest: %w", err)
	}

	pulled, err := h.oci.PullLayers(ctx, digest)
	if err != nil {
		_ = h.transition(ctx, dep.ID, state.DeployFailed, "oci pull layers: "+err.Error())
		return fmt.Errorf("imaged: pull layers: %w", err)
	}
	defer func() {
		for _, r := range pulled.Layers {
			_ = r.Close()
		}
	}()

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

	if err := h.store.SetDeploymentRootfs(ctx, dep.ID, result.ImagePath, result.ContentBytes); err != nil {
		_ = h.transition(ctx, dep.ID, state.DeployFailed, "stamp rootfs: "+err.Error())
		return fmt.Errorf("imaged: stamp rootfs: %w", err)
	}
	return nil
}

// buildFunctionLayer assembles a function deploy's app-layer ext4:
//
//  1. Apply the customer's source tarball at /app.
//  2. Copy the function runner binary at /usr/local/bin/faas-runner.
//  3. Stamp /etc/faas/app.json with the §4.9 manifest pointing at the
//     runner.
//
// The runner binary is injected from a path the daemon config provides
// (cmd/imaged wires this). For tests, FunctionRunnerPath is empty and
// the path is treated as a no-op so the table test can exercise the
// rest of the path without an actual binary.
func (h *Handler) buildFunctionLayer(ctx context.Context, app state.App, dep state.Deployment, acct state.Account) error {
	if err := h.transition(ctx, dep.ID, state.DeployImaging, ""); err != nil {
		return err
	}
	runtime := app.Runtime
	if runtime == "" {
		// Fall back to the per-deploy handler field when the app row
		// doesn't carry the runtime — keeps older clients working.
		runtime = dep.Handler
	}
	if runtime != "node22" && runtime != "python312" {
		_ = h.transition(ctx, dep.ID, state.DeployFailed, "unsupported runtime: "+runtime)
		return fmt.Errorf("imaged: unsupported function runtime %q", runtime)
	}
	manifest := api.AppManifest{
		Port:    api.DefaultAppPort,
		Healthz: "/healthz",
		Entrypoint: []string{
			"/usr/local/bin/faas-runner",
			"--runtime", runtime,
			"--handler", "/app/" + runtime + ".js", // python312 uses handler.py; node22 uses node22.js
		},
	}
	if runtime == "python312" {
		manifest.Entrypoint = []string{
			"/usr/local/bin/faas-runner",
			"--runtime", runtime,
			"--handler", "/app/handler.py",
		}
	}
	if err := manifest.Validate(); err != nil {
		_ = h.transition(ctx, dep.ID, state.DeployFailed, "manifest invalid: "+err.Error())
		return fmt.Errorf("imaged: validate manifest: %w", err)
	}

	outImage := filepath.Join(h.appsRoot, app.Slug, dep.ID+".ext4")
	result, err := h.builder.Build(ctx, rootfs.BuildInput{
		Layers:        layersAsReaders(nil), // function deploys use the tarball via BuildInput.Tarball
		Manifest:      manifest,
		GuestInitPath: h.guestInitPath,
		Plan:          acct.Plan,
		OutImage:      outImage,
		// TarballPath lets the rootfs.Builder stream the customer's
		// source tarball into /app during layer assembly. Tests skip
		// this by leaving TarballPath empty.
		TarballPath: dep.SourcePath,
		// FunctionRunnerPath is the static guest/runners/<rt>/faas-runner
		// binary that lives at /usr/local/bin/faas-runner in the layer.
		FunctionRunnerPath: h.functionRunnerPath,
	})
	if err != nil {
		_ = h.transition(ctx, dep.ID, state.DeployFailed, "build function layer: "+err.Error())
		return fmt.Errorf("imaged: build function layer: %w", err)
	}
	if err := h.store.SetDeploymentRootfs(ctx, dep.ID, result.ImagePath, result.ContentBytes); err != nil {
		_ = h.transition(ctx, dep.ID, state.DeployFailed, "stamp rootfs: "+err.Error())
		return fmt.Errorf("imaged: stamp rootfs: %w", err)
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
