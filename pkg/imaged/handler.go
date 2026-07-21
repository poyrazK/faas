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
	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/storage"
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
	// BuildBase handles the M6 base-image path (spec §4.6 two-drive):
	// assemble ALL layers of a shared read-only base into /srv/fc/base/*.ext4
	// so cold-boot can pass it as drive0. The base pipeline is the inverse
	// of Build: no app manifest injection, no plan cap, every layer applied.
	BuildBase(ctx context.Context, in rootfs.BaseBuildInput) (rootfs.BaseBuildResult, error)
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
	// functionRunnerNode22Path is the absolute path to the static
	// guest/runners/node22/faas-runner binary injected into node22 function
	// layers. Empty in tests; cmd/imaged wires this from FAAS_FUNCTION_RUNNER_NODE22.
	functionRunnerNode22Path string
	// functionRunnerPython312Path mirrors functionRunnerNode22Path for the
	// python312 runtime (FAAS_FUNCTION_RUNNER_PYTHON312).
	functionRunnerPython312Path string
	// deployBaseRefOverride replaces the per-runtime base ref during
	// aboveBaseLayers. See WithDeployBaseRef — test-only seam.
	deployBaseRefOverride string
	// storage is the artifact backend where per-app ext4 layers,
	// snapshot blobs, base images, and kernel artifacts live (issue
	// #96 / ADR-025 axis 2). Optional; when nil the handler falls back
	// to a per-app LocalStorageBackend rooted at appsRoot so legacy
	// callers keep working without rewiring New(...).
	storage storage.StorageBackend
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

// WithFunctionRunnerNode22 returns the handler with the node22 runner binary
// path set. Wired from cmd/imaged when the function runner has been compiled
// (Makefile target `guest-runners`).
func (h *Handler) WithFunctionRunnerNode22(p string) *Handler {
	h.functionRunnerNode22Path = p
	return h
}

// WithFunctionRunnerPython312 mirrors WithFunctionRunnerNode22 for python312.
func (h *Handler) WithFunctionRunnerPython312(p string) *Handler {
	h.functionRunnerPython312Path = p
	return h
}

// deployBaseRefOverride, when set, replaces the ghcr.io base ref used by
// aboveBaseLayers at deploy time. Only the test harness sets this (it
// redirects the base manifest fetch to the local FakeRegistry); production
// leaves it empty and the runtime→base mapping in pkg/imaged/base.go is
// authoritative. M6 closed the door on per-runtime override because the
// spec's base economics are a fleet-wide contract — overriding per-deploy
// would silently fork drive0 across tenants.
func (h *Handler) WithDeployBaseRef(ref string) *Handler {
	h.deployBaseRefOverride = ref
	return h
}

// WithStorage wires the artifact backend the handler publishes per-app
// layers and base images to. Issue #96 / ADR-025 axis 2 — replaces the
// direct appsRoot/<slug>/<depID>.ext4 write path in imaged with a
// StorageBackend.Put under key "apps/<slug>/<depID>.ext4". Production
// wiring lives in cmd/imaged (PrefixRouter composing apps- and fc-roots);
// tests build a per-test LocalStorageBackend so assertions on the
// published key stay hermetic. Calling WithStorage(nil) clears the
// override and falls back to the appsRoot-derived default.
func (h *Handler) WithStorage(s storage.StorageBackend) *Handler {
	h.storage = s
	return h
}

// storageFor returns the wired StorageBackend, building a default
// per-appsRoot LocalStorageBackend on first use. The lazy-default keeps
// existing callers — every test that never calls WithStorage — running
// against the legacy path without a churn. production calls WithStorage
// in cmd/imaged so the default is never exercised in prod.
func (h *Handler) storageFor() storage.StorageBackend {
	if h.storage != nil {
		return h.storage
	}
	// Safe-by-default: NewLocalStorageBackend only errors on empty root or
	// NUL bytes in the path (appsRoot is supplied by cmd/imaged and tests
	// use t.TempDir()). Falling back to a nil backend would crash every
	// Build call, which is the loud failure we want.
	be, err := storage.NewLocalStorageBackend(h.appsRoot)
	if err != nil {
		panic(fmt.Sprintf("imaged: storageFor default backend: %v", err))
	}
	h.storage = be
	return be
}

// appsRootPath returns the on-disk legacy path the legacy code path
// stamped into deployments.rootfs_path. Used to keep the SetDeploymentRootfs
// row contract identical to pre-#96 even when the new Storage path is
// used to write the ext4.
func (h *Handler) appsRootPath(slug, deploymentID string) string {
	return filepath.Join(h.appsRoot, slug, deploymentID+".ext4")
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
		// F5 / F-02: when apid supersedes a deployment, drop the per-app
		// layer ext4 so appsRoot doesn't accumulate orphans. The snapshot
		// blob is KEPT (one-click rollback needs it) and GC'd by the
		// nightly sweep. F-02: prior code passed keepSnap=false here,
		// deleting the blob and forcing every rollback across a supersede
		// to cold-boot — fixed to keepSnap=true.
		if p.Status == string(state.DeploySuperseded) && p.To != "" {
			if err := h.cleanupDeploymentFiles(ctx, p.To, true /* keepSnap */); err != nil {
				h.log.Warn("imaged: cleanup superseded", "deployment", p.To, "err", err)
			}
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
	case db.NotifySnapshotBoot:
		var p snapshotBootPayload
		if err := json.Unmarshal([]byte(n.Payload), &p); err != nil {
			h.log.Warn("imaged: bad snapshot_boot payload", "err", err)
			return
		}
		if err := h.handleSnapshotBoot(ctx, p); err != nil {
			h.log.Warn("imaged: snapshot boot failed", "deployment", p.DeploymentID, "err", err)
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
	case db.NotifyAppChanged:
		var p appChangedPayload
		if err := json.Unmarshal([]byte(n.Payload), &p); err != nil {
			h.log.Warn("imaged: bad app_changed payload", "err", err)
			return
		}
		// F5: app soft-delete triggers the full filesystem scrub.
		if p.Kind == "deleted" && p.AppID != "" {
			if err := h.cleanupAppFiles(ctx, p.AppID); err != nil {
				h.log.Warn("imaged: cleanup app", "app", p.AppID, "err", err)
			}
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
	// Status is the post-transition deployment status (e.g. "live",
	// "superseded"). imaged uses it to detect supersede for F5 cleanup.
	Status string `json:"status,omitempty"`
}

// appChangedPayload is the JSON shape apid emits on `app_changed`. imaged
// only listens for soft-delete today (F5); other kinds (created, updated)
// are no-ops.
type appChangedPayload struct {
	AppID string `json:"app_id"`
	Kind  string `json:"kind"`
}

// buildQueuedPayload is the JSON shape apid emits on `build_queued` (see
// cmd/apid/deploy_inputs.go — `{"build", "deployment", "app", "kind"}`).
// F4 closes the gap where this struct decoded `app_id`/`build_id` while
// the on-wire shape used `app`/`build`, so the M5 stub never fired.
type buildQueuedPayload struct {
	AppID        string `json:"app"`
	BuildID      string `json:"build"`
	DeploymentID string `json:"deployment"`
	Kind         string `json:"kind"`
}

// snapshotWrittenPayload is the JSON shape schedd emits on `snapshot_written`
// after a Prime/Park writes the blob via vmmd (ADR-018, see pkg/db.NotifyChannels).
// imaged is the sole writer to the snapshots table, so it records the row.
type snapshotWrittenPayload struct {
	DeploymentID string `json:"deployment_id"`
	MemPath      string `json:"mem_path"`
	VMStatePath  string `json:"vmstate_path"`
	// StorageKey is the canonical StorageBackend key (issue #96,
	// ADR-025 axis 2). schedd populates it on the snapshot_written
	// payload; imaged copies it onto the snapshots row so Wake can
	// read it back without recomputing sched.SnapshotMemKey.
	StorageKey   string `json:"storage_key"`
	MemBytes     int64  `json:"mem_bytes"`
	VMStateBytes int64  `json:"vmstate_bytes"`
	FCVersion    string `json:"fc_version"`
}

// snapshotBootPayload is the JSON shape builderd emits on `snapshot_boot`
// after a build VM has produced an OCI image tarball and stamped it on
// deployments.rootfs_path (see pkg/builderd/builderd.go::ProcessOne). imaged
// is the sole subscriber: it converts the OCI tarball into a per-app ext4
// (drive1) and then re-emits NotifySnapshotPrime so schedd can cold-boot
// + snapshot (F4). The payload is intentionally minimal so the channel
// stays narrow.
type snapshotBootPayload struct {
	AppID        string `json:"app_id"`
	DeploymentID string `json:"deployment_id"`
}

// handleDeployment advances a deployment up to the point where a snapshot
// is needed. Two paths:
//
//   - kind=image + app.Type=app    → pull OCI digest, build app-layer ext4.
//   - kind=image + app.Type=function → apply customer's source tarball +
//     copy the function-runner binary
//     into the layer; the manifest
//     points at the runner.
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
//
// ref is the full OCI reference (`host/repo@sha256:...`) apid stored into
// dep.ImageDigest. We use the full ref (not just the bare digest) for every
// OCI call so the puller dials the right registry — a bare digest resolves
// to docker.io/library/sha256:... and dials the wrong host for non-Docker
// deploys (issue #53 / M5 acceptance on Lima).
func (h *Handler) buildImageLayer(ctx context.Context, app state.App, dep state.Deployment, acct state.Account) error {
	ref := dep.ImageDigest
	digest, err := h.oci.PullDigest(ctx, ref)
	if err != nil {
		_ = h.markDeployFailed(ctx, dep.ID, err, "oci pull failed")
		return fmt.Errorf("imaged: oci pull: %w", err)
	}

	if err := h.transition(ctx, dep.ID, state.DeployImaging, ""); err != nil {
		return err
	}

	imageCfg, err := h.oci.PullImageConfig(ctx, ref)
	if err != nil {
		_ = h.markDeployFailed(ctx, dep.ID, err, "oci pull config")
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

	// M6 wired-up build path: when the puller implements oci.ManifestPuller
	// we honor the two-drive scheme (spec §4.6) — pull the app + base
	// manifests, compute LayersAboveBase, and stream ONLY the above-base
	// layer blobs through rootfs.Builder. Without this, every per-app
	// ext4 would re-include base layers and break the 130 MB fleet-snapshot
	// economics (CLAUDE.md "load-bearing — DO NOT fix"). The M5 fallback
	// below streams all layers via oci.PullLayers for fakes that don't
	// implement ManifestPuller.
	appsKey := sched.AppLayerKey(app.Slug, dep.ID)
	if mp, ok := h.oci.(oci.ManifestPuller); ok {
		above, diffs, err := h.aboveBaseLayers(ctx, mp, dep.ImageDigest, app.Runtime, manifest)
		if err != nil {
			// aboveBaseLayers can surface any of the three puller-side
			// sentinels (image-not-found on app manifest 404, manifest-list
			// rejection on multi-arch images, egress-denial on a private
			// registry) so it goes through markDeployFailed too. Non-pull
			// failures (e.g. base mismatch) get code "" — the message
			// preserves the upstream string.
			_ = h.markDeployFailed(ctx, dep.ID, err, "imaged: above-base")
			return err
		}
		defer func() {
			for _, c := range above.closers {
				_ = c.Close()
			}
		}()
		result, err := h.builder.Build(ctx, rootfs.BuildInput{
			Layers:        above.readers,
			Manifest:      manifest,
			GuestInitPath: h.guestInitPath,
			Plan:          acct.Plan,
			Storage:       h.storageFor(),
			StorageKey:    appsKey,
		})
		if err != nil {
			_ = h.transition(ctx, dep.ID, state.DeployFailed, "build app layer: "+err.Error())
			return fmt.Errorf("imaged: build app layer: %w", err)
		}
		if err := h.store.SetDeploymentRootfs(ctx, dep.ID, h.appsRootPath(app.Slug, dep.ID), result.ContentBytes); err != nil {
			_ = h.transition(ctx, dep.ID, state.DeployFailed, "stamp rootfs: "+err.Error())
			return fmt.Errorf("imaged: stamp rootfs: %w", err)
		}
		h.log.Info("imaged: build app layer (two-drive)",
			"app", app.Slug, "digest", digest, "key", result.ImageKey,
			"bytes", result.ContentBytes, "above_diff_ids", len(diffs))
	} else {
		// M5 fallback: stream all layers as-is. Used by fakes that only
		// implement oci.Puller — the existing unit tests exercise this
		// branch.
		pulled, err := h.oci.PullLayers(ctx, ref)
		if err != nil {
			_ = h.markDeployFailed(ctx, dep.ID, err, "oci pull layers")
			return fmt.Errorf("imaged: pull layers: %w", err)
		}
		defer func() {
			for _, r := range pulled.Layers {
				_ = r.Close()
			}
		}()
		result, err := h.builder.Build(ctx, rootfs.BuildInput{
			Layers:        layersAsReaders(pulled.Layers),
			Manifest:      manifest,
			GuestInitPath: h.guestInitPath,
			Plan:          acct.Plan,
			Storage:       h.storageFor(),
			StorageKey:    appsKey,
		})
		if err != nil {
			_ = h.transition(ctx, dep.ID, state.DeployFailed, "build app layer: "+err.Error())
			return fmt.Errorf("imaged: build app layer: %w", err)
		}
		if err := h.store.SetDeploymentRootfs(ctx, dep.ID, h.appsRootPath(app.Slug, dep.ID), result.ContentBytes); err != nil {
			_ = h.transition(ctx, dep.ID, state.DeployFailed, "stamp rootfs: "+err.Error())
			return fmt.Errorf("imaged: stamp rootfs: %w", err)
		}
		h.log.Info("imaged: build app layer (m5 fallback)", "app", app.Slug, "digest", digest, "key", result.ImageKey, "bytes", result.ContentBytes)
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
// The runner binary is injected from a per-runtime path the daemon config
// provides (cmd/imaged wires both env-driven). Fails loud when the matching
// path is empty — silent omission meant production function deploys were
// shipping a layer without /usr/local/bin/faas-runner (M8 readiness).
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
	if runtime != RuntimeNode22 && runtime != RuntimePython312 {
		_ = h.transition(ctx, dep.ID, state.DeployFailed, "unsupported runtime: "+runtime)
		return fmt.Errorf("imaged: unsupported function runtime %q", runtime)
	}
	// Fail loud when the runner binary isn't wired. This is the gap that
	// shipped in M6: the runner binary was never plumbed from cmd/imaged,
	// so a node22 or python312 deploy would silently leave the ext4
	// without /usr/local/bin/faas-runner and FAILED on first wake.
	runnerPath := h.runnerPathFor(runtime)
	if runnerPath == "" {
		msg := fmt.Sprintf("function runner binary not configured for runtime %q (set FAAS_FUNCTION_RUNNER_%s on the imaged unit)", runtime, runtimeToEnvSuffix(runtime))
		_ = h.transition(ctx, dep.ID, state.DeployFailed, msg)
		return fmt.Errorf("imaged: %s", msg)
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
	if runtime == RuntimePython312 {
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

	appsKey := sched.AppLayerKey(app.Slug, dep.ID)
	result, err := h.builder.Build(ctx, rootfs.BuildInput{
		Layers:        layersAsReaders(nil), // function deploys use the tarball via BuildInput.Tarball
		Manifest:      manifest,
		GuestInitPath: h.guestInitPath,
		Plan:          acct.Plan,
		Storage:       h.storageFor(),
		StorageKey:    appsKey,
		// TarballPath lets the rootfs.Builder stream the customer's
		// source tarball into /app during layer assembly. Tests skip
		// this by leaving TarballPath empty.
		TarballPath: dep.SourcePath,
		// FunctionRunnerPath is the static guest/runners/<rt>/faas-runner
		// binary that lives at /usr/local/bin/faas-runner in the layer.
		FunctionRunnerPath: runnerPath,
	})
	if err != nil {
		_ = h.transition(ctx, dep.ID, state.DeployFailed, "build function layer: "+err.Error())
		return fmt.Errorf("imaged: build function layer: %w", err)
	}
	if err := h.store.SetDeploymentRootfs(ctx, dep.ID, h.appsRootPath(app.Slug, dep.ID), result.ContentBytes); err != nil {
		_ = h.transition(ctx, dep.ID, state.DeployFailed, "stamp rootfs: "+err.Error())
		return fmt.Errorf("imaged: stamp rootfs: %w", err)
	}
	return nil
}

// runnerPathFor returns the wired static-binary path for the runtime, or ""
// when nothing was wired. Empty string is the fail-loud signal — callers
// must transition the deployment to failed before building.
func (h *Handler) runnerPathFor(runtime string) string {
	switch runtime {
	case RuntimeNode22:
		return h.functionRunnerNode22Path
	case RuntimePython312:
		return h.functionRunnerPython312Path
	}
	return ""
}

// runtimeToEnvSuffix maps a runtime identifier to its env-var suffix so the
// fail-loud error message names the operator-facing knob (e.g. NODE22 for
// FAAS_FUNCTION_RUNNER_NODE22).
func runtimeToEnvSuffix(runtime string) string {
	switch runtime {
	case RuntimeNode22:
		return "NODE22"
	case RuntimePython312:
		return "PYTHON312"
	}
	return runtime
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
		StorageKey:   p.StorageKey, // see snapshotWrittenPayload.StorageKey
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

// handleBuildQueued advances a queued source build. The full builderd VM
// pipeline runs in pkg/builderd and emits NotifySnapshotBoot when the OCI
// tarball is ready; imaged subscribes to that channel (handleSnapshotBoot)
// and converts the tarball into an app-layer ext4. handleBuildQueued here
// is a thin dispatcher that:
//
//   - flips the builds row to running so dashboards reflect the state,
//   - resolves the deployment_id from the build row,
//   - if the deployment's rootfs_path has already been stamped by builderd,
//     synthesises a snapshotBootPayload and forwards to handleSnapshotBoot;
//   - otherwise, returns (the canonical path is builderd's later
//     NotifySnapshotBoot emit).
//
// F-01: apid emits build_queued immediately at deploy creation; builderd
// stamps rootfs_path LATER (after the build VM produces an OCI image).
// The old code dispatched to handleSnapshotBoot unconditionally, which
// failed the deployment with "empty rootfs_path" on every tarball /
// dockerfile deploy. The canonical F4 path is the NotifySnapshotBoot
// channel — handleBuildQueued now stays out of the way until builderd
// has stamped.
//
// This keeps the apid-emitted payload shape ({build, deployment, app, kind})
// untouched while funnelling all OCI-tarball→ext4 work through the
// canonical handler.
func (h *Handler) handleBuildQueued(ctx context.Context, p buildQueuedPayload) error {
	if p.BuildID == "" || p.DeploymentID == "" {
		return errors.New("imaged: build_queued missing build or deployment id")
	}
	// failure_class is "" while the build is in-flight; both MemStore and
	// PgStore treat the empty string as "preserve prior value" via a
	// `case when $3 = ''` guard in the UPDATE. There is no FailureNone
	// constant — empty string is the canonical no-class sentinel.
	if err := h.store.UpdateBuildStatus(ctx, p.BuildID, state.BuildRunning, "", true, false); err != nil {
		return fmt.Errorf("imaged: mark building: %w", err)
	}
	dep, err := h.store.DeploymentByID(ctx, p.DeploymentID)
	if err != nil {
		return fmt.Errorf("imaged: resolve deployment for build: %w", err)
	}
	// F-01: wait for builderd to stamp rootfs_path before converting.
	// The handler isn't idempotent on a not-yet-stamped deployment —
	// running buildImageLayer / buildFunctionLayer now would either
	// no-op (image kind, nothing to pull) or fail loud (tarball kind,
	// no OCI tarball to convert). Either way we poison the deployment.
	if dep.RootfsPath == "" {
		h.log.Info("imaged: build_queued: waiting on builderd to stamp rootfs_path",
			"app", p.AppID, "build", p.BuildID, "deployment", dep.ID, "kind", p.Kind)
		return nil
	}
	h.log.Info("imaged: build queued (dispatch to handleSnapshotBoot)",
		"app", p.AppID, "build", p.BuildID, "deployment", dep.ID, "kind", p.Kind)
	return h.handleSnapshotBoot(ctx, snapshotBootPayload{
		AppID:        p.AppID,
		DeploymentID: dep.ID,
	})
}

// handleSnapshotBoot is the canonical builderd-driven path (F4). builderd
// has finished its build VM, stamped the OCI image tarball onto
// deployments.rootfs_path, and emitted NotifySnapshotBoot. imaged:
//
//   - redelivery-guards on the deployment status (no work past `building`),
//   - runs the same buildImageLayer path the image deploy uses — the OCI
//     tarball is consumed via rootfs.Builder,
//   - re-emits NotifySnapshotPrime for schedd to cold-boot + snapshot.
//
// The pre-image-deploy transitions (pending → building → imaging →
// snapshotting) are NOT walked here: by the time builderd fires the boot
// notification, apid has already advanced the row to `building` (apid's
// POST /v1/apps/{app}/deployments handler flips it). imaged picks up at
// `imaging` to keep the state-machine CHECK constraints happy.
func (h *Handler) handleSnapshotBoot(ctx context.Context, p snapshotBootPayload) error {
	if p.DeploymentID == "" {
		return errors.New("imaged: snapshot_boot missing deployment_id")
	}
	dep, err := h.store.DeploymentByID(ctx, p.DeploymentID)
	if err != nil {
		return fmt.Errorf("imaged: load deployment: %w", err)
	}
	switch dep.Status {
	case state.DeployPending, state.DeployBuilding:
		// proceed
	default:
		// Redelivery or out-of-order; bail silently.
		h.log.Info("imaged: snapshot_boot ignored",
			"deployment", p.DeploymentID, "status", dep.Status)
		return nil
	}
	if dep.RootfsPath == "" {
		// builderd hasn't stamped yet (or handleBuildQueued dispatched
		// too early). Treat as a transient no-op rather than failing
		// the deployment — the subsequent NotifySnapshotBoot / second
		// build_queued redelivery will find a stamp. F-01.
		h.log.Warn("imaged: snapshot_boot skipped — rootfs_path empty; waiting on builderd",
			"deployment", p.DeploymentID)
		return nil
	}
	app, err := h.store.AppByID(ctx, dep.AppID)
	if err != nil {
		return fmt.Errorf("imaged: load app: %w", err)
	}
	acct, err := h.store.AccountByID(ctx, app.AccountID)
	if err != nil {
		return fmt.Errorf("imaged: load account: %w", err)
	}
	if err := h.transition(ctx, dep.ID, state.DeployImaging, ""); err != nil {
		return err
	}
	// Dispatch on the deploy kind — builderd stamps the OCI tarball
	// (function deploy) or OCI image ref (tarball/dockerfile deploy)
	// onto deployments.rootfs_path before emitting NotifySnapshotBoot.
	// F4: an image-kind deploy for a tarball/dockerfile source is a
	// misconfig; we fail loud rather than silently try to OCI-pull a
	// local file.
	switch dep.Kind {
	case state.DeploymentKindImage:
		if err := h.buildImageLayer(ctx, app, dep, acct); err != nil {
			return err
		}
	case state.DeploymentKindTarball, state.DeploymentKindDockerfile:
		if err := h.buildFunctionLayer(ctx, app, dep, acct); err != nil {
			return err
		}
	default:
		return fmt.Errorf("imaged: snapshot_boot: unknown deployment kind %q", dep.Kind)
	}
	if err := h.transition(ctx, dep.ID, state.DeploySnapshotting, ""); err != nil {
		return err
	}
	primePayload, _ := json.Marshal(map[string]string{
		"app_id":        app.ID,
		"deployment_id": dep.ID,
	})
	if err := h.notif.Notify(ctx, db.NotifySnapshotPrime, string(primePayload)); err != nil {
		return fmt.Errorf("imaged: notify snapshot_prime: %w", err)
	}
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

// markDeployFailed transitions a deployment to `failed` with the given
// RFC 7807 code (or "" if the upstream error didn't map to a sentinel)
// and a free-text message. The code column carries the stable signal;
// the message preserves the upstream string for debugging. Returns
// the mark error so the caller can choose to log-and-continue or
// bubble it up; in practice callers ignore the mark error (the
// deployment row already reflects the failure and the original error
// is what the caller actually wants to return).
//
// ADR-021: this is the single seam where puller-side sentinels get
// lifted into a stable code on deployments.error_code. The wake
// path reads the same column and lifts it into a Problem on the
// failed-deployment GET response, so a customer / dashboard can
// branch on a stable string rather than parsing the free-text
// deployments.error.
func (h *Handler) markDeployFailed(ctx context.Context, depID string, err error, prefix string) error {
	code, _ := oci.SentinelToCode(err)
	if _, err := h.store.SetDeploymentFailed(ctx, depID, code, prefix+": "+err.Error()); err != nil {
		return fmt.Errorf("imaged: mark failed: %w", err)
	}
	return nil
}

// aboveBaseStream is the result of resolving the above-base layers for an
// app image. The Reader side is fed to rootfs.Builder; the Closers slice is
// closed by the caller in a defer so streaming ReadClosers don't leak.
type aboveBaseStream struct {
	readers []io.Reader
	closers []io.Closer
}

// aboveBaseLayers is the M6 two-drive seam: given the app's image ref + runtime,
// pull the app manifest, pull the matching base manifest, compute the
// app's diff_ids that sit ABOVE the base, and stream only those compressed
// blob readers. Callers MUST close the returned closers in a defer.
//
// Spec §4.6 (CLAUDE.md "load-bearing — DO NOT fix"): flattening the base
// layers into every per-app ext4 would duplicate ~150 MB of base per app and
// break the 130 MB fleet-snapshot economics. drive0 (base ext4) and drive1
// (this ext4) overlay at guest-init; this function returns only the parts
// that go into drive1.
func (h *Handler) aboveBaseLayers(ctx context.Context, mp oci.ManifestPuller,
	appRef, runtime string, _ api.AppManifest) (aboveBaseStream, []string, error) {
	appRepo := repoWithHost(appRef)
	if appRepo == "" {
		return aboveBaseStream{}, nil, fmt.Errorf("imaged: cannot derive repo from %q", appRef)
	}
	appManifest, err := mp.PullManifest(ctx, appRef)
	if err != nil {
		return aboveBaseStream{}, nil, fmt.Errorf("manifest: %w", err)
	}
	appCfg, err := h.pullConfig(ctx, mp, appRepo, appManifest.Config.Digest)
	if err != nil {
		return aboveBaseStream{}, nil, fmt.Errorf("app config: %w", err)
	}
	baseRef := h.deployBaseRefOverride
	if baseRef == "" {
		baseRef = baseRefFor(runtime)
	}
	baseRepo := repoWithHost(baseRef)
	if baseRepo == "" {
		return aboveBaseStream{}, nil, fmt.Errorf("imaged: cannot derive repo from base %q", baseRef)
	}
	baseManifest, err := mp.PullManifest(ctx, baseRef)
	if err != nil {
		return aboveBaseStream{}, nil, fmt.Errorf("base manifest: %w", err)
	}
	baseCfg, err := h.pullConfig(ctx, mp, baseRepo, baseManifest.Config.Digest)
	if err != nil {
		return aboveBaseStream{}, nil, fmt.Errorf("base config: %w", err)
	}
	above, err := oci.LayersAboveBase(baseCfg.DiffIDs, appCfg.DiffIDs)
	if err != nil {
		return aboveBaseStream{}, nil, fmt.Errorf("layers above base: %w", err)
	}

	// Map diff_ids → compressed-blob digest. The manifest's `layers[]` lists
	// compressed blobs in the same bottom-to-top order as config.diff_ids.
	if len(appManifest.Layers) != len(appCfg.DiffIDs) {
		return aboveBaseStream{}, nil, fmt.Errorf("layer count mismatch: manifest=%d config=%d",
			len(appManifest.Layers), len(appCfg.DiffIDs))
	}
	blobByDiff := make(map[string]oci.Descriptor, len(appManifest.Layers))
	for i, l := range appManifest.Layers {
		blobByDiff[appCfg.DiffIDs[i]] = l
	}

	readers := make([]io.Reader, 0, len(above))
	closers := make([]io.Closer, 0, len(above))
	for _, diffID := range above {
		desc, ok := blobByDiff[diffID]
		if !ok {
			// Roll back any readers we already opened.
			for _, c := range closers {
				_ = c.Close()
			}
			return aboveBaseStream{}, nil, fmt.Errorf("imaged: missing blob for diff %s", diffID)
		}
		rc, err := mp.PullBlob(ctx, appRepo, desc.Digest)
		if err != nil {
			for _, c := range closers {
				_ = c.Close()
			}
			return aboveBaseStream{}, nil, fmt.Errorf("pull blob %s: %w", desc.Digest, err)
		}
		closers = append(closers, rc)
		readers = append(readers, rc)
	}
	return aboveBaseStream{readers: readers, closers: closers}, above, nil
}

// pullConfig fetches and parses the OCI image config referenced by a manifest.
// The config carries the env/entrypoint (run by guest-init) AND the
// rootfs.diff_ids that drive the two-drive math.
func (h *Handler) pullConfig(ctx context.Context, mp oci.ManifestPuller, repo, digest string) (oci.Config, error) {
	r, err := mp.PullBlob(ctx, repo, digest)
	if err != nil {
		return oci.Config{}, err
	}
	defer func() { _ = r.Close() }()
	return oci.ParseConfig(r)
}

// repoWithHost returns "host/repo" for a parsed reference, or just "repo" when
// the reference is on the default registry (docker.io). The OCI client's
// PullBlob synthesizes a Reference from `repo+@digest` and looks up the
// registry from that synthesized ref; if the caller passes a bare repo path
// (e.g. "library/hello") the synthesised ref defaults to docker.io and
// non-Docker-Hub deploys dial the wrong host. Passing "host/repo" preserves
// the registry. Returns "" on parse failure.
func repoWithHost(ref string) string {
	r, err := oci.ParseReference(ref)
	if err != nil {
		return ""
	}
	if r.Registry == "docker.io" {
		return r.Repository
	}
	return r.Registry + "/" + r.Repository
}

// --- F5: filesystem cleanup -------------------------------------------------
//
// imaged is the sole owner of `/srv/fc/snap/<depID>/` and
// `<appsRoot>/<slug>/<depID>.ext4`. The DB row is the source of truth; the
// filesystem is the cache. Missing files log Warn, never fail (ADR-005:
// cold boot must always work, even if a stale filesystem lingers).
//
// Cleanup fires on two events:
//   - deployment superseded → drop the per-app ext4 (drive1). The snapshot
//     blob is KEPT so one-click rollback stays instant; the GC evicts it
//     when it falls out of the "current + previous" window.
//   - app soft-deleted → drop the ext4 AND the snap blobs for every
//     deployment of the app. Best-effort.

// cleanupDeploymentFiles removes the on-disk artifacts for a single deployment.
// keepSnap=true leaves the snapshot blob (one-click rollback) and only removes
// the per-app ext4.
func (h *Handler) cleanupDeploymentFiles(ctx context.Context, deploymentID string, keepSnap bool) error {
	dep, err := h.store.DeploymentByID(ctx, deploymentID)
	if err != nil {
		return fmt.Errorf("imaged: cleanup load deployment: %w", err)
	}
	app, err := h.store.AppByID(ctx, dep.AppID)
	if err != nil {
		return fmt.Errorf("imaged: cleanup load app: %w", err)
	}
	// Per-app ext4 (drive1). Storage.Delete swallows ErrNotFound; the
	// legacy os.Remove-Warn check is preserved via the storage backend's
	// own error wrapping.
	appsKey := sched.AppLayerKey(app.Slug, dep.ID)
	if err := h.storageFor().Delete(ctx, appsKey); err != nil {
		h.log.Warn("imaged: cleanup ext4", "key", appsKey, "err", err)
	}
	if !keepSnap {
		memKey := sched.SnapshotMemKey(dep.ID)
		vmKey := sched.SnapshotVMStateKey(dep.ID)
		if err := h.storageFor().Delete(ctx, memKey); err != nil {
			h.log.Warn("imaged: cleanup snap mem", "key", memKey, "err", err)
		}
		if err := h.storageFor().Delete(ctx, vmKey); err != nil {
			h.log.Warn("imaged: cleanup snap vmstate", "key", vmKey, "err", err)
		}
	}
	return nil
}

// cleanupAppFiles walks every deployment for the app, drops the per-app ext4
// AND the snap blobs for each, then unlinks the per-app directory entirely.
//
// A missing app row is treated as a silent no-op (logs at Info level when
// the store surfaces ErrNotFound). app_changed notifications can fire on
// non-delete transitions; the switch in HandleNotification only routes
// kind="deleted" here, but defensive zero-error keeps the loop steady
// under redelivery or operator replay.
func (h *Handler) cleanupAppFiles(ctx context.Context, appID string) error {
	app, err := h.store.AppByID(ctx, appID)
	if err != nil {
		if errors.Is(err, state.ErrNotFound) {
			h.log.Info("imaged: cleanup app no-op (missing)", "app", appID)
			return nil
		}
		return fmt.Errorf("imaged: cleanup load app: %w", err)
	}
	deps, err := h.store.ListDeploymentsForApp(ctx, appID, 0, 0)
	if err != nil {
		return fmt.Errorf("imaged: cleanup list deployments: %w", err)
	}
	be := h.storageFor()
	for _, d := range deps {
		appsKey := sched.AppLayerKey(app.Slug, d.ID)
		if err := be.Delete(ctx, appsKey); err != nil {
			h.log.Warn("imaged: app cleanup ext4", "key", appsKey, "err", err)
		}
		memKey := sched.SnapshotMemKey(d.ID)
		vmKey := sched.SnapshotVMStateKey(d.ID)
		if err := be.Delete(ctx, memKey); err != nil {
			h.log.Warn("imaged: app cleanup snap mem", "key", memKey, "err", err)
		}
		if err := be.Delete(ctx, vmKey); err != nil {
			h.log.Warn("imaged: app cleanup snap vmstate", "key", vmKey, "err", err)
		}
	}
	return nil
}

// --- F2: FC-version startup sweep ------------------------------------------

// MarkFCSnapshotsStale is the F2 sweep body. It is invoked once at imaged
// startup (cmd/imaged/main.go wires it) and never on a timer — a Firecracker
// upgrade requires the operator to restart imaged, which matches the
// "snapshots are cache, not truth" framing (ADR-005). Idempotent.
func (h *Handler) MarkFCSnapshotsStale(ctx context.Context, fcVersion string) (int64, error) {
	if fcVersion == "" {
		return 0, errors.New("imaged: MarkFCSnapshotsStale: empty fc version")
	}
	n, err := h.store.MarkAllSnapshotsStaleByFCVersion(ctx, fcVersion)
	if err != nil {
		return 0, fmt.Errorf("imaged: mark stale by fc: %w", err)
	}
	return n, nil
}
