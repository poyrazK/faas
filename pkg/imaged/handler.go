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
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/audit"
	"github.com/onebox-faas/faas/pkg/cosign"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/fcvm"
	"github.com/onebox-faas/faas/pkg/logsanitize"
	"github.com/onebox-faas/faas/pkg/oci"
	"github.com/onebox-faas/faas/pkg/rootfs"
	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/secretbox"
	"github.com/onebox-faas/faas/pkg/secretscan"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/storage"
	"github.com/onebox-faas/faas/pkg/wire"

	"filippo.io/age"
)

const defaultHealthzPath = "/healthz"

// Notifier is the minimal interface imaged needs from pkg/db. The real
// implementation is db.Notify (postgres LISTEN/NOTIFY); tests inject a fake.
type Notifier interface {
	Notify(ctx context.Context, channel, payload string) error
}

// ArtifactReplicator is an optional handoff for split-box deployments that
// still use the local storage backend. It runs after imaged has published and
// signed an app-layer ext4 and before snapshot_prime is emitted, so schedd can
// verify the same layer on the control-plane host without racing an
// eventually-consistent file copy. OCI-backed deployments do not need this
// hook: the shared registry is already the authoritative artifact store.
type ArtifactReplicator interface {
	Replicate(ctx context.Context, layerKey string) error
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
	// BuildBaseFromStaging (ADR-053) mkfs-es an already-populated
	// staging dir into the canonical base ext4. The imaged
	// parent-ref branch cp -a's the parent's tree into a fresh
	// MkdirBaseStaging dir, applies ONLY the runtime delta OCI
	// layers via ApplyLayerGz, and hands the dir here — mkfs +
	// publish, no layer apply inside Build.
	BuildBaseFromStaging(ctx context.Context, staging string, in rootfs.BaseBuildInput) (rootfs.BaseBuildResult, error)
	// BuildFullRootfs (ADR-141 §Decision 1, M-3 commit 5+6)
	// assembles a self-contained ext4 rootfs from ALL of the app
	// image's layers — bypasses the two-drive shared-base path
	// entirely. Used by imaged.buildFullRootfsLayer when the app
	// image is NOT built FROM one of our runner-* bases.
	BuildFullRootfs(ctx context.Context, in rootfs.BuildFullRootfsInput) (rootfs.BuildResult, error)
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
	// nodeName is the compute_node identity of this imaged process. A
	// snapshot_boot notification is fleet-wide, while the builder's OCI
	// export is local to the node that produced it. Named multi-box daemons
	// therefore handle only notifications addressed to their own node.
	// Empty preserves the legacy single-box behaviour.
	nodeName string

	// trustedPublishersDir is the directory holding the per-app
	// cosign trusted-publisher PEM files (issue #472 / ADR-054).
	// Read once at daemon startup (via cosign.TrustedPublishersFromDir)
	// and refreshed on pg_notify('trusted_signer_changed') via
	// HandleNotification. Empty means "verify disabled" — the
	// apps.require_signed=false default. Wired from cmd/imaged via
	// WithTrustedPublishersDir; env var FAAS_TRUSTED_PUBLISHERS_DIR
	// sets it at deploy time.
	trustedPublishersDir string
	// trustedPublishersMu guards the cache below. Read-mostly on the
	// hot path (every signed deploy reads once); refreshes are
	// rare (operator onboards a publisher). A sync.RWMutex keeps
	// the verify path lock-free for the read side.
	//
	// The cache is keyed by app.ID so the verify path can filter
	// by app at call time without re-reading the dir. Empty value
	// for an app means "no publishers for this app" (the
	// fail-closed case). Missing key means "we haven't loaded this
	// app yet" — first deploy pays the dir-load cost.
	trustedPublishersMu      sync.RWMutex
	trustedPublishersCache   map[string][]cosign.TrustedPublisher
	trustedPublishersCacheOK bool

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
	// functionRunnerGo124Path mirrors functionRunnerNode22Path for the
	// go124 runtime (FAAS_FUNCTION_RUNNER_GO124). The runner binary
	// itself is a static Go executable; the customer's compiled handler
	// (also a static Go binary, built by Railpack) lives at /app/handler
	// in the layer and is exec'd per request by the runner.
	functionRunnerGo124Path string
	// functionRunnerGo124AlpinePath mirrors functionRunnerGo124Path for
	// the go124-alpine runtime (FAAS_FUNCTION_RUNNER_GO124_ALPINE). The
	// runner binary at the wired path is the SAME static Go executable
	// as go124 — the only difference is the base image's libc
	// (alpine/musl vs bookworm/glibc). The function layer's argv is
	// identical to go124.
	functionRunnerGo124AlpinePath string
	// functionRunnerNode24Path mirrors functionRunnerNode22Path for the
	// node24 runtime (FAAS_FUNCTION_RUNNER_NODE24). Same `node`
	// interpreter as node22; the underlying version is bound by the OCI
	// base image (images/runner-node24.Dockerfile, PR 2 of Tier 1), not
	// by the runner binary. The handler path is /app/node24.js.
	functionRunnerNode24Path string
	// functionRunnerPython313Path mirrors functionRunnerPython312Path
	// for the python313 runtime (FAAS_FUNCTION_RUNNER_PYTHON313).
	// Handler path stays /app/handler.py (version-neutral on the wire).
	functionRunnerPython313Path string
	// deployBaseRefOverride replaces the per-runtime base ref during
	// aboveBaseLayers. See WithDeployBaseRef — test-only seam.
	deployBaseRefOverride string
	// storage is the artifact backend where per-app ext4 layers,
	// snapshot blobs, base images, and kernel artifacts live (issue
	// #96 / ADR-025 axis 2). Optional; when nil the handler falls back
	// to a per-app LocalStorageBackend rooted at appsRoot so legacy
	// callers keep working without rewiring New(...).
	storage storage.StorageBackend
	// replicator is the optional split-box local-artifact handoff. It is
	// deliberately separate from StorageBackend: a local backend has no
	// cross-host visibility, while an OCI backend already provides it.
	replicator ArtifactReplicator
	// ops holds the per-daemon Prometheus registry (this PR). Wired
	// via WithOpsMetrics; nil = observation no-op (unit tests).
	// Records imaged_op_duration_seconds / imaged_ops_total at the
	// OCI pull sites inside aboveBaseLayers + the legacy
	// PullLayers path, plus imaged_oci_pull_duration_seconds
	// per-pull op{manifest,config,blob,above_base}.
	ops *wire.OpsMetrics
	// audit (issue #470 / PR C / ADR-074) is the imaged-side
	// audit-log seam used to emit app.warm_snapshot_stale from the
	// MarkFCSnapshotsStale path. Wired via WithAudit; nil opts
	// out (unit-test parity with cmd/schedd/cmd/apid's audit
	// seam). Subject shape = &app.AccountID for account-scoped
	// audit listing per ADR-074 §3.2.
	audit *audit.Auditor
	// grypeRun is the supply-chain scan runner used at base-stage
	// time to write the Grype scan sidecar (issue #299 / ADR-075
	// PR-2). Wired via WithGrypeRun; nil = default to a subprocess
	// invocation (grype dir:<outImage> -o json) — production default.
	// Tests inject a stub returning canned findings so the sidecar
	// write is hermetic and doesn't require Grype on PATH. Fail-closed
	// at the sidecar-write site (CRITICAL=9999 placeholder) when
	// the runner returns an error or nil findings. The return type
	// is *ScanResult (PR-2 refactor) — the pre-PR-2 map[string]int
	// is the typed struct's SeverityCounts field; the base-ext4
	// sidecar write at base_stage.go::writeScanSidecar reads the
	// counts off the struct to build the legacy sidecar JSON.
	grypeRun func(ctx context.Context, dir string) (*ScanResult, error)
	// syftRun is the post-build SBOM generator used to populate
	// build_provenance.sbom_storage_key (issue #299 / ADR-038
	// Phase 3). Wired via WithSyftRun; nil = default to a
	// subprocess invocation (`syft dir:<outDir> -o cyclonedx-json`)
	// — production default. Tests inject a stub returning canned
	// CycloneDX JSON so the storage write is hermetic and doesn't
	// require syft on PATH. Best-effort: a syft error writes no
	// SBOM and leaves sbom_storage_key empty; the build itself
	// still succeeds (the SBOM is observational, not a deployment
	// precondition — schema §4.2).
	syftRun func(ctx context.Context, dir string) ([]byte, error)
	// secretScanRun is the post-build secretscan walker used at the
	// end of buildImageLayer / buildSidecarLayers to detect secrets
	// baked into the assembled image (PR-A, closes the v2 source-tree
	// gap for OCI image bytes — Dockerfile ENV, --build-arg, COPY'd
	// .env files all slip past cmd/apid source-tree scanning but
	// land baked into the image). Wired via WithSecretScanRun; nil
	// = default to a filepath.WalkDir + secretscan.ScanFile walker
	// (mirrors cmd/apid::scanExtractedTreeSecrets). Tests inject a
	// stub returning canned findings so the deploy-fail path is
	// hermetic and doesn't require secretscan.ScanFile to actually
	// match. Loud-fail posture: a finding stamps the audit row via
	// state.Store.UpsertDeploymentSecretFindings and the deploy
	// transitions to state.DeployFailed with errImageSecretDetected.
	// The grype CVE path (runDeployScan above) is best-effort by
	// design (ADR-075 AC #4); the secret path is intentionally NOT
	// — secrets are a security boundary, not metadata.
	secretScanRun func(ctx context.Context, dir, layer string) ([]secretscan.Finding, error)
	// vmmClient (ADR-053) is the imaged-side gRPC client to vmmd
	// used by the parent-ref staging branch of EnsureBaseExt4. vmmd
	// owns the loopback mount; imaged is not root (User=faas-imaged
	// + NoNewPrivileges=yes per spec §11) and cannot mount on its
	// own. Wired via WithVMMClient at daemon startup. Nil-safe:
	// the legacy "apply all layers" path stays operational without
	// a client wired; only the parent-ref branch fails loud.
	// Interface (not concrete *VMMClient) so tests can inject a
	// fakeVMMClient (defined in vmmclient.go) without dialing.
	vmmClient VMMClientIface
	// runtimeBaseMu serializes on-demand runtime-base staging. Multiple
	// deployments can arrive together after a cold start; without this
	// guard they could concurrently rebuild the same ext4 and race the
	// StorageBackend publication.
	runtimeBaseMu sync.Mutex
	// runtimeBaseStagingEnabled is true only for the production daemon. The
	// in-memory handler constructors used by unit tests intentionally keep
	// the build pipeline hermetic and do not need shared ext4 staging.
	runtimeBaseStagingEnabled bool
	// secretboxIdentity (issue #461 / ADR-062) is the host age
	// identity used to TRANSIENTLY unseal per-app private-registry
	// Basic Auth passwords during the pull path. The plaintext
	// password lives only inside one call frame and is GC'd on
	// return — NEVER attached to a Deployment, audit payload, log
	// line, or returned error. Wired via WithSecretboxIdentity at
	// daemon startup from FAAS_HOST_AGE_IDENTITY_PATH (the same
	// path apid loads for MFA unseal). Nil-safe: with no identity
	// wired, the registry credential lookup is skipped and pulls
	// stay anonymous (matches the Free plan / no-credential case).
	secretboxIdentity *age.X25519Identity
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
// WithTrustedPublishersDir configures the directory holding the
// per-app cosign trusted-publisher PEM files (issue #472 / ADR-054).
// Wired from cmd/imaged when FAAS_TRUSTED_PUBLISHERS_DIR is set.
// Empty dir (the default) disables signature verification — the
// apps.require_signed=false default keeps the open-deploy posture.
// On set, the handler immediately loads the dir into the in-memory
// cache so the first signed deploy doesn't pay a cold-load cost.
func (h *Handler) WithTrustedPublishersDir(dir string) *Handler {
	h.trustedPublishersDir = dir
	if dir != "" {
		if err := h.refreshTrustedPublishers(); err != nil {
			// Fail loud at startup — a misconfigured trust dir
			// is the canonical "operator typo" footgun. The
			// error is returned to the cmd/imaged wiring code,
			// which logs it and continues with an empty cache
			// (signed deploys will fail at verify time, not
			// silently). Logged rather than fatal because the
			// open-deploy posture (require_signed=false on
			// every app) means the daemon stays useful.
			h.log.Warn("trusted-publishers dir load failed at startup",
				"dir", dir, "err", err)
		}
	}
	return h
}

// refreshTrustedPublishers re-reads the trust dir into the
// in-memory cache. Triggered at startup (WithTrustedPublishersDir)
// and on pg_notify('trusted_signer_changed'). Holds the write lock
// for the duration of the disk read — the read is O(files) of
// small PEM blobs (~250 bytes each), so the critical section is
// microseconds.
//
// The cache is keyed by app.ID; the dir filename is
// `<app_id>--<name>.pem`. The apid-side LISTEN goroutine is the
// sole producer (it walks app_trusted_signers on every
// trusted_signer_changed notify); see
// cmd/apid/trusted_publisher_writer.go. The on-disk mirror is the
// imaged-side read surface, the DB row is the apid-side write
// surface. Without this refresh after a notify the verify path
// would either fail open (stale cache) or fail closed (no entry
// for the just-onboarded publisher).
func (h *Handler) refreshTrustedPublishers() error {
	if h.trustedPublishersDir == "" {
		h.trustedPublishersMu.Lock()
		h.trustedPublishersCache = nil
		h.trustedPublishersCacheOK = false
		h.trustedPublishersMu.Unlock()
		return nil
	}
	byApp, err := cosign.TrustListFromDir(h.trustedPublishersDir)
	if err != nil {
		return err
	}
	h.trustedPublishersMu.Lock()
	h.trustedPublishersCache = byApp
	h.trustedPublishersCacheOK = true
	h.trustedPublishersMu.Unlock()
	total := 0
	for _, v := range byApp {
		total += len(v)
	}
	h.log.Info("trusted-publishers cache refreshed", "dir", h.trustedPublishersDir, "apps", len(byApp), "keys", total)
	return nil
}

// snapshotTrustedPublishers returns the per-app trust list under
// the read lock. Returns nil when the cache is empty or the daemon
// is configured without a trust dir. Cheap to call on every signed
// deploy (single read-lock + map lookup).
func (h *Handler) snapshotTrustedPublishers(appID string) []cosign.TrustedPublisher {
	h.trustedPublishersMu.RLock()
	defer h.trustedPublishersMu.RUnlock()
	if !h.trustedPublishersCacheOK || h.trustedPublishersCache == nil {
		return nil
	}
	pubs, ok := h.trustedPublishersCache[appID]
	if !ok {
		return nil
	}
	out := make([]cosign.TrustedPublisher, len(pubs))
	copy(out, pubs)
	return out
}

// verifyImageSignature is the deploy-time verify hook (issue #472 /
// ADR-054). Branches on apps.require_signed; if true, calls
// pkg/cosign.VerifyImageSignature against the in-memory trust list
// and marks the deployment FAILED with the typed failure reason on
// either ErrSignatureMissing or ErrSignatureInvalid. Returns nil on
// success so buildImageLayer proceeds to PullDigest.
//
// The signatureMissing / signatureInvalid audit events are emitted
// here (not by apid) because imaged is the surface that observes the
// verify outcome — apid already emitted app.signed_image_accepted
// at accept-time (the "request passed the gate" event). The pair
// answers "request accepted but verify failed in imaged" without
// re-deriving from deployment status.
func (h *Handler) verifyImageSignature(ctx context.Context, app state.App, dep state.Deployment, ref string) error {
	pubs := h.snapshotTrustedPublishers(app.ID)
	if len(pubs) == 0 {
		// Defence-in-depth: apid's pre-flight already gated this
		// case, but if imaged is called outside the apid pipeline
		// (a future admin CLI, a test harness), refuse the
		// signature check rather than verify against an empty
		// allowlist.
		err := fmt.Errorf("%w: require_signed=true but no trusted publishers configured", cosign.ErrSignatureInvalid)
		_ = h.markDeployFailed(ctx, dep.ID, err, "signature_invalid: no trusted publishers")
		return err
	}
	signer, _, err := cosign.VerifyImageSignature(ctx, &ociImageSignaturePuller{oci: h.oci}, ref, pubs)
	if err == nil {
		h.log.Info("image signature verified", "app", app.Slug, "deployment", dep.ID, "signer", signer, "ref", ref)
		return nil
	}
	switch {
	case errors.Is(err, cosign.ErrSignatureMissing):
		_ = h.markDeployFailed(ctx, dep.ID, err, "signature_missing: no signature for ref")
		h.emitSignatureAudit(ctx, "app.signature_missing", app, dep, ref, "")
	case errors.Is(err, cosign.ErrSignatureInvalid):
		_ = h.markDeployFailed(ctx, dep.ID, err, "signature_invalid: no trusted publisher matched")
		h.emitSignatureAudit(ctx, "app.signature_invalid", app, dep, ref, "")
	default:
		_ = h.markDeployFailed(ctx, dep.ID, err, "signature_verify: registry error")
		h.emitSignatureAudit(ctx, "app.signature_invalid", app, dep, ref, "")
	}
	return err
}

// emitSignatureAudit fires the audit row for verify failures. Mirrors
// the audit.emit shape used by apid (data.app_id + data.deployment_id
// + data.ref + data.signer). signer is empty on missing/invalid.
//
// imaged-side audit events travel via pg_notify('audit_event') —
// pkg/audit (apid-side) subscribes and writes the rows. This keeps
// the audit write surface single-sourced (apid) while letting imaged
// surface operator-visible events.
func (h *Handler) emitSignatureAudit(ctx context.Context, kind string, app state.App, dep state.Deployment, ref, signer string) {
	h.log.Warn(kind, "app", app.Slug, "deployment", dep.ID, "ref", ref, "signer", signer)
	if h.notif != nil {
		payload := fmt.Sprintf(`{"kind":%q,"app_id":%q,"deployment_id":%q,"ref":%q,"signer":%q}`,
			kind, app.ID, dep.ID, ref, signer)
		_ = h.notif.Notify(ctx, "audit_event", payload)
	}
}

// ociImageSignaturePuller adapts oci.Puller to the minimal
// cosign.ImageSignaturePuller surface. PullDigest / FetchSignature
// are the two methods VerifyImageSignature needs; FetchSignature
// type-asserts to oci.ManifestPuller (which production's
// RegistryClient satisfies — the DefaultPuller used by unit tests
// does NOT) and falls back to ErrSignatureMissing when the
// assertion fails. This keeps the test surface green without
// importing a network into pkg/imaged unit tests.
type ociImageSignaturePuller struct {
	oci oci.Puller
}

func (p *ociImageSignaturePuller) ResolveDigest(ctx context.Context, ref string) (string, error) {
	return p.oci.PullDigest(ctx, ref)
}

func (p *ociImageSignaturePuller) FetchSignature(ctx context.Context, ref, digest string) ([]byte, error) {
	mp, ok := p.oci.(oci.ManifestPuller)
	if !ok {
		// DefaultPuller / offline fakes can't fetch cosign sigs.
		// Return ErrSignatureMissing so the verify hook reports
		// the customer-facing "no signature" reason rather than a
		// generic error.
		return nil, cosign.ErrSignatureMissing
	}
	// Cosign v2 signature location: the digest identifies the
	// well-known sha256-<hex>.sig tag in the same repo as ref.
	// We pass digest through PullBlob's (repo, digest) signature
	// unchanged — PullBlob will hit the registry's blob endpoint
	// for the same content-addressed blob.
	rc, err := mp.PullBlob(ctx, ref, digest)
	if err != nil {
		return nil, errors.Join(cosign.ErrSignatureMissing, err)
	}
	defer func() { _ = rc.Close() }()
	return io.ReadAll(rc)
}

func (h *Handler) WithFunctionRunnerNode22(p string) *Handler {
	h.functionRunnerNode22Path = p
	return h
}

// WithNodeName pins the handler to a compute node identity for split-box
// event routing. cmd/imaged wires this from FAAS_NODE_NAME, which is also
// the identity used by builderd when it emits snapshot_boot.
func (h *Handler) WithNodeName(name string) *Handler {
	h.nodeName = strings.TrimSpace(name)
	return h
}

// WithFunctionRunnerPython312 mirrors WithFunctionRunnerNode22 for python312.
func (h *Handler) WithFunctionRunnerPython312(p string) *Handler {
	h.functionRunnerPython312Path = p
	return h
}

// WithFunctionRunnerGo124 mirrors WithFunctionRunnerNode22 for go124.
// The runner binary at the wired path is the static Go executable built
// from guest/runners/go124; cmd/imaged passes the path through
// FAAS_FUNCTION_RUNNER_GO124. Empty in unit tests.
func (h *Handler) WithFunctionRunnerGo124(p string) *Handler {
	h.functionRunnerGo124Path = p
	return h
}

// WithFunctionRunnerGo124Alpine mirrors WithFunctionRunnerGo124 for the
// go124-alpine runtime. The runner binary is identical; only the base
// image's libc differs. Wired from cmd/imaged via
// FAAS_FUNCTION_RUNNER_GO124_ALPINE.
func (h *Handler) WithFunctionRunnerGo124Alpine(p string) *Handler {
	h.functionRunnerGo124AlpinePath = p
	return h
}

// WithFunctionRunnerNode24 mirrors WithFunctionRunnerNode22 for the
// node24 runtime. Wired from cmd/imaged via FAAS_FUNCTION_RUNNER_NODE24.
// The runner binary is built from guest/runners/node24 and is identical
// in shape to the node22 shim; only --runtime and --handler defaults
// differ (TestNode24RunnerHandlerDefault pins the /app/node24.js path).
func (h *Handler) WithFunctionRunnerNode24(p string) *Handler {
	h.functionRunnerNode24Path = p
	return h
}

// WithFunctionRunnerPython313 mirrors WithFunctionRunnerPython312 for
// the python313 runtime. Wired from cmd/imaged via
// FAAS_FUNCTION_RUNNER_PYTHON313. Handler path stays /app/handler.py
// (TestPython313RunnerHandlerDefault pins it).
func (h *Handler) WithFunctionRunnerPython313(p string) *Handler {
	h.functionRunnerPython313Path = p
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

// WithRuntimeBaseStaging enables the production on-demand runtime-base
// staging hook. It is separate from WithStorage so tests that use a local
// backend do not unexpectedly invoke mkfs or registry pulls.
func (h *Handler) WithRuntimeBaseStaging() *Handler {
	h.runtimeBaseStagingEnabled = true
	return h
}

// WithArtifactReplicator installs the optional split-box local artifact
// handoff. The hook is called only after the layer and its signature have
// both been written, and before the scheduler notification is sent.
func (h *Handler) WithArtifactReplicator(r ArtifactReplicator) *Handler {
	h.replicator = r
	return h
}

func (h *Handler) replicateLayer(ctx context.Context, layerKey string) error {
	if h.replicator == nil {
		return nil
	}
	if layerKey == "" {
		return errors.New("imaged: artifact replication: empty layer key")
	}
	if err := h.replicator.Replicate(ctx, layerKey); err != nil {
		return fmt.Errorf("imaged: replicate layer %q: %w", layerKey, err)
	}
	return nil
}

// WithOpsMetrics attaches the daemon-wide Prometheus registry. The
// OCI-pull observer reads from it inside aboveBaseLayers +
// PullLayers. Nil-safe (Observe* is no-op on a nil receiver).
// Mirrors pkg/builderd/builderd.go's WithOpsMetrics (PR #124,
// ADR-030) and cmd/apid/server.go's WithOpsMetrics (this PR).
func (h *Handler) WithOpsMetrics(ops *wire.OpsMetrics) *Handler {
	h.ops = ops
	return h
}

// WithAudit (issue #470 / PR C / ADR-074) attaches the daemon-
// wide audit seam. cmd/imaged wires the same audit.New(store,
// log, ops, "imaged") instance Loop uses. nil opts out (no row
// written; pre-PR-C fixtures keep their existing behaviour).
// Currently emits app.warm_snapshot_stale from
// MarkFCSnapshotsStale.
func (h *Handler) WithAudit(a *audit.Auditor) *Handler {
	h.audit = a
	return h
}

// WithGrypeRun replaces the default Grype subprocess invocation
// (issue #299 / ADR-075 PR-2). Default is nil, which falls back
// to the production runner that shells out to `grype dir:<dir>
// -o json` and parses the typed ScanResult. Tests inject a stub
// returning canned findings so the sidecar write is hermetic.
// Mirrors the `LayerBuilder` interface injection pattern — same
// Handler-Builder seam, same With* fluent setter shape.
func (h *Handler) WithGrypeRun(fn func(ctx context.Context, dir string) (*ScanResult, error)) *Handler {
	h.grypeRun = fn
	return h
}

// WithSyftRun injects the post-build SBOM generator (issue #299 /
// ADR-038 Phase 3). Mirrors WithGrypeRun's fluent setter shape.
// Tests wire a stub returning canned CycloneDX bytes; production
// leaves the field nil so the default subprocess runner in
// pkg/imaged/sbom.go fires.
func (h *Handler) WithSyftRun(fn func(ctx context.Context, dir string) ([]byte, error)) *Handler {
	h.syftRun = fn
	return h
}

// WithSecretScanRun injects the post-build secretscan walker
// (PR-A, imaged-layer secret scan). Mirrors WithGrypeRun's
// fluent setter shape. Tests wire a stub returning canned
// secretscan.Finding slices so the deploy-fail path is hermetic.
// Production leaves the field nil so the default walker in
// pkg/imaged/secretscan.go::runDeployLayerSecretScan fires —
// which uses the same pkg/secretscan.IsTextFile + ScanFile
// engine as the cmd/apid source-tree path so the two paths
// agree on patterns, providers, and severities.
//
// The "layer" argument on the callback is the per-walk source
// label ("app" | "sidecar-<slug>") that gets stamped onto every
// finding in the audit row — the API surface needs the label
// to know whether a finding is in the main image or in a
// sidecar (different blast radius).
func (h *Handler) WithSecretScanRun(fn func(ctx context.Context, dir, layer string) ([]secretscan.Finding, error)) *Handler {
	h.secretScanRun = fn
	return h
}

// runSecretScan dispatches to the wired secretScanRun callback
// when present, else falls back to the package-level default
// walker. Mirrors runGrype above (handler.go:579-584). The
// "layer" argument is the per-walk source label forwarded into
// the runner so the audit row can attribute findings to the
// right image segment.
func (h *Handler) runSecretScan(ctx context.Context, dir, layer string) ([]secretscan.Finding, error) {
	if h.secretScanRun != nil {
		return h.secretScanRun(ctx, dir, layer)
	}
	return runDeployLayerSecretScan(ctx, dir, layer)
}

// WithVMMClient wires the vmmd gRPC client used by the
// ADR-053 parent-ref staging branch of EnsureBaseExt4. The
// client is nil-safe: the legacy "apply all layers" path
// (RuntimeGo124, RuntimeGo124Alpine, RuntimeDebianParent
// itself, builder-base) stays operational without a client
// wired; only the parent-ref branch (RuntimeNode22/24 +
// RuntimePython312/313) fails loud when the client is nil
// and the parent ext4 isn't staged.
//
// Production cmd/imaged constructs the client against
// FAAS_VMM_SOCK (default unix:///run/faas/vmmd.sock,
// ADR-015) and calls this once at startup. The client is
// reused across staging cycles; cmd/imaged also calls
// h.vmmClient.Close() on SIGTERM so the dial doesn't leak.
func (h *Handler) WithVMMClient(c VMMClientIface) *Handler {
	h.vmmClient = c
	return h
}

// WithSecretboxIdentity wires the host age identity used to
// unseal per-app private-registry Basic Auth passwords in the
// pull path (issue #461 / ADR-062). Mirrors the apid
// FAAS_HOST_AGE_IDENTITY_PATH loading — same file, same key,
// same in-process lifetime.
//
// The identity is the SAME age.X25519Identity vmmd uses for
// seal/unseal across the box. imaged does NOT load the
// recipient; that's apid-only. The identity is required only
// when an app has a private-registry credential stored; nil =
// anonymous pulls for every app (matches Free plan + no-cred
// Hobby paths). Unit tests pass nil — the registry credential
// path is exercised by a separate hermetic test file
// (handler_auth_test.go).
func (h *Handler) WithSecretboxIdentity(ident *age.X25519Identity) *Handler {
	h.secretboxIdentity = ident
	return h
}

// CloseVMMClient closes the vmmd gRPC client wired at startup.
// Exposed (rather than a method on VMMClient only) so cmd/imaged
// can call h.CloseVMMClient() on SIGTERM without exposing the
// unexported vmmClient field. Idempotent on a nil receiver.
func (h *Handler) CloseVMMClient() error {
	if h == nil || h.vmmClient == nil {
		return nil
	}
	return h.vmmClient.Close()
}

// runGrype dispatches to the injected grypeRun or falls back to
// the default subprocess runner (issue #299 / ADR-075 PR-2).
// The default shells out to `grype dir:<dir> -o json` and parses
// the matches[].vulnerability.severity counts into a typed
// ScanResult. Errors and nil results are surfaced to the caller
// (the sidecar-write site fail-closed with a CRITICAL=9999
// placeholder). Production wires the default; tests wire a stub
// via WithGrypeRun.
func (h *Handler) runGrype(ctx context.Context, dir string) (*ScanResult, error) {
	if h.grypeRun != nil {
		return h.grypeRun(ctx, dir)
	}
	return defaultGrypeRun(ctx, dir)
}

// runDeployScan runs the per-deploy grype scan and stamps the
// result on the deployment row (issue #464 / ADR-075 / PR-3).
// The scan reads the per-app layer ext4 (appsRoot/<slug>/<depID>.ext4)
// and writes scan_result + scan_status + scanned_at via
// state.Store.UpsertDeploymentScanResult. The method is
// best-effort — both the grype runner error path and the SQL
// write error path log at WARN and return so the deploy's
// snapshotting transition fires regardless (AC #4: CRITICAL-CVE
// images deploy successfully; AC #1: scan lands within 5 min
// via this hook, well under the SLA).
//
// Runs synchronously in the deploy-complete path (post
// SetDeploymentRootfs, pre snapshotting transition). Grype's
// ~1-3s per-layer cost is acceptable here because the build
// path already spends seconds-to-minutes in
// aboveBaseLayers + the snapshot prime; the scan is one more
// step in a pipeline that's already gated by build cold-boot.
//
// On a retry-exhausted grype failure, status='failed' is
// stamped with the error in scan_result's Error field. The
// dashboard renders a "scan failed" chip; the deploy itself
// is unaffected.
//
// stageScanExt4 resolves the scan-source ext4 for the per-deploy
// grype scan. The grype subprocess takes `grype dir:<path>` where
// `<path>` may be a file or directory (it accepts both); the
// helper returns a path + cleanup func that the caller defers.
//
// Routing (ADR-054 acceptance closure, Tier 1 Phase 3):
//
//   - LocalStorageBackend: return the canonical appsRoot path
//     unchanged. Single-box behaviour preserved 1:1; the bytes
//     are read off `/var/lib/faas/apps/<slug>/<depID>.ext4`.
//   - Anything else (PrefixRouter, OCI registry, etc.): stage the
//     blob to a tempdir under h.appsRoot (or os.TempDir() if the
//     appsRoot isn't writable) and return that path. The caller
//     MUST defer the returned cleanup func to remove the staged
//     file. The LocalCacheBackend wrapper on top of the OCI
//     registry gives the second + third scan of the same layer
//     a free cache hit — staging is not a re-fetch.
//
// Returns ("", noop, err) on a path-traversal slug (the
// appsRootPath guard rejects bad slugs) or a Get error from the
// backend. The grype runner is NOT called in that case; the
// caller stamps scan_status='failed' with the error string.
func (h *Handler) stageScanExt4(ctx context.Context, be storage.StorageBackend, app state.App, dep state.Deployment) (string, func(), error) {
	// Local backend short-circuit. The legacy appsRootPath stays
	// because SetDeploymentRootfs (handler.go:1334+1380+1697)
	// stamps it onto the deployment row as a diagnostic column.
	if _, isLocal := be.(*storage.LocalStorageBackend); isLocal {
		p := h.appsRootPath(app.Slug, dep.ID)
		if p == "" {
			return "", func() {}, errors.New("slug/path-traversal guard rejected")
		}
		return p, func() {}, nil
	}

	// Remote backend (OCI registry, hybrid router, …). Stage
	// the ext4 bytes to a local tempdir so grype dir:<path> can
	// scan a filesystem path regardless of where the canonical
	// bytes live.
	appsKey := sched.AppLayerKey(app.Slug, dep.ID)
	rc, err := be.Get(ctx, appsKey)
	if err != nil {
		return "", func() {}, fmt.Errorf("imaged: scan stage backend.Get(%q): %w", appsKey, err)
	}
	defer func() { _ = rc.Close() }()

	// MkdirTemp roots under h.appsRoot (the daemon's writable
	// scratch area) when possible; falls back to os.TempDir()
	// if the appsRoot doesn't exist or isn't writable. The
	// tempdir itself becomes the grype source — grype
	// dir:<tempdir> walks all files under it, of which there
	// is exactly one (the staged ext4).
	stageRoot := h.appsRoot
	if stageRoot == "" {
		stageRoot = os.TempDir()
	}
	stageDir, mkErr := os.MkdirTemp(stageRoot, "imaged-scan-")
	if mkErr != nil {
		// appsRoot wasn't writable; fall back to os.TempDir()
		// to avoid taking the scan path down with the daemon.
		stageDir, mkErr = os.MkdirTemp(os.TempDir(), "imaged-scan-")
		if mkErr != nil {
			return "", func() {}, fmt.Errorf("imaged: scan stage MkdirTemp: %w", mkErr)
		}
	}
	stagedPath := filepath.Join(stageDir, "rootfs.ext4")
	out, openErr := os.OpenFile(stagedPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if openErr != nil {
		_ = os.RemoveAll(stageDir)
		return "", func() {}, fmt.Errorf("imaged: scan stage OpenFile(%q): %w", stagedPath, openErr)
	}
	if _, copyErr := io.Copy(out, rc); copyErr != nil {
		_ = out.Close()
		_ = os.RemoveAll(stageDir)
		return "", func() {}, fmt.Errorf("imaged: scan stage io.Copy: %w", copyErr)
	}
	if syncErr := out.Sync(); syncErr != nil {
		_ = out.Close()
		_ = os.RemoveAll(stageDir)
		return "", func() {}, fmt.Errorf("imaged: scan stage Sync: %w", syncErr)
	}
	if closeErr := out.Close(); closeErr != nil {
		_ = os.RemoveAll(stageDir)
		return "", func() {}, fmt.Errorf("imaged: scan stage Close: %w", closeErr)
	}
	cleanup := func() { _ = os.RemoveAll(stageDir) }
	return stageDir, cleanup, nil
}

// Marshal contract: *ScanResult + Error field are plain Go
// types (ints, string) so json.Marshal can't fail at runtime.
// A future field that breaks this invariant (e.g. a chan)
// surfaces as an immediate panic in tests.
//
// Scan source: ADR-054 acceptance closure (Tier 1 Phase 3)
// routes the scan through the wired StorageBackend. Under
// `FAAS_STORAGE_BACKEND=oci`, the layer ext4 lives in the
// registry, not on local disk — the helper `stageScanExt4`
// materializes the bytes to a tempdir so the grype subprocess
// has a filesystem path to scan. The legacy appsRootPath is
// preserved as the SetDeploymentRootfs DB column, untouched.
func (h *Handler) runDeployScan(ctx context.Context, app state.App, dep state.Deployment) {
	if h.store == nil || h.log == nil {
		// Defensive: tests that build a Handler without wiring
		// store/log skip the scan entirely (no row to write, no
		// log channel). Production wires both at line 87+195
		// of cmd/imaged/main.go, so the nil branches are
		// unreachable in prod.
		return
	}
	start := time.Now()
	be, err := h.storageFor()
	if err != nil {
		// storageFor only errors when h.appsRoot is empty /
		// contains NUL bytes and no WithStorage was wired. The
		// defensive branch is unreachable in production (the
		// daemon wires WithStorage at cmd/imaged/main.go:239).
		h.log.Warn("imaged: per-deploy scan skipped, storageFor",
			"deployment", dep.ID, "app", app.Slug, "err", err)
		failedResult := &ScanResult{Error: "storageFor: " + err.Error()}
		b, mErr := json.Marshal(failedResult)
		if mErr != nil {
			return
		}
		if writeErr := h.store.UpsertDeploymentScanResult(ctx, dep.ID, b, "failed"); writeErr != nil {
			h.log.Warn("imaged: stamp scan_status=failed",
				"deployment", dep.ID, "err", writeErr)
		}
		h.ops.ObserveDeployScanDuration(app.Slug, "skipped", time.Since(start))
		h.ops.ObserveDeployScanTotal(app.Slug, "skipped")
		return
	}
	scanDir, cleanup, err := h.stageScanExt4(ctx, be, app, dep)
	if err != nil {
		// Defensive: path-traversal guard, Get error, or
		// MkdirTemp failure. Stamp scan_status='failed' with
		// the reason so the dashboard renders the failure
		// distinctly from a grype runner error (AC #4 of
		// ADR-075: don't block the deploy).
		h.log.Warn("imaged: per-deploy scan skipped, stage",
			"deployment", dep.ID, "app", app.Slug, "err", err)
		failedResult := &ScanResult{Error: err.Error()}
		b, mErr := json.Marshal(failedResult)
		if mErr != nil {
			return
		}
		if writeErr := h.store.UpsertDeploymentScanResult(ctx, dep.ID, b, "failed"); writeErr != nil {
			h.log.Warn("imaged: stamp scan_status=failed",
				"deployment", dep.ID, "err", writeErr)
		}
		h.ops.ObserveDeployScanDuration(app.Slug, "skipped", time.Since(start))
		h.ops.ObserveDeployScanTotal(app.Slug, "skipped")
		return
	}
	defer cleanup()
	result, err := h.runGrype(ctx, scanDir)
	if err != nil {
		// Fail the scan, not the deploy. Log the grype error
		// so the operator sees the underlying cause.
		h.log.Warn("imaged: per-deploy grype scan failed",
			"deployment", dep.ID, "app", app.Slug, "err", err)
		failedResult := &ScanResult{Error: err.Error()}
		b, mErr := json.Marshal(failedResult)
		if mErr != nil {
			h.log.Warn("imaged: marshal failed scan result",
				"deployment", dep.ID, "err", mErr)
			return
		}
		if writeErr := h.store.UpsertDeploymentScanResult(ctx, dep.ID, b, "failed"); writeErr != nil {
			h.log.Warn("imaged: stamp scan_status=failed",
				"deployment", dep.ID, "err", writeErr)
		}
		// ADR-075: surface the failure as a metric increment so
		// the §12 dashboard panel can render a 5-min red rate.
		// Duration histogram is observed on the failed branch too
		// — the 5-min SLA bucket catches stuck scans even when
		// the grype runner returns quickly.
		h.ops.ObserveDeployScanDuration(app.Slug, "failed", time.Since(start))
		h.ops.ObserveDeployScanTotal(app.Slug, "failed")
		return
	}
	b, mErr := json.Marshal(result)
	if mErr != nil {
		h.log.Warn("imaged: marshal scan result",
			"deployment", dep.ID, "err", mErr)
		return
	}
	if err := h.store.UpsertDeploymentScanResult(ctx, dep.ID, b, "complete"); err != nil {
		h.log.Warn("imaged: stamp scan_status=complete",
			"deployment", dep.ID, "err", err)
		return
	}
	// ADR-075: stamped-clean. Record wall-clock duration +
	// per-severity counts so the §12 dashboard panel can graph
	// the fleet-deploy scan latency over a 5-min window.
	h.ops.ObserveDeployScanDuration(app.Slug, "complete", time.Since(start))
	h.ops.ObserveDeployScanTotal(app.Slug, "complete")
	for sev, n := range map[string]int{
		"CRITICAL": result.Critical,
		"HIGH":     result.High,
		"MEDIUM":   result.Medium,
		"LOW":      result.Low,
		"UNKNOWN":  result.Unknown,
	} {
		h.ops.ObserveDeployScanVulns(app.Slug, sev, n)
	}
	h.log.Info("imaged: per-deploy scan stamped",
		"deployment", dep.ID, "app", app.Slug,
		"critical", result.Critical, "high", result.High,
		"medium", result.Medium, "low", result.Low, "unknown", result.Unknown)
}

// errImageSecretDetected is the typed sentinel runDeployLayerSecretScan
// returns to markDeployFailed on a finding. Mirrors the
// errStatefulViolation style in pkg/imaged/base.go (G13 closure) —
// markDeployFailed lifts the sentinel to the wire-stable code
// "image_secret_detected" via the error_code column. ADR-075 ships
// the same pattern for grype-side failures; this is the secret-side
// analog. Free-text column (migration 00021), no schema widening
// required.
var errImageSecretDetected = errors.New("imaged: secret-shaped values detected in image layer")

// runDeployLayerSecretScan runs the post-build secretscan walker
// against the per-deploy ext4 for ONE layer and returns the typed
// findings WITHOUT writing the audit row or failing the deploy.
// The caller (handleDeployment) accumulates findings across the
// main "app" layer + each "sidecar-<slug>" layer and stamps the
// audit row once at the end — otherwise each layer scan overwrites
// the row and a multi-sidecar deploy with findings-in-sidecar-A
// only would lose them when sidecar-B's clean walk stamps a
// fresh "complete, 0 findings" row.
//
// Mirrors runDeployScan structurally (same stageScanExt4 helper,
// same observation log line) but the posture is loud-fail: a
// single finding fails the deploy with errImageSecretDetected.
// The grype CVE path is best-effort by design (ADR-075 AC #4 —
// don't block deploys on supply-chain metadata); the secret path
// is intentionally NOT — secrets are a security boundary, not
// metadata.
//
// `layer` is the per-walk source label ("app" for the main image,
// "sidecar-<slug>" for each sidecar). It's stamped into every
// SecretFinding.Layer in the audit row so the dashboard can render
// which ext4 the finding came from.
//
// Returns findings (possibly nil for a clean walk) and a walk-level
// error. A walk-level error is non-fatal for the scan itself —
// the caller logs and continues; secrets are a security scan, not
// a deploy precondition. Storage/stage failures also return
// non-fatal errors. The CALLER decides how to react to a hit:
//
//	findings, walkErr := h.runDeployLayerSecretScan(...)
//	if walkErr != nil { /* log + skip */ }
//	if len(findings) > 0 { /* accumulate + fail */ }
//
// Failure modes:
//
//   - storageFor error or stageScanExt4 error: log WARN, return
//     (nil, err). Matches runDeployScan — if we can't even stage
//     the ext4, the build itself is already in trouble, and we
//     don't want to mask the root cause with a noise secret-scan
//     failure. The build error path will surface downstream.
//   - runSecretScan returns walk-level error: log WARN + return
//     (nil, err). Best-effort matches cmd/apid::scanExtractedTreeSecrets.
//   - runSecretScan returns findings (possibly empty): return the
//     findings slice; the caller accumulates + stamps once.
//     (best-effort on the write itself, but the deploy MUST fail),
//     then markDeployFailed with errImageSecretDetected. Returns
//     true so the caller short-circuits the snapshotting transition.
//
// The markDeployFailed path is what guards the security boundary;
// even if the audit-row write fails (DB blip, schema drift), the
// deploy fails loudly so the customer's next attempt sees a clean
// state.
func (h *Handler) runDeployLayerSecretScan(ctx context.Context, app state.App, dep state.Deployment, layer string) ([]secretscan.Finding, error) {
	if h.store == nil || h.log == nil {
		// Defensive: tests that build a Handler without wiring
		// store/log skip the scan entirely (no row to write, no
		// log channel). Production wires both at cmd/imaged wiring
		// so the nil branches are unreachable in prod.
		return nil, nil
	}
	start := time.Now()
	be, err := h.storageFor()
	if err != nil {
		h.log.Warn("imaged: layer secret scan skipped, storageFor",
			"deployment", dep.ID, "app", app.Slug, "layer", layer, "err", err)
		return nil, err
	}
	scanDir, cleanup, err := h.stageScanExt4(ctx, be, app, dep)
	if err != nil {
		h.log.Warn("imaged: layer secret scan skipped, stage",
			"deployment", dep.ID, "app", app.Slug, "layer", layer, "err", err)
		return nil, err
	}
	defer cleanup()
	findings, walkErr := h.runSecretScan(ctx, scanDir, layer)
	if walkErr != nil {
		// Walk-level error: log + return. The build itself is
		// unaffected — secrets are a security scan, not a deploy
		// precondition. (Loud-fail posture is for PATTERN-LEVEL
		// findings, not walk errors. cmd/apid::scanExtractedTreeSecrets
		// makes the same distinction.)
		h.log.Warn("imaged: layer secret scan walk failed",
			"deployment", dep.ID, "app", app.Slug, "layer", layer,
			"err", walkErr, "elapsed", time.Since(start))
		return nil, walkErr
	}
	if len(findings) == 0 {
		h.log.Info("imaged: layer secret scan clean",
			"deployment", dep.ID, "app", app.Slug, "layer", layer,
			"elapsed", time.Since(start))
	}
	return findings, nil
}

// storageFor returns the wired StorageBackend, building a default
// per-appsRoot LocalStorageBackend on first use. The lazy-default keeps
// existing callers — every test that never calls WithStorage — running
// against the legacy path without a churn. production calls WithStorage
// in cmd/imaged so the default is never exercised in prod.
//
// Issue #197 B3.8: previously panicked on a bad appsRoot (empty path /
// NUL bytes). Tests can now reach this code path when a Handler is
// constructed without WithStorage AND appsRoot is empty — return the
// error instead of panicking. Production wiring (cmd/imaged calls
// WithStorage unconditionally) is unaffected.
func (h *Handler) storageFor() (storage.StorageBackend, error) {
	if h.storage != nil {
		return h.storage, nil
	}
	// Safe-by-default: NewLocalStorageBackend only errors on empty root or
	// NUL bytes in the path (appsRoot is supplied by cmd/imaged and tests
	// use t.TempDir()). Returning the error lets callers degrade to a
	// logged WARN instead of taking the daemon down mid-deploy.
	be, err := storage.NewLocalStorageBackend(h.appsRoot)
	if err != nil {
		return nil, fmt.Errorf("imaged: storageFor default backend: %w", err)
	}
	h.storage = be
	return be, nil
}

// appsRootPath returns the on-disk legacy path the legacy code path
// stamped into deployments.rootfs_path. Used to keep the SetDeploymentRootfs
// row contract identical to pre-#96 even when the new Storage path is
// used to write the ext4.
//
// Defensive path-traversal guard (issue #464 / ADR-075 review
// finding): a slug that contains `..` or starts with `/` would
// resolve outside appsRoot after filepath.Join's Clean pass;
// grype dir:<escaped> would then scan a file outside the intended
// per-app tree. Apid's slug validator (cmd/apid/handlers.go:389)
// already enforces `^[a-z0-9]([a-z0-9-]{1,38})[a-z0-9]$` so the
// vector is unreachable in the production write path. The
// guard here is defense-in-depth: a row created by a future
// admin SQL path (or a corrupt DB) cannot escape the tree.
//
// Returns "" when the slug is malformed or the resolved path
// escapes appsRoot. Callers MUST check the "" return — the
// grype-runner or ext4-write path that consumes the result
// skips the operation on empty.
func (h *Handler) appsRootPath(slug, deploymentID string) string {
	// Sanity-check the slug. Mirrors cmd/apid/handlers.go:389's
	// slugRe. A future widening of the apid pattern must widen
	// this check in lockstep, or the layers diverge — the
	// isSlugSafe / safeDeploymentID predicates are the single
	// machine-checkable seam.
	if !isSlugSafe(slug) || !isDeploymentIDSafe(deploymentID) {
		return ""
	}
	cleaned := filepath.Join(h.appsRoot, slug, deploymentID+".ext4")
	// Belt-and-braces: even with the slug regex, a stale
	// appsRoot containing a trailing slash or symlink could
	// let the join resolve outside. absPrefix pins the
	// resolved prefix to the canonical appsRoot.
	absRoot, err := filepath.Abs(h.appsRoot)
	if err != nil {
		return ""
	}
	absJoined, err := filepath.Abs(cleaned)
	if err != nil {
		return ""
	}
	if !strings.HasPrefix(absJoined, absRoot+string(filepath.Separator)) &&
		absJoined != absRoot {
		return ""
	}
	return cleaned
}

// isSlugSafe mirrors cmd/apid/handlers.go:389's slugRe without
// importing the regex package (the slug is a hot-path field on
// every deploy). Hand-rolled to keep pkg/imaged dep-light.
func isSlugSafe(slug string) bool {
	if len(slug) < 3 || len(slug) > 40 {
		return false
	}
	for i, r := range slug {
		switch {
		case r >= 'a' && r <= 'z':
		case r >= '0' && r <= '9':
		case r == '-':
		case i == 0 && r >= 'a' && r <= '9':
		default:
			return false
		}
	}
	return true
}

// isDeploymentIDSafe is a defensive check on the deployment
// identifier. The column is a UUID (or short hash) emitted by
// apid — the constraint here is that it must not contain a
// path separator or `..` so the filepath.Join above pins the
// result under appsRoot.
func isDeploymentIDSafe(id string) bool {
	if id == "" || len(id) > 64 {
		return false
	}
	for _, r := range id {
		if r == '/' || r == '\\' || r == 0 || r == '.' {
			return false
		}
	}
	return true
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
	// PR-B: NotifyBuildQueued arm removed (builderd owns the channel now).
	case db.NotifySnapshotBoot:
		var p snapshotBootPayload
		if err := json.Unmarshal([]byte(n.Payload), &p); err != nil {
			h.log.Warn("imaged: bad snapshot_boot payload", "err", err)
			return
		}
		if !handlesSnapshotBoot(h.nodeName, p.NodeID) {
			h.log.Debug("imaged: ignoring snapshot_boot for sibling node",
				"owner_node", p.NodeID, "local_node", h.nodeName,
				"deployment", p.DeploymentID)
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
	case "trusted_signer_changed":
		// Issue #472 / ADR-054: apid emits this on every CRUD op on
		// app_trusted_signers. We refresh the in-memory cache so a
		// freshly-onboarded publisher takes effect on the next
		// deploy without an imaged restart. The refresh is cheap
		// (file IO of ~250 bytes per PEM, max 16 files per Scale
		// plan) so we do it inline rather than punting to a
		// goroutine.
		if err := h.refreshTrustedPublishers(); err != nil {
			h.log.Warn("imaged: trusted_signer_changed refresh failed", "err", err)
		}
	case "audit_event":
		// imaged-side audit emits (app.signature_missing /
		// app.signature_invalid) are routed via pg_notify and
		// eventually written by apid-side pkg/audit. The Loop in
		// pkg/loop subscribes; this arm is a no-op so the typed
		// payload reaches pkg/audit unchanged.
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

// PR-B: buildQueuedPayload and (*Handler).handleBuildQueued were
// removed. imaged is no longer subscribed to db.NotifyBuildQueued
// (builderd owns the queue via the durable worker + LISTEN fast path,
// see ADR-031). The build → OCI-image conversion happens in the
// snapshot_boot handler below, which fires AFTER builderd stamps
// rootfs_path onto the deployment row.

// snapshotWrittenPayload is the JSON shape schedd emits on `snapshot_written`
// after a Prime/Park writes the blob via vmmd (ADR-018, see pkg/db.NotifyChannels).
// imaged is the sole writer to the snapshots table, so it records the row.
type snapshotWrittenPayload struct {
	DeploymentID string `json:"deployment_id"`
	NodeID       string `json:"node_id,omitempty"`
	VMStatePath  string `json:"vmstate_path"`
	// StorageKey is the canonical StorageBackend key (issue #96,
	// ADR-025 axis 2). schedd populates it on the snapshot_written
	// payload; imaged copies it onto the snapshots row so Wake can
	// read it back without recomputing the canonical form.
	StorageKey   string `json:"storage_key"`
	MemBytes     int64  `json:"mem_bytes"`
	VMStateBytes int64  `json:"vmstate_bytes"`
	FCVersion    string `json:"fc_version"`
	// Tier (issue #470 / PR #470-FU-B) is the snapshot tier this
	// row belongs to: "init" (taken right after guest-init binds
	// :8080; restore pays framework warmup) or "warm" (taken
	// after N successful requests meet warm_snapshot_min_ms,
	// when the framework is hot). Empty falls back to "init"
	// (the legacy pre-#470 default) so the CHECK constraint and
	// the existing memory tier-untagged writes stay valid.
	// schedd sources this from its vmmd_grpc.PauseAndSnapshot
	// call (PR #470-FU-A wires the tier through); until that
	// lands, this field is empty and the row is recorded as
	// init per the DB column default.
	Tier string `json:"tier,omitempty"`
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
	NodeID       string `json:"node_id,omitempty"`
}

// handlesSnapshotBoot is the split-box ownership gate. The event publisher
// and consumer both use the registered compute_node name. Empty values are
// accepted for the legacy one-box shape; once both sides are named, a
// notification can only be consumed by its owner. A named daemon also
// accepts an unlabelled event during a rolling upgrade, because older
// builderd versions did not include node_id yet.
func handlesSnapshotBoot(localNode, ownerNode string) bool {
	localNode = strings.TrimSpace(localNode)
	ownerNode = strings.TrimSpace(ownerNode)
	return localNode == "" || ownerNode == "" || localNode == ownerNode
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
func (h *Handler) handleDeployment(ctx context.Context, p deploymentChangedPayload) (err error) {
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

	if err := h.transitionWithStage(ctx, dep.ID, state.StageSourceDownload, state.StageDependencyRestore, state.DeployBuilding, ""); err != nil {
		return err
	}
	// Issue #195 B1.5: every error path from here forward MUST land
	// the deployment row in a terminal-good state. The inner
	// buildImageLayer/buildFunctionLayer paths call markDeployFailed
	// on their own failures; the defer catches the windows they
	// miss (transition failures between DeployBuilding and the
	// build, notif.Notify failures after a successful build, and
	// any unknown-kind dispatch). The defer installs AFTER the
	// status guard so a pg_notify redelivery that bails at
	// dep.Status != DeployPending never fires a spurious mark.
	defer h.markFailedOnUnhandledError(ctx, dep.ID, &err)

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
		// PR-A imaged-layer secret scan orchestration lives
		// inside buildImageLayer (it needs the layer-findings
		// accumulator across both the main layer and each
		// sidecar so the audit row is stamped ONCE at the end).
		// Function deploys are out of scope — they don't go
		// through buildImageLayer; the runner tarball is
		// scanned at apid source-tree time.
		if err := h.buildImageLayer(ctx, app, dep, acct); err != nil {
			return err
		}
	}

	// Per-deploy grype scan (issue #464 / ADR-075 / PR-3).
	// Runs AFTER the per-app ext4 layer is built + published
	// (buildImageLayer/buildFunctionLayer both stamped
	// SetDeploymentRootfs above) and BEFORE the
	// pending→snapshotting transition. The scan is
	// best-effort observability — a grype runner error or
	// SQL write failure is logged at WARN and dropped; the
	// deploy's snapshotting transition still fires so the
	// customer contract (AC #4: CRITICAL-CVE images deploy
	// successfully) holds. The scan lands on the deployment
	// row within seconds of the layer publish (well inside
	// the 5-min SLA from AC #1).
	h.runDeployScan(ctx, app, dep)
	// Runtime bases are staged on demand so a fresh bare-metal node can
	// become ready without building every supported runtime at startup.
	if err := h.ensureDeploymentRuntimeBase(ctx, app); err != nil {
		return err
	}

	// ADR-117 PR-A review fix (F1): close the security_scan stage
	// and open image_build. The 3 transition sites that previously
	// went dep_restore → image_build now go dep_restore →
	// security_scan; this is the seam that closes security_scan
	// after runDeployScan completes (best-effort — a scan
	// failure logs Warn in runDeployScan itself, the stage
	// transition still fires so the customer's ticker sees the
	// image_build row).
	if err := h.transitionWithStage(ctx, dep.ID, state.StageSecurityScan, state.StageImageBuild, state.DeployImaging, ""); err != nil {
		return err
	}

	if err := h.transitionWithStage(ctx, dep.ID, state.StageImageBuild, state.StageSnapshotPrepare, state.DeploySnapshotting, ""); err != nil {
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
// resolveRegistryAuth looks up a per-app private-registry Basic Auth
// credential keyed by the OCI ref's host, transiently unseals the
// password via the wired secretbox identity, and returns the
// *oci.BasicAuth the puller should carry on its realm request. The
// plaintext lives only inside this call frame and is GC'd on return —
// NEVER attached to dep, audit, log, or the returned error.
//
// Three return paths:
//   - (nil, nil): no credential stored for this host → anonymous pull
//     (Free-plan / Hobby-without-private / public-registry case).
//   - (auth, nil): credential found + unsealed successfully → puller
//     threads `auth` into PullXxxWithAuth.
//   - (nil, err): non-ErrNotFound lookup failure, or unseal failure →
//     caller fails the deployment loudly (a sealed blob we can't open
//     is a configuration error, not a soft miss).
//
// Issue #461 / ADR-062: keyed by (accountID, appID, host) — the
// (accountID, appID) tuple is the IDOR-safe guard the apid handlers
// already enforce, and the host comes from the OCI ref (NOT the
// customer-supplied deployment image_ref). Pulling the host from the
// parsed ref ensures a customer can never accidentally widen the
// credential to a different registry by manipulating a request body.
func (h *Handler) resolveRegistryAuth(ctx context.Context, app state.App, host string) (*oci.BasicAuth, error) {
	if h.secretboxIdentity == nil {
		return nil, nil
	}
	if host == "" {
		return nil, nil
	}
	cred, err := h.store.GetAppRegistryCredential(ctx, app.AccountID, app.ID, host)
	if err != nil {
		if errors.Is(err, state.ErrNotFound) {
			return nil, nil
		}
		return nil, fmt.Errorf("imaged: lookup registry credential for %s: %w",
			logsanitize.Field(host), err)
	}
	// OpenBytes returns (namespace, plaintext, err); the
	// namespace is the literal "registry_creds" we sealed
	// under at PUT time (mirrors handlers_secrets.go's
	// app_secret namespace check). A namespace mismatch
	// means the envelope was sealed for a different
	// secretbox slot — refuse rather than passing through.
	ns, plaintext, err := secretbox.OpenBytes(h.secretboxIdentity, cred.PasswordEncrypted)
	if err != nil {
		// Don't echo the unseal error verbatim — it can include
		// corrupted-byte markers that aid an attacker probing the
		// seal format. Replace with a fixed-shape message.
		return nil, fmt.Errorf("imaged: open registry credential for %s: %w",
			logsanitize.Field(host), err)
	}
	if ns != "registry_creds" {
		return nil, fmt.Errorf("imaged: open registry credential for %s: namespace=%q",
			logsanitize.Field(host), ns)
	}
	return &oci.BasicAuth{
		Username: cred.Username,
		Password: string(plaintext),
	}, nil
}

// markRegistryCredentialUsed records that the credential for host
// was used to successfully authenticate a pull (issue #461 /
// ADR-062). The update is best-effort — a non-fatal warn log on
// failure. The schema's last_used_at is observed metadata
// (operator dashboards), NOT a deployment precondition, so a
// transient update failure must not abort an otherwise-successful
// build. Returns immediately on nil appAuth (anonymous pull).
//
// Callers MUST invoke this AFTER a successful authenticated pull
// and never on error paths — the contract per ADR-062 §Decision 8
// is "LastUsedAt updated only after a successful authenticated
// pull".
//
// Failure channel: every failed MarkAppRegistryCredentialUsed
// increments the daemon-wide
// imaged_registry_credential_mark_used_failures_total counter
// (ADR-062 / issue #461) so operators can detect a lagging
// last_used_at — non-fatal here would otherwise be silent. h.ops
// is nil-checked for unit-test paths that don't wire metrics.
func (h *Handler) markRegistryCredentialUsed(ctx context.Context, app state.App, host string, appAuth *oci.BasicAuth) {
	if appAuth == nil || host == "" {
		return
	}
	if err := h.store.MarkAppRegistryCredentialUsed(ctx, app.AccountID, app.ID, host); err != nil {
		// Warn, never fail. The deployment already succeeded.
		h.log.Warn("imaged: mark registry credential used failed",
			"registry", logsanitize.Field(host),
			"err", err.Error())
		if c := h.ops.RegistryCredentialMarkUsedFailures(); c != nil {
			c.Inc()
		}
	}
}

// ref is the full OCI reference (`host/repo@sha256:...`) apid stored into
// dep.ImageDigest. We use the full ref (not just the bare digest) for every
// OCI call so the puller dials the right registry — a bare digest resolves
// to docker.io/library/sha256:... and dials the wrong host for non-Docker
// deploys (issue #53 / M5 acceptance on Lima).
func (h *Handler) buildImageLayer(ctx context.Context, app state.App, dep state.Deployment, acct state.Account) error {
	ref := dep.ImageDigest

	// Issue #461 / ADR-062: resolve the per-app private-registry Basic
	// Auth credential (if any) keyed by the OCI ref's host. The plaintext
	// password lives only inside this call frame. We capture the host
	// here so both the M5 fallback (legacy PullDigest/PullLayers) and the
	// M6 two-drive (aboveBaseLayers) branches can thread it through. A
	// lookup or unseal failure here fails the deployment loudly — a
	// credential we can't open is a configuration error, not a soft miss.
	//
	// refHost is the parsed registry host ("ghcr.io",
	// "registry.gregale.dev", ...) used as the credential key.
	// Empty string means anonymous — either the ref failed to
	// parse (StatefulDenyListMatch below will reject anyway) or
	// the registry host is empty (docker.io default).
	var appAuth *oci.BasicAuth
	refHost := ""
	if parsedRef, parseErr := oci.ParseReference(ref); parseErr == nil {
		refHost = parsedRef.APIHost()
		appAuth, parseErr = h.resolveRegistryAuth(ctx, app, refHost)
		if parseErr != nil {
			_ = h.markDeployFailed(ctx, dep.ID, parseErr, "imaged: resolve registry auth")
			return parseErr
		}
	}
	defer func() {
		// Zero the plaintext on the way out so the GC can
		// reclaim it even if the runtime kept a reference to
		// the struct's backing memory. Best-effort — Go does
		// not guarantee zeroization, but it's the most we can
		// do at the language boundary without unsafe.
		if appAuth != nil {
			appAuth.Password = ""
		}
	}()

	// Wave 0 / year-one stateless-only: refuse well-known stateful
	// base images before we burn the PullDigest round-trip. The check
	// runs first because PullDigest is the expensive part of the
	// build (registry dial + manifest GET); a stateful base is going
	// to fail anyway, just later.
	if hint, denied := StatefulDenyListMatch(ref); denied {
		err := fmt.Errorf("%w: %s — %s", errStatefulViolation, ref, hint)
		_ = h.markDeployFailed(ctx, dep.ID, err, "stateful base image denied")
		return err
	}

	// Issue #472 / ADR-054: per-app cosign signature verification.
	// Runs AFTER the stateful-deny check (a known-bad base is
	// rejected faster without a network round-trip) and BEFORE
	// PullDigest (no point pulling an unsigned / untrusted image).
	// The flag lives on the apps row (NOT the deployment row) so a
	// single PATCH takes effect on the next deploy — apid's
	// createDeployment gate already handles the "no signers" fail-
	// closed case before imaged sees the deployment. Defence-in-
	// depth: if imaged is called with require_signed=true but the
	// on-disk trust dir is empty, we re-verify here.
	if app.RequireSigned {
		if err := h.verifyImageSignature(ctx, app, dep, ref); err != nil {
			return err
		}
	}

	digest, err := pullDigestWithAuth(ctx, h.oci, ref, appAuth)
	if err != nil {
		_ = h.markDeployFailed(ctx, dep.ID, err, "oci pull failed")
		return fmt.Errorf("imaged: oci pull: %w", err)
	}
	// Issue #461 / ADR-062: best-effort mark credential used on
	// successful authenticated pull. Best-effort so a transient
	// mark-used failure cannot abort an otherwise-successful
	// build; non-fatal warn log inside markRegistryCredentialUsed.
	h.markRegistryCredentialUsed(ctx, app, refHost, appAuth)

	// ADR-117: Building→Imaging closes the dependency_restore stage
	// and opens the security_scan stage. The runDeployScan call
	// below (handler.go:1353) closes security_scan and opens
	// image_build in turn. Caller at handleDeploySourceChanged
	// (handler.go:1305) and handleSnapshotBoot (handler.go:2305)
	// have already advanced to `dependency_restore`.
	if err := h.transitionWithStage(ctx, dep.ID, state.StageDependencyRestore, state.StageSecurityScan, state.DeployImaging, ""); err != nil {
		return err
	}

	imageCfg, err := pullImageConfigWithAuth(ctx, h.oci, ref, appAuth)
	if err != nil {
		_ = h.markDeployFailed(ctx, dep.ID, err, "oci pull config")
		return fmt.Errorf("imaged: pull image config: %w", err)
	}

	manifest, err := manifestFromImageConfig(imageCfg)
	if err != nil {
		// Image declares neither Entrypoint nor Cmd — oci.ManifestFromConfig
		// already wrapped it with ErrImageManifestInvalid; mark the deploy
		// failed so the row reflects the rejection and the customer sees
		// the canonical error code at the API surface (ADR-136 §Decision 5).
		_ = h.markDeployFailed(ctx, dep.ID, err, "image declares no entrypoint/cmd")
		return err
	}
	if dep.Handler != "" {
		manifest.Entrypoint = []string{dep.Handler}
	}
	// PR-B (issue #460 / ADR-053): layer the deployment's six persisted
	// override columns onto the OCI-derived manifest before validation. The
	// helper is a pure function; an error here means a jsonb column failed
	// to decode (i.e. the row was tampered with or a migration replayed an
	// old shape). Mirror the manifest.Validate() error path: deploy failed
	// + wrap.
	manifest, err = applyOverrides(manifest, dep)
	if err != nil {
		_ = h.markDeployFailed(ctx, dep.ID, err, "manifest overrides: decode failed")
		return fmt.Errorf("imaged: apply overrides: %w", err)
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
	be, err := h.storageFor()
	if err != nil {
		_ = h.markDeployFailed(ctx, dep.ID, err, "imaged: storageFor")
		return fmt.Errorf("imaged: storageFor: %w", err)
	}
	if mp, ok := h.oci.(oci.ManifestPuller); ok {
		// Issue #461 / ADR-062: thread the customer's per-app
		// registry credential through the M6 two-drive path.
		// aboveBaseLayers dispatches app manifest + app blobs
		// with `appAuth`, and base manifest + base blobs with
		// nil (the base is always public
		// ghcr.io/onebox-faas/...). Production RegistryClient
		// satisfies AuthManifestPuller so both paths carry the
		// correct auth shape.
		above, diffs, err := h.aboveBaseLayers(ctx, mp, dep.ImageDigest, app.Runtime, manifest, appAuth)
		if err != nil {
			// ADR-141 §Decision 3 + §Decision 2: when the app image is
			// not built FROM our runner-* base, dispatch on the
			// tri-state FullRootfsAllowAuto / FullRootfsOverride
			// fields (commit 6 widens state.Deployment). The
			// typed sentinel is the ONLY signal the dispatch
			// consults — never a string match — so future wrapping
			// (telemetry, retries) cannot bypass the gate.
			//
			// Commit 4 lands the dispatch skeleton with a stub that
			// returns ErrLayersNotAboveBase until commit 6 wires
			// buildFullRootfsLayer (BuildFullRootfs itself ships
			// in commit 5). Forcing today-equivalent failure
			// surfaces the canonical sentinel so customers can
			// opt in via `--full-rootfs` once commit 6 lands.
			if errors.Is(err, oci.ErrLayersNotAboveBase) {
				fullErr := h.dispatchFullRootfs(ctx, app, dep, acct, manifest, appAuth)
				if fullErr != nil {
					_ = h.markDeployFailed(ctx, dep.ID, fullErr, "imaged: full-rootfs dispatch")
					return fullErr
				}
				return nil
			}
			// aboveBaseLayers can surface any of the three puller-side
			// sentinels (image-not-found on app manifest 404, manifest-list
			// rejection on multi-arch images, egress-denial on a private
			// registry) so it goes through markDeployFailed too. Non-pull
			// failures (e.g. base mismatch) get code "" — the message
			// preserves the upstream string.
			_ = h.markDeployFailed(ctx, dep.ID, err, "imaged: above-base")
			return err
		}
		// Issue #461 / ADR-062: mark credential used after a successful
		// above-base resolution — every authenticated pull above this
		// line was either app manifest, app config, or app blob.
		h.markRegistryCredentialUsed(ctx, app, refHost, appAuth)
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
			Storage:       be,
			StorageKey:    appsKey,
			// Issue #299 / ADR-038 Phase 3: SBOM emission runs
			// inside Builder.Build on the staging dir (the only
			// artefact that contains the customer's source tree at
			// that point). SBOMKey is stamped onto the
			// build_provenance row after a successful build — see
			// updateBuildProvenanceSBOM below.
			SBOMRun:        h.syftRun,
			SBOMStorageKey: h.sbomStorageKeyForDeployment(ctx, dep.ID),
		})
		if err != nil {
			_ = h.transition(ctx, dep.ID, state.DeployFailed, "build app layer: "+err.Error())
			return fmt.Errorf("imaged: build app layer: %w", err)
		}
		// Stamp the SBOM storage key onto build_provenance.sbom_storage_key
		// (issue #299 / ADR-038 Phase 3). Best-effort: an error here
		// is logged at WARN and the build still succeeds (the SBOM
		// is observational metadata, schema §4.2).
		h.updateBuildProvenanceSBOM(ctx, dep.ID, result.SBOMKey)
		if err := h.store.SetDeploymentRootfs(ctx, dep.ID, h.appsRootPath(app.Slug, dep.ID), appsKey, result.ContentBytes); err != nil {
			_ = h.transition(ctx, dep.ID, state.DeployFailed, "stamp rootfs: "+err.Error())
			return fmt.Errorf("imaged: stamp rootfs: %w", err)
		}
		if err := h.replicateLayer(ctx, appsKey); err != nil {
			_ = h.transition(ctx, dep.ID, state.DeployFailed, err.Error())
			return err
		}
		h.log.Info("imaged: build app layer (two-drive)",
			"app", app.Slug, "digest", digest, "key", result.ImageKey,
			"bytes", result.ContentBytes, "above_diff_ids", len(diffs))
	} else {
		// M5 fallback: stream all layers as-is. Used by fakes that only
		// implement oci.Puller — the existing unit tests exercise this
		// branch. Observed under op="blob" so the §12 dashboard sees
		// one series per legacy fallback's stream (it's pull-then-
		// decode-and-stitch in the legacy path; for the dashboard it's
		// just a slower blob pull).
		start := time.Now()
		pulled, err := pullLayersWithAuth(ctx, h.oci, ref, appAuth)
		h.ops.ObserveImagedOCIPull("blob", pullResult(err), time.Since(start))
		if err != nil {
			_ = h.markDeployFailed(ctx, dep.ID, err, "oci pull layers")
			return fmt.Errorf("imaged: pull layers: %w", err)
		}
		// Issue #461 / ADR-062: mark credential used after a
		// successful M5 fallback layer pull.
		h.markRegistryCredentialUsed(ctx, app, refHost, appAuth)
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
			Storage:       be,
			StorageKey:    appsKey,
			// Issue #299 / ADR-038 Phase 3: see the two-drive
			// branch above for the SBOM-populator contract.
			SBOMRun:        h.syftRun,
			SBOMStorageKey: h.sbomStorageKeyForDeployment(ctx, dep.ID),
		})
		if err != nil {
			_ = h.transition(ctx, dep.ID, state.DeployFailed, "build app layer: "+err.Error())
			return fmt.Errorf("imaged: build app layer: %w", err)
		}
		h.updateBuildProvenanceSBOM(ctx, dep.ID, result.SBOMKey)
		if err := h.store.SetDeploymentRootfs(ctx, dep.ID, h.appsRootPath(app.Slug, dep.ID), appsKey, result.ContentBytes); err != nil {
			_ = h.transition(ctx, dep.ID, state.DeployFailed, "stamp rootfs: "+err.Error())
			return fmt.Errorf("imaged: stamp rootfs: %w", err)
		}
		if err := h.replicateLayer(ctx, appsKey); err != nil {
			_ = h.transition(ctx, dep.ID, state.DeployFailed, err.Error())
			return err
		}
		h.log.Info("imaged: build app layer (m5 fallback)", "app", app.Slug, "digest", digest, "key", result.ImageKey, "bytes", result.ContentBytes)
	}
	// Issue #463 / ADR-069 / PR-B: after the main app's drive1
	// is built and stamped, build + stamp one ext4 per sidecar
	// the deployment carries. Per-sidecar ext4 lives at
	// apps/<slug>/<depID>-<sidecarName>.ext4 (sibling of the
	// main layer key); the per-workload row in
	// deployment_sidecar_layers (migration 00119) is the
	// vmmd-readable handle. Sidecar builds are best-effort in
	// the sense that ONE sidecar failing fails the whole
	// deploy — a partial sidecar set is worse than a clean
	// failure because vmmd expects every name in the jsonb
	// contract surface to have a row.
	scFindings, err := h.buildSidecarLayers(ctx, app, dep, acct)
	if err != nil {
		return err
	}
	// Per-deploy secret-can-on-image scan (PR-A, imaged-layer
	// secret scan). Runs AFTER buildSidecarLayers has stamped
	// SetDeploymentSidecarLayer for every sidecar (i.e. every
	// per-app ext4 is on disk) and BEFORE the grype CVE scan
	// + the pending→snapshotting transition. Loud-fail
	// posture: a single finding fails the deploy with
	// errImageSecretDetected + stamps the audit row via
	// state.Store.UpsertDeploymentSecretFindings ONCE at the
	// end with all main+sidecar findings accumulated.
	// Function deploys are out of scope — buildFunctionLayer
	// is the only path that doesn't run this scan.
	mainFindings, walkErr := h.runDeployLayerSecretScan(ctx, app, dep, "app")
	if walkErr != nil {
		h.log.Warn("imaged: layer secret scan walk failed (main, non-fatal)",
			"deployment", dep.ID, "app", app.Slug, "err", walkErr)
	}
	allFindings := append(mainFindings, scFindings...)
	if len(allFindings) == 0 {
		return nil
	}
	scannedAt := time.Now().UTC()
	upsertDeploymentSecretFindings(ctx, h.store, dep.ID,
		allFindings, layerSecretScanStatusCompleteWithRedactions,
		dep.ImageDigest, scannedAt, h.log)
	if markErr := h.markDeployFailed(ctx, dep.ID, errImageSecretDetected, "image-layer secret detected"); markErr != nil {
		h.log.Warn("imaged: mark deploy failed on layer secret",
			"deployment", dep.ID, "app", app.Slug, "err", markErr)
	}
	return errImageSecretDetected
}

// buildSidecarLayers handles the per-sidecar image build for
// issue #463 / ADR-069 / PR-B. For each sidecar in the
// deployment's sidecars jsonb:
//
//  1. Decode the api.Sidecar typed shape (validation already
//     happened at the apid handler boundary — pkg/api ↔ pkg/state
//     cycle avoidance per pkg-api-cannot-import-pkg-state memory).
//  2. Pull the sidecar's OCI ref (same puller path as the main
//     image, per-sidecar Auth credential).
//  3. Compute diff_ids above the same base the main image uses
//     (the per-app base digest, captured by imaged during base
//     staging — pkg/imaged/base_stage.go).
//  4. Build the sidecar ext4 via rootfs.Builder, same Builder
//     call as the main path. ADR-040's verbatim-Linkname +
//     clamp-on-traversal invariant lives in rootfs.ApplyLayerGz
//     so the sidecar layers inherit it for free.
//  5. Upsert the per-workload row via
//     SetDeploymentSidecarLayer.
//
// Stateless denylist re-check: pkg/statefuldenylist.Match
// re-validates each sidecar image before pull — the API gate
// ran at apid time, but a malicious or buggy CI script that
// edits the row directly must still trip the deny list at the
// store boundary.
//
// Returns nil when the deployment carries zero sidecars (the
// common case today); the api.Sidecar parse and the per-sidecar
// pullers only run when there's actual work to do.
func (h *Handler) buildSidecarLayers(ctx context.Context, app state.App, dep state.Deployment, acct state.Account) ([]secretscan.Finding, error) {
	findings := []secretscan.Finding{}
	if len(dep.Sidecars) == 0 || string(dep.Sidecars) == "null" {
		return findings, nil
	}
	var sidecars api.Sidecars
	if err := json.Unmarshal(dep.Sidecars, &sidecars); err != nil {
		_ = h.transition(ctx, dep.ID, state.DeployFailed, "decode sidecars: "+err.Error())
		return findings, fmt.Errorf("imaged: decode sidecars: %w", err)
	}
	if len(sidecars) == 0 {
		return findings, nil
	}
	for _, sc := range sidecars {
		if sc.Name == "" {
			_ = h.transition(ctx, dep.ID, state.DeployFailed, "sidecar: missing name")
			return findings, fmt.Errorf("imaged: sidecar missing name")
		}
		// Re-validate the deny list at the storage boundary.
		// The API gate already rejected stateful image refs;
		// this catches any post-hoc mutation.
		if hint, denied := StatefulDenyListMatch(sc.Image); denied {
			msg := fmt.Sprintf("sidecar %q image refused: stateful pattern; %s", sc.Name, hint)
			_ = h.transition(ctx, dep.ID, state.DeployFailed, msg)
			return findings, fmt.Errorf("imaged: %s", msg)
		}
		layerKey := sched.AppSidecarLayerKey(app.Slug, dep.ID, sc.Name)
		// Per-sidecar pull path. We use the same h.oci
		// ManifestPuller as the main image; per-sidecar
		// auth may differ (a sidecar image can sit on a
		// separate private registry than the main image),
		// but for PR-B the simplest correct shape is one
		// auth per ref — resolve via the same path as the
		// main image, keyed on the sidecar ref's host.
		var auth *oci.BasicAuth
		if parsedRef, parseErr := oci.ParseReference(sc.Image); parseErr == nil {
			auth, _ = h.resolveRegistryAuth(ctx, app, parsedRef.APIHost())
		}
		// Sidecar pull: use the M5 stream path (pullLayersWithAuth)
		// rather than the two-drive base-diff path. Sidecars are
		// treated as opaque layer streams — the customer uploads
		// the full image and the build ext4s everything above the
		// rootfs's empty lower dir. This mirrors the M5 fallback
		// in buildImageLayer and keeps the sidecar semantics
		// independent of the main image's runtime selection.
		start := time.Now()
		pulled, err := pullLayersWithAuth(ctx, h.oci, sc.Image, auth)
		h.ops.ObserveImagedOCIPull("sidecar_blob", pullResult(err), time.Since(start))
		if err != nil {
			_ = h.transition(ctx, dep.ID, state.DeployFailed, fmt.Sprintf("sidecar %q pull: %s", sc.Name, err.Error()))
			return findings, fmt.Errorf("imaged: sidecar %q pull: %w", sc.Name, err)
		}
		defer func() {
			for _, r := range pulled.Layers {
				_ = r.Close()
			}
		}()
		be, err := h.storageFor()
		if err != nil {
			_ = h.transition(ctx, dep.ID, state.DeployFailed, "sidecar storage init: "+err.Error())
			return findings, fmt.Errorf("imaged: sidecar storage init: %w", err)
		}
		// rootfs.Builder.Build input. The sidecar's
		// `ram_mb` is the memory.max the sidecar cgroup gets
		// carved (PR-B step 6 wires that on the host side via
		// writeWorkloadCgroup). The Manifest's entrypoint is a
		// placeholder — guest-init reads the per-workload
		// workload.json (issue #463 / ADR-069 §Sidecar staging)
		// for the sidecar's argv/env/port at boot time, not
		// the rootfs-baked app.json. The placeholder exists
		// because pkg/api.AppManifest.Validate rejects an
		// empty entrypoint; using a constant argv keeps the
		// build happy without baking a customer-visible Cmd
		// into the ext4.
		result, err := h.builder.Build(ctx, rootfs.BuildInput{
			Layers:        layersAsReaders(pulled.Layers),
			Manifest:      api.SidecarBuildManifest(),
			GuestInitPath: h.guestInitPath,
			Plan:          acct.Plan,
			Storage:       be,
			StorageKey:    layerKey,
		})
		if err != nil {
			_ = h.transition(ctx, dep.ID, state.DeployFailed, fmt.Sprintf("sidecar %q build: %s", sc.Name, err.Error()))
			return findings, fmt.Errorf("imaged: sidecar %q build: %w", sc.Name, err)
		}
		if _, err := h.store.SetDeploymentSidecarLayer(ctx, state.DeploymentSidecarLayer{
			DeploymentID:  dep.ID,
			SidecarName:   sc.Name,
			StorageKey:    layerKey,
			Bytes:         result.ContentBytes,
			ContentDigest: sc.Image,
		}); err != nil {
			_ = h.transition(ctx, dep.ID, state.DeployFailed, fmt.Sprintf("sidecar %q stamp: %s", sc.Name, err.Error()))
			return findings, fmt.Errorf("imaged: sidecar %q stamp: %w", sc.Name, err)
		}
		if err := h.replicateLayer(ctx, layerKey); err != nil {
			_ = h.transition(ctx, dep.ID, state.DeployFailed, err.Error())
			return findings, err
		}
		// Per-sidecar secret-can-on-image scan (PR-A). Mirrors the
		// main-image scan above — same engine, same loud-fail
		// posture. The "layer" label is "sidecar-<slug>" so the
		// dashboard can attribute findings to the right sidecar.
		// We ACCUMULATE findings here and let handleDeployment
		// stamp the audit row ONCE at the end — otherwise each
		// sidecar's clean walk would overwrite the prior sidecar's
		// findings (UpsertDeploymentSecretFindings is an
		// overwrite-not-append). The first failure for any sidecar
		// short-circuits the rest of the sidecar loop (a partial
		// sidecar set is worse than a clean failure — see the
		// comment block above).
		if scFindings, _ := h.runDeployLayerSecretScan(ctx, app, dep, "sidecar-"+sc.Name); len(scFindings) > 0 {
			findings = append(findings, scFindings...)
			return findings, errImageSecretDetected
		}
		h.log.Info("imaged: build sidecar layer",
			"app", app.Slug, "sidecar", sc.Name, "kind", sc.Type,
			"key", layerKey, "bytes", result.ContentBytes,
			"layers", len(pulled.Layers))
	}
	return findings, nil
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
	// ADR-117: Building→Imaging closes dependency_restore, opens
	// security_scan. The runDeployScan call at handler.go:1353
	// (after this function returns) closes security_scan and
	// opens image_build. Same seam as buildImageLayer at
	// handler.go:1551.
	if err := h.transitionWithStage(ctx, dep.ID, state.StageDependencyRestore, state.StageSecurityScan, state.DeployImaging, ""); err != nil {
		return err
	}
	runtime := app.Runtime
	if runtime == "" {
		// Fall back to the per-deploy handler field when the app row
		// doesn't carry the runtime — keeps older clients working.
		runtime = dep.Handler
	}
	if runtime != RuntimeNode22 && runtime != RuntimePython312 && runtime != RuntimeGo124 && runtime != RuntimeGo124Alpine && runtime != RuntimeNode24 && runtime != RuntimePython313 {
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
	// Per-runtime handler path. The baseline is `/app/node22.js` —
	// matches the node22 runner's `--handler` default at
	// guest/runners/node22/main.go so the round-trip works without an
	// override. python312 and go124 override because their handlers
	// ship under different filenames: `/app/handler.py` for python
	// (the .py suffix matters), `/app/handler` for the Go static
	// binary (Railpack's --plan go emits CGO_ENABLED=0). The go124
	// branch is also a tripwire: an unknown runtime that ever slips
	// past the allow-list above would silently produce
	// `/app/node22.js` and the runner would exec the wrong file on
	// first wake. Pin: TestBuildFunctionLayer_Runtimes in
	// handler_test.go asserts every runtime's argv verbatim.
	manifest := api.AppManifest{
		Port:    api.DefaultAppPort,
		Healthz: defaultHealthzPath,
		Entrypoint: []string{
			"/usr/local/bin/faas-runner",
			"--runtime", runtime,
			"--handler", "/app/node22.js",
		},
	}
	// node22 has no explicit override here — its `/app/node22.js`
	// matches the default above. Adding `case RuntimeNode22 { ... }`
	// for symmetry would silently diverge from the runner default.
	if runtime == RuntimePython312 {
		manifest.Entrypoint = []string{
			"/usr/local/bin/faas-runner",
			"--runtime", runtime,
			"--handler", "/app/handler.py",
		}
	}
	if runtime == RuntimeNode24 {
		// Versioned node handler path. The runner's `--handler` default
		// (guest/runners/node24/main.go:54) is `/app/node24.js`, pinned
		// by TestNode24RunnerHandlerDefault; the manifest mirrors it
		// verbatim so the round-trip works without an override.
		manifest.Entrypoint = []string{
			"/usr/local/bin/faas-runner",
			"--runtime", runtime,
			"--handler", "/app/node24.js",
		}
	}
	if runtime == RuntimePython313 {
		// Version-neutral Python handler. Like python312, the handler
		// filename carries no version — the version is bound by the
		// OCI base (images/runner-python313.Dockerfile). Pinned by
		// TestPython313RunnerHandlerDefault.
		manifest.Entrypoint = []string{
			"/usr/local/bin/faas-runner",
			"--runtime", runtime,
			"--handler", "/app/handler.py",
		}
	}
	if runtime == RuntimeGo124 {
		// The customer's handler is a static Go binary (Railpack's go
		// plan emits CGO_ENABLED=0 by default), so the runner execs
		// the file directly with no interpreter argument. The path
		// `/app/handler` is independent of the node22 baseline above
		// and pinned by
		// guest/runners/go124/main_test.go::TestGoRunnerHandlerDefault
		// so a default-flag drift surfaces at unit-test time, not
		// on first wake.
		manifest.Entrypoint = []string{
			"/usr/local/bin/faas-runner",
			"--runtime", runtime,
			"--handler", "/app/handler",
		}
	}
	if runtime == RuntimeGo124Alpine {
		// Same argv as go124 (bookworm): the runner shim is identical
		// (guest/runners/go124/main.go), only the base image's libc
		// differs (musl vs glibc). CGO_ENABLED=0 (Railpack's default)
		// produces a fully-static binary that runs on both bases.
		// Customers with cgo bindings must rebuild against
		// `FROM golang:1.24-alpine AS build` in their Dockerfile —
		// the libc mismatch surfaces as `exec format error` on first
		// wake (see docs/runtimes/go124.md failure-mode table).
		manifest.Entrypoint = []string{
			"/usr/local/bin/faas-runner",
			"--runtime", runtime,
			"--handler", "/app/handler",
		}
	}
	// PR-B (issue #460 / ADR-053): same seam as buildImageLayer (handler.go:546).
	// Function deploys build their manifest inline (no OCI pull), so the
	// "OCI base" is the runtime-default argv from the switch above;
	// overrides layer on top. snapshot-boot fans out through here, so this
	// one insertion covers both routes.
	manifest, err := applyOverrides(manifest, dep)
	if err != nil {
		_ = h.markDeployFailed(ctx, dep.ID, err, "manifest overrides: decode failed")
		return fmt.Errorf("imaged: apply overrides: %w", err)
	}
	if err := manifest.Validate(); err != nil {
		_ = h.transition(ctx, dep.ID, state.DeployFailed, "manifest invalid: "+err.Error())
		return fmt.Errorf("imaged: validate manifest: %w", err)
	}
	// Source builds arrive with a builderd-produced local OCI archive in
	// dep.RootfsPath. Keep the original source tarball for the customer
	// handler and overlay the built OCI layers first so dependencies and the
	// compiled Go /app/server artifact are present during function assembly.
	// Direct image deployments have no source-build OCI handoff and retain the
	// legacy raw-tarball path.
	var builtLayers []io.Reader
	var cleanupBuiltLayers func()
	if (runtime == RuntimeGo124 || runtime == RuntimeGo124Alpine) &&
		dep.RootfsPath != "" && dep.RootfsPath != dep.SourcePath && dep.Kind != state.DeploymentKindImage {
		_, layers, cleanup, loadErr := loadLocalOCIArchive(dep.RootfsPath)
		if loadErr != nil {
			_ = h.transition(ctx, dep.ID, state.DeployFailed, "load source build artifact: "+loadErr.Error())
			return fmt.Errorf("imaged: load source build artifact: %w", loadErr)
		}
		builtLayers = layersAsReaders(layers)
		cleanupBuiltLayers = cleanup
		defer cleanupBuiltLayers()
	}

	appsKey := sched.AppLayerKey(app.Slug, dep.ID)
	be, err := h.storageFor()
	if err != nil {
		_ = h.transition(ctx, dep.ID, state.DeployFailed, "storageFor: "+err.Error())
		return fmt.Errorf("imaged: storageFor: %w", err)
	}
	result, err := h.builder.Build(ctx, rootfs.BuildInput{
		Layers:        builtLayers,
		Manifest:      manifest,
		GuestInitPath: h.guestInitPath,
		Plan:          acct.Plan,
		Storage:       be,
		StorageKey:    appsKey,
		// TarballPath lets the rootfs.Builder stream the customer's
		// source tarball into /app during layer assembly. For source builds,
		// builtLayers above are applied first so Go's compiled /app/server
		// can be normalized to /app/handler.
		TarballPath: dep.SourcePath,
		// The customer-facing Node convention is handler.js while the
		// versioned runner executes /app/node22.js or /app/node24.js.
		// rootfs creates that runtime alias during assembly.
		FunctionHandlerPath: manifest.Entrypoint[len(manifest.Entrypoint)-1],
		// FunctionRunnerPath is the static guest/runners/<rt>/faas-runner
		// binary that lives at /usr/local/bin/faas-runner in the layer.
		FunctionRunnerPath: runnerPath,
		// Issue #299 / ADR-038 Phase 3: SBOM emission runs inside
		// Builder.Build on the staging dir (which holds the customer's
		// source tarball + the runner binary + guest-init — exactly
		// what the SBOM should enumerate). SBOMKey is stamped onto
		// the build_provenance row immediately below.
		SBOMRun:        h.syftRun,
		SBOMStorageKey: h.sbomStorageKeyForDeployment(ctx, dep.ID),
	})
	if err != nil {
		_ = h.transition(ctx, dep.ID, state.DeployFailed, "build function layer: "+err.Error())
		return fmt.Errorf("imaged: build function layer: %w", err)
	}
	h.updateBuildProvenanceSBOM(ctx, dep.ID, result.SBOMKey)
	if err := h.store.SetDeploymentRootfs(ctx, dep.ID, h.appsRootPath(app.Slug, dep.ID), appsKey, result.ContentBytes); err != nil {
		_ = h.transition(ctx, dep.ID, state.DeployFailed, "stamp rootfs: "+err.Error())
		return fmt.Errorf("imaged: stamp rootfs: %w", err)
	}
	if err := h.replicateLayer(ctx, appsKey); err != nil {
		_ = h.transition(ctx, dep.ID, state.DeployFailed, err.Error())
		return err
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
	case RuntimeGo124:
		return h.functionRunnerGo124Path
	case RuntimeGo124Alpine:
		return h.functionRunnerGo124AlpinePath
	case RuntimeNode24:
		return h.functionRunnerNode24Path
	case RuntimePython313:
		return h.functionRunnerPython313Path
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
	case RuntimeGo124:
		return "GO124"
	case RuntimeGo124Alpine:
		return "GO124_ALPINE"
	case RuntimeNode24:
		return "NODE24"
	case RuntimePython313:
		return "PYTHON313"
	}
	return runtime
}

// manifestFromImageConfig maps an OCI ImageConfig to an api.AppManifest.
// Per ADR-136 §Decision 1-4, the conversion honours the full OCI image-
// config shape: Entrypoint+Cmd combined per OCI semantics, User preserved
// (numeric-only today; named-user resolution lands in M-3), Healthcheck /
// StopSignal / StopGracePeriod surfaced onto the manifest. The function
// delegates the shape derivation to oci.ManifestFromConfig so the registry
// path (handler) and the local-OCI build path (local_oci.go) share the
// exact same projection + the same ErrImageManifestInvalid failure mode.
//
// ADR-051 Phase 4 (characterization boot): the App path must default
// Port + Healthz and inject PORT=8080 into Env so the in-guest probe
// (guest/init/{characterize,portnorm}_linux.go) sees a known listening
// port and the app listens on :8080. Without these defaults the port
// normalization ladder in portnorm_linux.go must fall through to the
// userspace forwarder on every first wake, which the architecture
// avoids (ADR-051 §"Consequences"). Customer-pinned values in
// cfg.Port / cfg.Env["PORT"] survive this seeding (last-write-wins
// is the customer's call).
func manifestFromImageConfig(cfg oci.ImageConfig) (api.AppManifest, error) {
	manifest, err := oci.ManifestFromConfig(oci.Config{
		Env:              cloneEnvMap(cfg.Env),
		Entrypoint:       append([]string(nil), cfg.Entrypoint...),
		Cmd:              append([]string(nil), cfg.Cmd...),
		WorkingDir:       cfg.WorkingDir,
		User:             cfg.User,
		Healthcheck:      cfg.Healthcheck,
		StopSignal:       cfg.StopSignal,
		StopGracePeriodS: cfg.StopGracePeriodS,
	})
	if err != nil {
		return api.AppManifest{}, err
	}
	// Containerised-defaults overlay (ADR-051 Phase 4) — applied
	// AFTER oci.ManifestFromConfig so the default seed wins on the
	// fields the customer didn't pin (Healthz, Env["PORT"]) and
	// doesn't overwrite Customer-supplied OCI values (env flattening
	// has already turned `Env` into a map by this point, so PORT
	// can be checked with `_, set := manifest.Env["PORT"]`).
	applyContainerDefaults(&manifest)
	return manifest, nil
}

// applyContainerDefaults seeds the platform-default Healthz path
// (ADR-051 §"Consequences") and the PORT=8080 env var when the
// customer didn't pin them. Lives here so both the registry pull
// path (manifestFromImageConfig) and the local OCI build path
// (buildLocalOCIAppLayer in local_oci.go) share the exact same
// seeding rule — the F8 fixup consolidates what was duplicated.
func applyContainerDefaults(m *api.AppManifest) {
	if m.Healthz == "" {
		m.Healthz = defaultHealthzPath
	}
	if m.Env == nil {
		m.Env = make(map[string]string, 1)
	}
	if _, set := m.Env["PORT"]; !set {
		m.Env["PORT"] = "8080"
	}
}

// cloneEnvMap returns a defensive copy of the env map. The caller
// (manifestFromImageConfig, handleDeployment) may apply per-deploy
// overrides without mutating the shared ImageConfig the puller
// returned. Renamed from cloneEnv at the F8 fixup — "Env" was too
// generic once oci.Config.Env became a map (no slice to clone from).
func cloneEnvMap(m map[string]string) map[string]string {
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
		FCVersion:    p.FCVersion,  // pins restore compatibility (ADR-005)
		StorageKey:   p.StorageKey, // see snapshotWrittenPayload.StorageKey
		MemBytes:     p.MemBytes,
		DiskBytes:    p.VMStateBytes,
		// Tier (issue #470 / PR #470-FU-B). Empty payload falls
		// back to "init" (the DB column default and the legacy
		// pre-#470 behaviour); warm-tier rows are only ever
		// written by schedd's captureWarmSnapshot path
		// (PR #470-FU-A), which is the only flow that knows the
		// framework-ready signal has actually fired.
		Tier: p.Tier,
	}
	stored, err := h.store.CreateSnapshot(ctx, snap)
	if err != nil {
		if !errors.Is(err, state.ErrConflict) {
			return fmt.Errorf("imaged: create snapshot: %w", err)
		}
		stored, err = h.store.LatestSnapshotForTier(ctx, p.DeploymentID, snap.Tier)
		if err != nil {
			return fmt.Errorf("imaged: load existing snapshot: %w", err)
		}
	}
	if p.NodeID != "" {
		if origins, ok := h.store.(state.SnapshotOriginStore); ok {
			if originErr := origins.RecordSnapshotOrigin(ctx, stored.ID, p.NodeID); originErr != nil {
				// Origin metadata improves locality but is not the blob's
				// source of truth. Keep the deployment live if an older
				// compute_nodes row or a transient DB issue blocks it.
				h.log.Warn("imaged: record snapshot origin failed", "snapshot_id", stored.ID, "node_id", p.NodeID, "err", originErr)
			}
		}
	}

	if err := h.store.MarkDeploymentLive(ctx, dep.ID); err != nil {
		return fmt.Errorf("imaged: mark live: %w", err)
	}
	// ADR-117: close the readiness stage. snapshot_prepare closed
	// at handler.go:1355 / 2334; readiness opened when vmmd stamped
	// the framework-ready probe success on this row (instance
	// framework_ready_at). The customer sees the readiness row
	// in the summary block — the `✓ Deployed.` line is owned by
	// streamDeployLogs and is NOT a stage row.
	if _, serr := h.store.AppendDeploymentStage(ctx, dep.ID,
		state.StageSnapshotPrepare, state.StageReadiness, time.Now(), ""); serr != nil {
		h.log.Warn("mark live: stage append failed",
			"deployment_id", dep.ID, "from", "snapshot_prepare", "to", "readiness", "err", serr)
	}
	// PR-A review fix: now close the readiness stage so the
	// customer's ticker carries a duration_ms on the wire rather
	// than showing "Readiness passed" stuck on in_progress.
	if _, serr := h.store.CloseDeploymentStage(ctx, dep.ID, state.StageReadiness, time.Now()); serr != nil {
		h.log.Warn("mark live: stage close failed",
			"deployment_id", dep.ID, "stage", "readiness", "err", serr)
	}
	// Fan out so audit / dashboard SSE see the terminal transition.
	if err := h.notif.Notify(ctx, db.NotifyDeploymentChanged,
		`{"app_id":"`+dep.AppID+`","to":"`+dep.ID+`","status":"live"}`); err != nil {
		h.log.Warn("imaged: notify live", "err", err)
	}
	return nil
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
func (h *Handler) handleSnapshotBoot(ctx context.Context, p snapshotBootPayload) (err error) {
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
	// PR-A review fix (F6): precondition check on stage_state.
	// handleSnapshotBoot's caller (builderd.notifySnapshotBoot
	// path) sets `status = DeployBuilding` upstream and assumes
	// the active stage is dependency_restore. If a redelivered
	// snapshot_boot notification arrives at a row that hasn't
	// been advanced through handleDeploySourceChanged yet, the
	// stage_state.current is still the schema default
	// (source_download). The previous version called
	// transitionWithStage(dep_restore, security_scan) which
	// silently dropped the stage projection via the stale-read
	// guard at pgstore.go. Surface the precondition violation
	// as an error so the caller logs it loudly and the operator
	// sees the regression rather than a customer's ticker stuck
	// on "Source downloaded" forever.
	fromStage := state.StageDependencyRestore
	if len(dep.StageState) > 0 {
		var ss state.StageState
		if uerr := json.Unmarshal(dep.StageState, &ss); uerr == nil && ss.Current != "" {
			if ss.Current != state.StageDependencyRestore && ss.Current != state.StageSourceDownload {
				h.log.Warn("imaged: snapshot_boot precondition violated — stage_state.current is not dependency_restore or source_download",
					"deployment", p.DeploymentID, "current_stage", ss.Current, "status", dep.Status)
				return fmt.Errorf("imaged: snapshot_boot precondition violated (current=%q, want dependency_restore or source_download)", ss.Current)
			}
			fromStage = ss.Current
		}
	}
	if dep.RootfsPath == "" {
		// builderd hasn't stamped yet. Treat as a transient no-op rather
		// than failing the deployment — the subsequent NotifySnapshotBoot
		// redelivery will find a stamp. F-01.
		h.log.Warn("imaged: snapshot_boot skipped — rootfs_path empty; waiting on builderd",
			"deployment", p.DeploymentID)
		return nil
	}
	// Issue #195 B1.5: install the defer AFTER the empty-rootfs
	// early-return so the no-op skip path doesn't touch the row.
	// The defer covers the build-window + snapshot_prime notifier
	// windows — the build may succeed (builds.status='succeeded')
	// while the snapshot_prime notifier fails, and the deployment
	// row would otherwise be left in DeployBuilding indefinitely.
	defer h.markFailedOnUnhandledError(ctx, dep.ID, &err)
	app, err := h.store.AppByID(ctx, dep.AppID)
	if err != nil {
		return fmt.Errorf("imaged: load app: %w", err)
	}
	acct, err := h.store.AccountByID(ctx, app.AccountID)
	if err != nil {
		return fmt.Errorf("imaged: load account: %w", err)
	}
	// ADR-117: handleSnapshotBoot enters when the row is already in
	// DeployBuilding (the caller at builderd's notifySnapshotBoot
	// path set the status upstream). The active StageName is
	// dependency_restore (or source_download for direct builds).
	// PR-A review fix (F1): open security_scan here; the snapshot_boot
	// path also calls runDeployScan below (line ~1353) so
	// security_scan→image_build closes at the same place as the
	// source_changed path. Same from→to pair as sites 1551 + 1928 except
	// `to` is now security_scan instead of image_build.
	if err := h.transitionWithStage(ctx, dep.ID, fromStage, state.StageSecurityScan, state.DeployImaging, ""); err != nil {
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
		if app.Type == state.AppTypeFunction || app.Runtime != "" {
			if err := h.buildFunctionLayer(ctx, app, dep, acct); err != nil {
				return err
			}
		} else if err := h.buildImageLayer(ctx, app, dep, acct); err != nil {
			return err
		}
	case state.DeploymentKindTarball, state.DeploymentKindDockerfile,
		state.DeploymentKindGitHub, state.DeploymentKindPreview:
		// GitHub push/preview deployments arrive here after builderd has
		// produced the OCI source-build tarball. They use the same local
		// OCI conversion as tarball/dockerfile builds; function apps still
		// need their runtime runner layered into the result.
		if app.Type == state.AppTypeFunction || app.Runtime != "" {
			if err := h.buildFunctionLayer(ctx, app, dep, acct); err != nil {
				return err
			}
		} else if err := h.buildLocalOCIAppLayer(ctx, app, dep, acct); err != nil {
			return err
		}
	default:
		return fmt.Errorf("imaged: snapshot_boot: unknown deployment kind %q", dep.Kind)
	}
	if err := h.ensureDeploymentRuntimeBase(ctx, app); err != nil {
		return err
	}
	// ADR-117 PR-A review fix (F1): run the scan here too — the
	// snapshot_boot path doesn't go through handleDeploySourceChanged,
	// so without an explicit runDeployScan + security_scan→
	// image_build transition the customer's ticker would show
	// "Security scan" stuck on in_progress.
	h.runDeployScan(ctx, app, dep)
	if err := h.transitionWithStage(ctx, dep.ID, state.StageSecurityScan, state.StageImageBuild, state.DeployImaging, ""); err != nil {
		return err
	}
	// ADR-117: image_build closes; snapshot_prepare opens. Same
	// from→to pair as the handleDeploySourceChanged post-scan site
	// at handler.go:1355.
	if err := h.transitionWithStage(ctx, dep.ID, state.StageImageBuild, state.StageSnapshotPrepare, state.DeploySnapshotting, ""); err != nil {
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

// ensureDeploymentRuntimeBase makes the shared drive0 available before
// schedd is asked to cold-boot the deployment. Empty runtime means a plain
// OCI app, whose legacy minimal-base path does not use the runtime matrix.
func (h *Handler) ensureDeploymentRuntimeBase(ctx context.Context, app state.App) error {
	// Unit-test handlers without the production routed StorageBackend keep
	// the historical in-memory build seam; production cmd/imaged always
	// wires storage before accepting notifications.
	if app.Runtime == "" || !h.runtimeBaseStagingEnabled {
		return nil
	}
	if _, err := h.EnsureRuntimeBase(ctx, app.Runtime, runtime.GOARCH, os.Getenv); err != nil {
		return fmt.Errorf("imaged: ensure runtime base %s: %w", app.Runtime, err)
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

// transitionWithStage (ADR-117, migration 00302) is the single
// chokepoint for status changes that ALSO advance the customer-UX
// stage projection (deployments.stage_state). It maps the internal
// state-machine status (pending/building/imaging/snapshotting/live)
// onto the closed 6-stage StageName vocabulary and calls
// Store.AppendDeploymentStage after the bare transition.
//
// Mapping (the closed StageName set lives at pkg/state/types.go:89):
//
//	DeployPending       → StageSourceDownload
//	DeployBuilding      → StageDependencyRestore (cache hit) OR
//	                      StageImageBuild (cold cache) — see callers
//	DeployImaging       → StageSecurityScan (entry) or
//	                      StageImageBuild (after scan completes) —
//	                      see PR-A review fix, F1
//	DeploySnapshotting  → StageSnapshotPrepare
//	DeployLive          → StageReadiness
//	DeployFailed        → no stage advance; the caller drives
//	                      MarkDeploymentStageFailed directly so the
//	                      in-flight stage (not the previously-closed
//	                      one) is stamped with reason.
//	DeploySuperseded    → no-op (not a customer-visible stage event)
//
// The from→to pair is computed by the caller — this helper is the
// seam, the policy lives at the call site so the future PR that
// refines the cache-hit vs cold-cache boundary has a single place
// to thread it. The bare `transition(...)` is preserved for the
// failure paths that don't want a stage projection (e.g.
// markDeployFailed's first flip before the active stage is known).
func (h *Handler) transitionWithStage(ctx context.Context, depID string, from, to state.StageName, status state.DeploymentStatus, errMsg string) error {
	if err := h.transition(ctx, depID, status, errMsg); err != nil {
		return err
	}
	row, err := h.store.AppendDeploymentStage(ctx, depID, from, to, time.Now(), "")
	if err != nil {
		// Stage projection is best-effort: a failed append logs
		// but does NOT roll back the status flip. The SSE consumer
		// will simply miss one frame for this transition. The
		// stage_state column is a customer-UX projection; the
		// state machine on `status` is the source of truth.
		h.log.Warn("transitionWithStage: append stage failed (status flip preserved)",
			"deployment_id", depID, "from", from, "to", to, "err", err)
		return nil
	}
	// SLO histogram (ADR-117 §Production-ready follow-on). One
	// observation per closed stage: the duration is the just-
	// appended row's `ended_at - started_at` so the metric
	// agrees with the customer-facing stage timeline to the
	// millisecond. Status label tracks the deployment's terminal
	// state at write time — a "live" close observes status=completed;
	// the failure path is observed separately by the caller via
	// MarkDeploymentStageFailed (see markDeployFailed). Safe on a
	// nil h.ops (unit tests that don't wire the registry).
	if h.ops != nil && len(row.StageState) > 0 {
		var ss state.StageState
		if json.Unmarshal(row.StageState, &ss) == nil && len(ss.History) > 0 {
			last := ss.History[len(ss.History)-1]
			if last.StartedAt != nil && last.EndedAt != nil {
				h.ops.ObserveDeployStageDuration(string(from), "completed", last.EndedAt.Sub(*last.StartedAt))
			}
		}
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
	// ADR-117 §3 + PR-A review fix: stamp the active stage as
	// failed. MarkDeploymentStageFailed moves the active row into
	// history with status="failed" + reason rather than overwriting
	// history[len-1] (the previously-closed stage, not the one in
	// flight). Best-effort — the state-machine flip on `status`
	// is the source of truth; the stage projection is the
	// customer-UX surface.
	if row, serr := h.store.MarkDeploymentStageFailed(ctx, depID, time.Now(), prefix+": "+err.Error()); serr != nil {
		h.log.Warn("markDeployFailed: stamp failed stage", "deployment_id", depID, "err", serr)
	} else if h.ops != nil && len(row.StageState) > 0 {
		// SLO histogram (ADR-117 §Production-ready follow-on).
		// Observe the failed stage's wall-clock duration under
		// status=failed so the per-stage p99 panel surfaces
		// failure-stall tails distinctly from success tails.
		// Same row-derived duration contract as transitionWithStage.
		var ss state.StageState
		if json.Unmarshal(row.StageState, &ss) == nil && len(ss.History) > 0 {
			last := ss.History[len(ss.History)-1]
			if last.StartedAt != nil && last.EndedAt != nil {
				// Code-review finding #2: MarkDeploymentStageFailed
				// clears state.Current on the way out (the in-flight
				// stage rolls into history with status=failed), so
				// reading `ss.Current` here produces stage="". That
				// falls outside the pre-instantiated closed-6 label
				// set and corrupts the §12 per-stage SLO panel
				// (every failed observation lands on an off-panel
				// time series instead of the row's actual failing
				// stage). Use the history row's Name — that IS the
				// stage that failed, by construction.
				h.ops.ObserveDeployStageDuration(string(last.Name), "failed", last.EndedAt.Sub(*last.StartedAt))
			}
		}
	}
	return nil
}

// markFailedOnUnhandledError is the catch-all (issue #195 B1.5). It
// runs from a named-return defer in handleDeployment and
// handleSnapshotBoot. The defer catches error windows the inner
// markDeployFailed calls miss:
//
//   - transition(DeployBuilding) → buildImageLayer call site
//     (the inner call has its own mark, but a notif.Notify failure
//     after a successful build does not — without this defer the
//     deployment row would stay in DeployBuilding indefinitely).
//   - handleSnapshotBoot's snapshot_prime notifier failure.
//   - any unknown-kind dispatch in the function-type switch.
//
// The defer uses a fresh context with a 5 s timeout so the catch-all
// mark survives a cancelled parent ctx (deploy restart). It reloads
// the deployment row before marking — the in-memory `dep` struct
// captured at function entry may be stale by the time the defer
// fires. If the reload sees a terminal-good status (DeployFailed /
// DeployLive / DeploySuperseded) the defer is a no-op so a late error
// after a successful path can never clobber the success.
//
// underlying SQL keeps its tracing handles but the cancellation chain
// is broken — the lint rule sees the WithoutCancel but expects a
// function-shape continuation; documenting the intent here is the
// cleaner alternative to passing ctx through unused.
//
//nolint:contextcheck // ctx is detached via context.WithoutCancel so the
func (h *Handler) markFailedOnUnhandledError(ctx context.Context, depID string, errp *error) {
	if errp == nil || *errp == nil {
		return
	}
	// Detach from the caller's ctx so a cancelled parent (deploy
	// restart) doesn't preclude the safety mark, but inherit values
	// (logger etc.) so the underlying SQL keeps its tracing handles.
	detached := context.WithoutCancel(ctx)
	markCtx, cancel := context.WithTimeout(detached, 5*time.Second)
	defer cancel()
	current, err := h.store.DeploymentByID(markCtx, depID)
	if err != nil {
		// Best-effort; if we can't read the row, we can't safely
		// mark it. Log and skip.
		h.log.Warn("imaged: catch-all mark: reload failed",
			"deployment", depID, "err", err)
		return
	}
	switch current.Status {
	case state.DeployFailed, state.DeployLive, state.DeploySuperseded:
		// Inner path already handled it (or it's a success). The
		// catch-all must NEVER clobber a terminal-good row.
		return
	}
	upstreamErr := *errp
	h.log.Warn("imaged: catch-all mark: unhandled error after transition",
		"deployment", depID, "status", current.Status, "err", upstreamErr)
	if mErr := h.markDeployFailed(markCtx, depID, upstreamErr, "handleDeployment"); mErr != nil {
		h.log.Warn("imaged: catch-all mark: mark failed",
			"deployment", depID, "err", mErr)
	}
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
//
// appAuth (issue #461 / ADR-062) is the customer's per-app
// private-registry Basic Auth credential, transiently unsealed by
// buildImageLayer before calling this method. App manifest + app
// blob pulls carry it; base manifest + base blob pulls stay
// anonymous (the base is always public ghcr.io/onebox-faas/...).
// Production RegistryClient satisfies oci.AuthManifestPuller so
// both paths dispatch via PullManifestWithAuth / PullBlobWithAuth;
// offline DefaultPuller satisfies the interface too (auth
// ignored).
func (h *Handler) aboveBaseLayers(ctx context.Context, mp oci.ManifestPuller,
	appRef, runtime string, _ api.AppManifest, appAuth *oci.BasicAuth) (aboveBaseStream, []string, error) {
	appRepo := repoWithHost(appRef)
	if appRepo == "" {
		return aboveBaseStream{}, nil, fmt.Errorf("imaged: cannot derive repo from %q", appRef)
	}
	start := time.Now()
	appManifest, err := pullManifestWithAuth(ctx, mp, appRef, appAuth)
	h.ops.ObserveImagedOCIPull("manifest", pullResult(err), time.Since(start))
	if err != nil {
		return aboveBaseStream{}, nil, fmt.Errorf("manifest: %w", err)
	}
	appCfg, err := h.pullConfig(ctx, mp, appRepo, appManifest.Config.Digest, appAuth)
	if err != nil {
		return aboveBaseStream{}, nil, fmt.Errorf("app config: %w", err)
	}
	baseRef := h.deployBaseRefOverride
	if baseRef == "" {
		// Per-runtime env var (FAAS_DEPLOY_BASE_REF_<RUNTIME>) takes
		// precedence over the legacy single-string global
		// FAAS_DEPLOY_BASE_REF (wired at cmd/imaged/main.go:255).
		// Matches the posture of EnsureBases (startup auto-stage):
		// per-runtime is the canonical operator surface, the single
		// global is the test-harness / legacy knob. Unknown runtimes
		// fall through to baseRefFor's default (BaseRefMinimal for
		// the "" / customer-uploaded-image case). Same
		// digest-pin validation as the row-level override gate.
		resolved, err := resolveDeployBaseRef(runtime, os.Getenv)
		if err != nil {
			return aboveBaseStream{}, nil, err
		}
		baseRef = resolved
	}
	baseRepo := repoWithHost(baseRef)
	if baseRepo == "" {
		return aboveBaseStream{}, nil, fmt.Errorf("imaged: cannot derive repo from base %q", baseRef)
	}
	start = time.Now()
	// Base manifest stays anonymous — the base is always public
	// (ghcr.io/onebox-faas/...). Mismatched auth on a public
	// base pull would break the build path (the base has no
	// realm challenge, so the realm endpoint would 401).
	baseManifest, err := pullManifestWithAuth(ctx, mp, baseRef, nil)
	h.ops.ObserveImagedOCIPull("manifest", pullResult(err), time.Since(start))
	if err != nil {
		return aboveBaseStream{}, nil, fmt.Errorf("base manifest: %w", err)
	}
	baseCfg, err := h.pullConfig(ctx, mp, baseRepo, baseManifest.Config.Digest, nil)
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
		start = time.Now()
		rc, err := pullBlobWithAuth(ctx, mp, appRepo, desc.Digest, appAuth)
		h.ops.ObserveImagedOCIPull("blob", pullResult(err), time.Since(start))
		if err != nil {
			for _, c := range closers {
				_ = c.Close()
			}
			return aboveBaseStream{}, nil, fmt.Errorf("pull blob %s: %w", desc.Digest, err)
		}
		closers = append(closers, rc)
		readers = append(readers, rc)
	}
	// One observation per above-base resolution completing — feeds the
	// "above_base" bucket of the histogram (the §12 dashboard's
	// "above-base resolved" panel). Result is "ok" when at least one
	// layer was streamed; an empty above (zero new layers) still counts
	// as "ok" — that's the steady-state case where the deploy matches
	// the base exactly.
	h.ops.ObserveImagedOCIPull("above_base", "ok", time.Since(start))
	return aboveBaseStream{readers: readers, closers: closers}, above, nil
}

// pullResult maps a non-nil error to "err"; nil to "ok". The histogram
// takes a closed label set {"ok","err"}; anything else becomes a new
// series Prometheus doesn't know about (and silently drops).
func pullResult(err error) string {
	if err == nil {
		return "ok"
	}
	return "err"
}

// pullConfig fetches and parses the OCI image config referenced by a manifest.
// The config carries the env/entrypoint (run by guest-init) AND the
// rootfs.diff_ids that drive the two-drive math.
//
// `auth` (issue #461 / ADR-062) threads the customer's per-app
// private-registry Basic Auth credential through the blob fetch.
// Pass nil for base pulls (the base is always public).
func (h *Handler) pullConfig(ctx context.Context, mp oci.ManifestPuller, repo, digest string, auth *oci.BasicAuth) (oci.Config, error) {
	start := time.Now()
	r, err := pullBlobWithAuth(ctx, mp, repo, digest, auth)
	if err != nil {
		h.ops.ObserveImagedOCIPull("config", "err", time.Since(start))
		return oci.Config{}, err
	}
	defer func() { _ = r.Close() }()
	cfg, perr := oci.ParseConfig(r)
	res := pullResult(perr)
	h.ops.ObserveImagedOCIPull("config", res, time.Since(start))
	return cfg, perr
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

// pullDigestWithAuth dispatches PullDigest through the AuthPuller seam
// when the production RegistryClient is wired; falls back to the
// anonymous PullDigest for offline DefaultPuller. auth == nil collapses
// to the anonymous path on both branches (issue #461 / ADR-062).
func pullDigestWithAuth(ctx context.Context, p oci.Puller, ref string, auth *oci.BasicAuth) (string, error) {
	if ap, ok := p.(oci.AuthPuller); ok {
		return ap.PullDigestWithAuth(ctx, ref, auth)
	}
	return p.PullDigest(ctx, ref)
}

// pullImageConfigWithAuth mirrors pullDigestWithAuth for PullImageConfig.
func pullImageConfigWithAuth(ctx context.Context, p oci.Puller, ref string, auth *oci.BasicAuth) (oci.ImageConfig, error) {
	if ap, ok := p.(oci.AuthPuller); ok {
		return ap.PullImageConfigWithAuth(ctx, ref, auth)
	}
	return p.PullImageConfig(ctx, ref)
}

// pullLayersWithAuth mirrors pullDigestWithAuth for PullLayers.
func pullLayersWithAuth(ctx context.Context, p oci.Puller, ref string, auth *oci.BasicAuth) (oci.PullLayersResult, error) {
	if ap, ok := p.(oci.AuthPuller); ok {
		return ap.PullLayersWithAuth(ctx, ref, auth)
	}
	return p.PullLayers(ctx, ref)
}

// pullManifestWithAuth dispatches PullManifest through the
// AuthManifestPuller seam when wired; falls back to the anonymous
// PullManifest otherwise. The M6 two-drive path calls this for both
// app manifest + base manifest pulls (issue #461 / ADR-062).
func pullManifestWithAuth(ctx context.Context, mp oci.ManifestPuller, ref string, auth *oci.BasicAuth) (oci.Manifest, error) {
	if amp, ok := mp.(oci.AuthManifestPuller); ok {
		return amp.PullManifestWithAuth(ctx, ref, auth)
	}
	return mp.PullManifest(ctx, ref)
}

// pullBlobWithAuth mirrors pullManifestWithAuth for PullBlob. The M6
// two-drive path uses this for app blobs (auth carried) AND base
// blobs (auth == nil).
func pullBlobWithAuth(ctx context.Context, mp oci.ManifestPuller, repo, digest string, auth *oci.BasicAuth) (io.ReadCloser, error) {
	if amp, ok := mp.(oci.AuthManifestPuller); ok {
		return amp.PullBlobWithAuth(ctx, repo, digest, auth)
	}
	return mp.PullBlob(ctx, repo, digest)
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
	be, err := h.storageFor()
	if err != nil {
		// Issue #197 B3.8: storage init failure during cleanup is a
		// Warn — there's nothing useful to clean up if storage is
		// missing, and we don't want to fail the deploy that triggered
		// this cleanup. The caller (HandleNotification) logs the
		// returned error at Warn.
		h.log.Warn("imaged: cleanup storageFor", "deployment", dep.ID, "err", err)
		return err
	}
	appsKey := sched.AppLayerKey(app.Slug, dep.ID)
	if err := be.Delete(ctx, appsKey); err != nil {
		h.log.Warn("imaged: cleanup ext4", "key", appsKey, "err", err)
	}
	if !keepSnap {
		memKey := state.SnapMemKey(dep.ID)
		vmKey := state.SnapVMStateKey(dep.ID)
		if err := be.Delete(ctx, memKey); err != nil {
			h.log.Warn("imaged: cleanup snap mem", "key", memKey, "err", err)
		}
		if err := be.Delete(ctx, vmKey); err != nil {
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
	be, err := h.storageFor()
	if err != nil {
		// Issue #197 B3.8: storage init failure during app cleanup is
		// Warn-only — return the error so HandleNotification logs it,
		// but don't fail the caller.
		return fmt.Errorf("imaged: app cleanup storageFor: %w", err)
	}
	for _, d := range deps {
		appsKey := sched.AppLayerKey(app.Slug, d.ID)
		if err := be.Delete(ctx, appsKey); err != nil {
			h.log.Warn("imaged: app cleanup ext4", "key", appsKey, "err", err)
		}
		// Issue #463 / ADR-069 / PR-B: walk the deployment's
		// per-workload sidecar ext4 set and delete each. The
		// store-side FK CASCADE on `deployment_sidecar_layers`
		// keeps the row consistent; this loop removes the
		// storage artifact that the row used to reference.
		// We swallow List errors as Warn (the FK-side cascade
		// means the row goes with the deployment even if the
		// storage sweep fails, and a future rebuild would
		// generate fresh keys).
		if layers, listErr := h.store.ListDeploymentSidecarLayers(ctx, d.ID); listErr == nil {
			for _, l := range layers {
				if delErr := be.Delete(ctx, l.StorageKey); delErr != nil {
					h.log.Warn("imaged: app cleanup sidecar ext4",
						"key", l.StorageKey, "sidecar", l.SidecarName, "err", delErr)
				}
			}
		} else {
			h.log.Warn("imaged: app cleanup list sidecar layers",
				"deployment", d.ID, "err", listErr)
		}
		memKey := state.SnapMemKey(d.ID)
		vmKey := state.SnapVMStateKey(d.ID)
		if err := be.Delete(ctx, memKey); err != nil {
			h.log.Warn("imaged: app cleanup snap mem", "key", memKey, "err", err)
		}
		if err := be.Delete(ctx, vmKey); err != nil {
			h.log.Warn("imaged: app cleanup snap vmstate", "key", vmKey, "err", err)
		}
	}
	return nil
}

// --- F2/Issue #299 / ADR-038 Phase 3: SBOM populator seams -------------------

// sbomStorageKeyForDeployment resolves the canonical CycloneDX storage
// key for a deployment's source tree, or "" when no build row exists
// for the deployment (issue #299 / ADR-038 Phase 3). The image-only
// deploy path (app.Type == AppTypeApp on the legacy
// handleDeployment arm) has no build — the OCI image comes straight
// from the registry, and a build_provenance row would be empty. We
// return "" rather than synthesising an empty-key stamp; the build
// will populate the column on its first cache-hit (builderd calls
// recordProvenance at every markSucceeded site).
//
// When the deployment's source has a build row, the storage key is
// derived from its build_id:
//
//	sboms/<build_id>.cdx.json
//
// The convention mirrors BuildSBOMKey() in pkg/imaged/sbom.go (the
// populator helper). Re-deriving here rather than importing
// BuildSBOMKey avoids a tiny read-side alias across packages — the
// field is private, but the key shape is the load-bearing contract
// the apid GET handler reads back.
func (h *Handler) sbomStorageKeyForDeployment(ctx context.Context, deploymentID string) string {
	if deploymentID == "" {
		return ""
	}
	build, err := h.store.BuildByDeployment(ctx, deploymentID)
	if err != nil {
		// ErrNotFound is the steady-state for image-only deploys;
		// log at Debug (not Warn) so we don't pollute the log
		// stream with the legacy arm.
		if !errors.Is(err, state.ErrNotFound) {
			h.log.Debug("imaged: sbom key lookup failed",
				"deployment", deploymentID, "err", err)
		}
		return ""
	}
	return BuildSBOMKey(build.ID)
}

// updateBuildProvenanceSBOM stamps the SBOM storage key onto the
// build_provenance row for this deployment (issue #299 /
// ADR-038 Phase 3). Best-effort: an error here is logged at WARN
// and the build still succeeds (the SBOM is observational
// metadata, schema §4.2).
//
// The deployment may not have a build row yet (the image-only
// arm of handleDeployment) — in that case sbomKey is "" and we
// skip the lookup entirely. sbomKey == "" also covers the
// syft-failure case: the build still succeeds, build_provenance
// is stamped with empty sbom_storage_key, and the apid GET
// returns 503 build_sbom_unavailable.
func (h *Handler) updateBuildProvenanceSBOM(ctx context.Context, deploymentID, sbomKey string) {
	if deploymentID == "" {
		return
	}
	build, err := h.store.BuildByDeployment(ctx, deploymentID)
	if err != nil {
		if errors.Is(err, state.ErrNotFound) {
			// Image-only deploy or pre-build state — no
			// build_provenance row to stamp yet.
			return
		}
		h.log.Warn("imaged: build_provenance lookup failed",
			"deployment", deploymentID, "err", err)
		return
	}
	if err := h.store.UpdateBuildProvenanceSBOM(ctx, build.ID, sbomKey); err != nil {
		// A ErrNotFound here means the builderd populator
		// INSERT failed (logged at WARN inside
		// builderd.recordProvenance). We log at WARN too so the
		// missing pair surfaces in operator dashboards, but do
		// not fail the deploy — the build itself is the
		// load-bearing artefact.
		h.log.Warn("imaged: stamp sbom_storage_key",
			"build", build.ID, "sbom_key", sbomKey, "err", err)
	}
}

// --- F2: FC-version startup sweep ------------------------------------------

// MarkFCSnapshotsStale is the F2 sweep body. It is invoked once at imaged
// startup (cmd/imaged/main.go wires it) and never on a timer — a Firecracker
// upgrade requires the operator to restart imaged, which matches the
// "snapshots are cache, not truth" framing (ADR-005). Idempotent.
//
// Issue #470 / PR C / ADR-074: when n > 0, walk the just-marked-stale
// rows and emit app.warm_snapshot_stale per affected app. The kind
// joins with schedd's app.warm_snapshot_promoted and apid's
// app.warm_snapshot_disabled to give operators a single-grep
// lifecycle audit trail. Subject = &app.AccountID per ADR-074 §3.2.
// The walk is best-effort: an audit-write failure here is logged
// and does NOT roll back the mark-stale (the FC-version truth is
// what matters; the audit row is observer signal only).
func (h *Handler) MarkFCSnapshotsStale(ctx context.Context, fcVersion string) (int64, error) {
	if fcVersion == "" {
		return 0, errors.New("imaged: MarkFCSnapshotsStale: empty fc version")
	}
	// Snapshot per-app non-stale row counts BEFORE the sweep so
	// emitWarmSnapshotStale can compute per-app "rows flipped" for
	// each emit. ListSnapshotsForGC filters stale=false; after the
	// sweep the same query returns only survivors.
	beforeByApp, err := h.snapshotNonStaleByApp(ctx)
	if err != nil {
		return 0, fmt.Errorf("imaged: mark stale by fc: pre-sweep list: %w", err)
	}
	n, err := h.store.MarkAllSnapshotsStaleByFCVersion(ctx, fcVersion)
	if err != nil {
		return 0, fmt.Errorf("imaged: mark stale by fc: %w", err)
	}
	if n > 0 && h.audit != nil {
		afterByApp, listErr := h.snapshotNonStaleByApp(ctx)
		if listErr != nil {
			// Best-effort: log the list failure and still return
			// n to the caller (the mark-stale itself succeeded).
			h.log.Warn("imaged: warm_snapshot_stale post-sweep list",
				"err", listErr)
		} else {
			h.emitWarmSnapshotStale(ctx, fcVersion, beforeByApp, afterByApp)
		}
	}
	return n, nil
}

// snapshotNonStaleByApp returns the {appID: count} of non-stale
// rows currently in ListSnapshotsForGC. Used to compute per-app
// delta for the warm_snapshot_stale audit emit. Empty apps are
// omitted from the map.
func (h *Handler) snapshotNonStaleByApp(ctx context.Context) (map[string]int64, error) {
	rows, err := h.store.ListSnapshotsForGC(ctx)
	if err != nil {
		return nil, err
	}
	out := make(map[string]int64, len(rows))
	for _, r := range rows {
		if r.AppID == "" {
			continue
		}
		out[r.AppID]++
	}
	return out, nil
}

// MarkAppProtocolSnapshotsStale is the F3-app-protocol sweep
// (ADR-127 §D1, Layer 6). Mirrors MarkFCSnapshotsStale but flips
// every non-stale snapshot whose deployment's app.app_protocol ∈
// {http2, grpc} stale. Called from runFCSweep AFTER F2 (the
// Firecracker-version sweep). The two sweeps have different
// triggers (F2 on FC upgrade per ADR-005; F3 on
// FAAS_BASE_IMAGE_VERSION bump per ADR-127) and different audit
// subjects ("fc_version:<v>" vs "app_protocol:<v>") — they are
// intentionally NOT merged.
//
// app_protocol=http1 snapshots are NEVER affected (ADR-126
// §Decision 6). The sweep's audit subject carries the
// FAAS_BASE_IMAGE_VERSION stamp so operators reading the audit
// log can correlate a base-image bump with the rebuild.
func (h *Handler) MarkAppProtocolSnapshotsStale(ctx context.Context) (int64, error) {
	// The wire-protocol-capable close-set is {http2, grpc}; http1
	// stays on the unchanged H1+chunked path.
	h2cProtocols := []string{api.AppProtocolHTTP2, api.AppProtocolGRPC}

	// Capture per-app non-stale row counts BEFORE the sweep so the
	// audit emit can compute per-app "rows flipped" deltas (same
	// shape as F2 — see MarkFCSnapshotsStale above).
	beforeByApp, err := h.snapshotNonStaleByApp(ctx)
	if err != nil {
		return 0, fmt.Errorf("imaged: mark stale by app_protocol: pre-sweep list: %w", err)
	}
	n, err := h.store.MarkAllSnapshotsStaleByAppProtocol(ctx, h2cProtocols)
	if err != nil {
		return 0, fmt.Errorf("imaged: mark stale by app_protocol: %w", err)
	}
	if n > 0 && h.audit != nil {
		afterByApp, listErr := h.snapshotNonStaleByApp(ctx)
		if listErr != nil {
			// Best-effort: log the list failure and still return n
			// to the caller (the mark-stale itself succeeded).
			h.log.Warn("imaged: warm_snapshot_stale (app_protocol) post-sweep list",
				"err", listErr)
		} else {
			h.emitWarmSnapshotStale(ctx,
				"app_protocol:"+fcvm.FAAS_BASE_IMAGE_VERSION,
				beforeByApp, afterByApp)
		}
	}
	return n, nil
}

// emitWarmSnapshotStale (issue #470 / PR C / ADR-074) emits one
// app.warm_snapshot_stale audit row per app that had at least
// one snapshot row transition to stale during the FC-version
// sweep. stale_count is the per-app count of rows that flipped
// (NOT the fleet total — operators reading the audit row expect
// the value to be scoped to the app_id subject).
//
// Caveat (ADR-074 §3.2): apps whose ENTIRE fleet went stale in
// this sweep emit no row because ListSnapshotsForGC filters
// stale=false. Those apps surface through the fleet-level
// warm_snapshot_write_failures counter, not per-app audit.
// Best-effort: an audit-write failure is logged and does NOT
// roll back the mark-stale (ADR-005 says FC version is the
// source of truth; the audit row is observer signal only).
func (h *Handler) emitWarmSnapshotStale(ctx context.Context, fcVersion string, beforeByApp map[string]int64, afterByApp map[string]int64) {
	for appID, before := range beforeByApp {
		after, ok := afterByApp[appID]
		if !ok {
			// No surviving non-stale rows → all of this app's
			// rows went stale. The fleet-level counter is the
			// only signal; per-app audit omitted (caveat).
			continue
		}
		staleThisApp := before - after
		if staleThisApp <= 0 {
			continue
		}
		app, err := h.store.AppByID(ctx, appID)
		if err != nil {
			h.log.Warn("imaged: warm_snapshot_stale app load",
				"app", appID, "err", err)
			continue
		}
		h.audit.Emit(ctx, "app.warm_snapshot_stale", &app.AccountID, map[string]any{
			"app_id":      app.ID,
			"slug":        app.Slug,
			"fc_version":  fcVersion,
			"stale_count": staleThisApp,
		})
	}
}

// dispatchFullRootfs is the typed-sentinel gate that decides whether
// the deployment proceeds via the full-rootfs build path (commit 5)
// or surfaces today-equivalent failure (ADR-141 §Decision 2 +
// §Decision 3).
//
// Tri-state resolution (commit 6):
//   - dep.FullRootfsOverride=&false → today-equivalent failure (force-off).
//   - dep.FullRootfsOverride=&true  → full-rootfs (force-on, even on Free).
//   - dep.FullRootfsOverride=nil:
//     - dep.FullRootfsAllowAuto && paid plan → full-rootfs (auto).
//     - else                              → today-equivalent failure.
//
// "Paid plan" means PlanHobby / PlanPro / PlanScale per
// api.PlanMeetsMinimumPlan(_, PlanHobby). Free plan auto-fallback
// is rejected; the customer MUST opt in via FullRootfsOverride=&true.
// The two-drive path's behavior is unchanged for any image whose
// layers prefix one of our runner-* bases.
func (h *Handler) dispatchFullRootfs(
	ctx context.Context,
	app state.App,
	dep state.Deployment,
	acct state.Account,
	manifest api.AppManifest,
	appAuth *oci.BasicAuth,
) error {
	if dep.FullRootfsOverride != nil && !*dep.FullRootfsOverride {
		// Force-off: surface today-equivalent failure so pkg/api
		// SentinelToCode maps to CodeImageManifestInvalid (422).
		return fmt.Errorf("%w: customer forced two-drive path via FullRootfsOverride=&false", oci.ErrLayersNotAboveBase)
	}
	if dep.FullRootfsOverride != nil && *dep.FullRootfsOverride {
		// Force-on: honor regardless of plan / AllowAuto.
		return h.buildFullRootfsLayer(ctx, app, dep, acct, manifest, appAuth)
	}
	if dep.FullRootfsAllowAuto && api.PlanMeetsFullRootfs(acct.Plan) {
		// Auto-dispatch: paid plans opt-in by default. Free plans
		// must explicitly opt in via FullRootfsOverride=&true.
		return h.buildFullRootfsLayer(ctx, app, dep, acct, manifest, appAuth)
	}
	// Default: today-equivalent failure on Free without override
	// (and on any future plan whose AllowAuto=false without
	// override).
	return fmt.Errorf("%w: plan %s does not auto-dispatch to full-rootfs; pass FullRootfsOverride=&true to opt in",
		oci.ErrLayersNotAboveBase, acct.Plan)
}

// buildFullRootfsLayer pulls ALL of the app image's layers
// (bottom-to-top), calls rootfs.Builder.BuildFullRootfs, and stamps
// the resulting ext4 to the same appsKey the two-drive path uses
// (ADR-141 §Decision 4: shared StorageKey shape). No drive0 base
// pull — full-rootfs images are self-contained.
//
// The auth wire mirrors aboveBaseLayers (M6 two-drive path): the
// app manifest + app blobs carry appAuth; no base is consulted.
func (h *Handler) buildFullRootfsLayer(
	ctx context.Context,
	app state.App,
	dep state.Deployment,
	acct state.Account,
	manifest api.AppManifest,
	appAuth *oci.BasicAuth,
) error {
	mp, ok := h.oci.(oci.ManifestPuller)
	if !ok {
		return fmt.Errorf("imaged: full-rootfs requires oci.ManifestPuller; got %T", h.oci)
	}
	appRepo := repoWithHost(dep.ImageDigest)
	if appRepo == "" {
		return fmt.Errorf("imaged: cannot derive repo from %q", dep.ImageDigest)
	}
	start := time.Now()
	appManifest, err := pullManifestWithAuth(ctx, mp, dep.ImageDigest, appAuth)
	h.ops.ObserveImagedOCIPull("manifest", pullResult(err), time.Since(start))
	if err != nil {
		return fmt.Errorf("imaged: full-rootfs manifest: %w", err)
	}
	// Issue #461 / ADR-062: stamp credential used after a
	// successful app manifest pull on the full-rootfs path too —
	// every authenticated pull above this line was either app
	// manifest, app config, or app blob.
	refHost := ""
	if parsedRef, parseErr := oci.ParseReference(dep.ImageDigest); parseErr == nil {
		refHost = parsedRef.APIHost()
	}
	h.markRegistryCredentialUsed(ctx, app, refHost, appAuth)

	if len(appManifest.Layers) == 0 {
		return fmt.Errorf("imaged: full-rootfs image has zero layers")
	}
	readers := make([]io.Reader, 0, len(appManifest.Layers))
	closers := make([]io.Closer, 0, len(appManifest.Layers))
	for _, l := range appManifest.Layers {
		start := time.Now()
		rc, err := pullBlobWithAuth(ctx, mp, appRepo, l.Digest, appAuth)
		h.ops.ObserveImagedOCIPull("blob", pullResult(err), time.Since(start))
		if err != nil {
			for _, c := range closers {
				_ = c.Close()
			}
			return fmt.Errorf("imaged: full-rootfs pull blob %s: %w", l.Digest, err)
		}
		closers = append(closers, rc)
		readers = append(readers, rc)
	}
	defer func() {
		for _, c := range closers {
			_ = c.Close()
		}
	}()

	be, err := h.storageFor()
	if err != nil {
		return fmt.Errorf("imaged: full-rootfs storage backend: %w", err)
	}
	appsKey := sched.AppLayerKey(app.Slug, dep.ID)

	res, err := h.builder.BuildFullRootfs(ctx, rootfs.BuildFullRootfsInput{
		Layers:         readers,
		Manifest:       manifest,
		GuestInitPath:  h.guestInitPath,
		Plan:           acct.Plan,
		Storage:        be,
		StorageKey:     appsKey,
		OutImage:       h.appsRootPath(app.Slug, dep.ID),
		SBOMRun:        h.syftRun,
		SBOMStorageKey: h.sbomStorageKeyForDeployment(ctx, dep.ID),
		// Resolver wired by commit 7 from the image's merged
		// /etc/passwd table. Today (commit 6) we pass nil so the
		// per-entry chown path falls through to the daemon uid +
		// unparseable_uid counter — same as the two-drive path.
		Resolver: nil,
	})
	if err != nil {
		_ = h.transition(ctx, dep.ID, state.DeployFailed, "build full-rootfs: "+err.Error())
		return fmt.Errorf("imaged: build full-rootfs: %w", err)
	}
	h.updateBuildProvenanceSBOM(ctx, dep.ID, res.SBOMKey)
	if err := h.store.SetDeploymentRootfs(ctx, dep.ID, h.appsRootPath(app.Slug, dep.ID), appsKey, res.ContentBytes); err != nil {
		_ = h.transition(ctx, dep.ID, state.DeployFailed, "stamp full-rootfs: "+err.Error())
		return fmt.Errorf("imaged: stamp full-rootfs: %w", err)
	}
	if err := h.replicateLayer(ctx, appsKey); err != nil {
		_ = h.transition(ctx, dep.ID, state.DeployFailed, err.Error())
		return err
	}
	h.log.Info("imaged: build app layer (full-rootfs)",
		"app", app.Slug,
		"digest", dep.ImageDigest,
		"key", res.ImageKey,
		"bytes", res.ContentBytes,
		"layers", len(appManifest.Layers),
		"plan", string(acct.Plan),
	)
	return nil
}
