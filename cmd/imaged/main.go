// Command imaged — image and snapshot service (spec §4.6).
//
// imaged owns OCI→bootable-rootfs conversion (the two-drive scheme), base/runner
// images, and snapshot GC. It turns the layers ABOVE a shared base into a per-app
// ext4 app layer, injects guest-init + the app.json contract, and enforces the
// plan's app-layer cap. Never flatten to one rootfs per app (spec §4.6).
//
// M8 wiring: the daemon owns a Loop that drives
//
//   - the LISTEN subscriber (deployment_changed, build_queued, snapshot_boot,
//     snapshot_written, app_changed),
//   - the nightly GC (per-app keep current+previous; fleet budget pressure
//     evicts from the heaviest accounts first),
//   - a one-shot FC-version sweep on startup that marks all stale-version
//     snapshots stale (ADR-005).
//
// runDeps is the DI seam for tests (mirror cmd/schedd/main.go).
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/capdecl/runtimecheck"
	"github.com/onebox-faas/faas/pkg/cosign"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/imaged"
	"github.com/onebox-faas/faas/pkg/manifest"
	"github.com/onebox-faas/faas/pkg/oci"
	"github.com/onebox-faas/faas/pkg/role"
	"github.com/onebox-faas/faas/pkg/rootfs"
	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/secretbox"
	"github.com/onebox-faas/faas/pkg/secretscan"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/storage"
	"github.com/onebox-faas/faas/pkg/trace"
	"github.com/onebox-faas/faas/pkg/wire"
)

func main() {
	wire.Daemon("imaged", run)
}

// runDeps is the DI seam for tests. Production wires every field via
// defaultDeps(); tests swap one or two. Mirrors cmd/schedd/main.go::runDeps.
type runDeps struct {
	openDB    func(ctx context.Context, url string) (*pgxpool.Pool, error)
	migrate   func(ctx context.Context, pool *pgxpool.Pool) error
	lvUsedPct func(ctx context.Context) (float64, error)
	detectFC  func(ctx context.Context) (string, error)
	now       func() time.Time
	// configPath is the on-disk TOML path; ADR-122 introduces a
	// minimal cmd/imaged/config.go that LoadConfig reads. Empty
	// defaults to /etc/faas/imaged.toml in defaultDeps.
	configPath string
	// loadConfig is the seam tests use to inject a pre-built
	// *Config without writing to disk. nil in production →
	// LoadConfig(configPath).
	loadConfig func(path string) (*Config, error)
	// capCheck: DEPLOY-1 / ADR-075 capdecl gate seam. nil →
	// runtimecheck.MustCheckOnBoot(capsDecl, log, nil) which
	// exits on violation in production. Tests inject
	// func() error { return nil } to bypass the live capset
	// (the runner lacks the production capset, and
	// MustCheckOnBoot's os.Exit on violation would kill the
	// test process). Review finding M2: this seam now matches
	// the vmmd shape — every daemon's runtimecheck boot gate
	// is overridable for tests.
	capCheck func() error
}

func defaultDeps() runDeps {
	return runDeps{
		openDB: db.Open,
		migrate: func(ctx context.Context, pool *pgxpool.Pool) error {
			// F2 / ADR-124: acquires pg_advisory_lock; safe for fleet bootstrap.
			return db.MigrateUp(ctx, pool)
		},
		lvUsedPct:  imaged.DefaultLvFcUsedPct(imaged.LvFcName),
		detectFC:   imaged.DetectFirecrackerVersion,
		now:        time.Now,
		configPath: "/etc/faas/imaged.toml",
		loadConfig: LoadConfig,
	}
}

func run(ctx context.Context, log *slog.Logger) error {
	return defaultDeps().run(ctx, log)
}

func (d runDeps) run(ctx context.Context, log *slog.Logger) error {
	// DEPLOY-1 / ADR-075 capdecl gate. imaged's capsDecl
	// asserts no cap_sys_admin in Bnd (review finding M1).
	// A future PR that brings back
	// AmbientCapabilities=cap_sys_admin would trip this check
	// at boot, not silently at the first mount syscall. The
	// capCheck seam lets tests stub out the live /proc/self
	// status check (the test runner's cap set does not match
	// the production EX44 cap set).
	capCheck := d.capCheck
	if capCheck == nil {
		capCheck = func() error { return runtimecheck.MustCheckOnBoot(capsDecl, log, nil) }
	}
	if err := capCheck(); err != nil {
		return err
	}
	traceShutdown, traceErr := trace.InitTracer(ctx, "imaged", wire.Version, log)
	if traceErr != nil {
		return fmt.Errorf("imaged: init tracing: %w", traceErr)
	}
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		if err := traceShutdown(shutdownCtx); err != nil {
			log.Warn("imaged: trace shutdown failed", "err", err)
		}
	}()

	// ADR-122 / follow-on to imaged env-only config (issue #995
	// post-merge audit): load /etc/faas/imaged.toml with defaults.
	// Missing file is not an error — the defaults produce a working
	// daemon. The pre-existing FAAS_IMAGED_METRICS_ADDR env overlay
	// is honoured by GetMetricsAddr below.
	loadCfg := d.loadConfig
	if loadCfg == nil {
		loadCfg = LoadConfig
	}
	cfgPath := d.configPath
	if cfgPath == "" {
		cfgPath = "/etc/faas/imaged.toml"
	}
	imgCfg, err := loadCfg(cfgPath)
	if err != nil {
		return err
	}

	// Gate-B box-role gate. imaged is a compute-only daemon — it
	// refuses to start under RoleControlPlane. The role is resolved
	// from TOML role (post-ADR-122) with FAAS_IMAGED_ROLE as the
	// env-overlay (role.FromConfig falls back to the second arg
	// when the first is empty). Default RoleSingleBox so single-box
	// dev boots unmoved. The gate runs before d.openDB so a
	// misconfigured boot doesn't waste a Postgres connection.
	if err := role.Require("imaged", role.FromConfig(string(imgCfg.Role), "FAAS_IMAGED_ROLE"),
		role.RoleSingleBox, role.RoleComputeOnly); err != nil {
		return err
	}

	// Mega-PR-A (issue #911 / ADR-110 PR-1): capture FAAS_NODE_NAME
	// before any control-plane handshake so the boot log carries
	// the identity. imaged has NO TOML NodeName field today — the
	// env var is read directly here. The systemd drop-in
	// (deploy/ansible/roles/compute_only_service/files/
	// faas-imaged.service.d/99-faas-node-name.conf) is the single
	// source of truth. Empty + log.Info("legacy single-box") mirrors
	// the schedd owner-node line. (Future improvement: lift
	// NodeName into cmd/imaged/config.go so it matches the
	// schedd/meterd/builderd shape — out of scope for ADR-122.)
	if nodeName := os.Getenv("FAAS_NODE_NAME"); nodeName != "" {
		log.Info("imaged owner node", "node_name", nodeName)
	} else {
		log.Info("imaged: legacy single-box (FAAS_NODE_NAME unset)")
	}

	// PR-5 / issue #911 — manifest reconcile against FAAS_BUILDER_BASE_REF.
	// When FAAS_MANIFEST_PATH is set, load the split-box deployment
	// manifest and assert the manifest's release.builder_base_digest
	// (a sha256 digest from PR-0's schema) matches the resolved
	// FAAS_BUILDER_BASE_REF. A drift between what the manifest
	// promises and what imaged actually loads is silent today; PR-5
	// makes it fatal at boot, pointing the operator at
	// `gregale manifest validate`. Single-box installs that don't
	// carry a manifest are unaffected (env-only behaviour).
	if err := reconcileManifestBuilderBase(); err != nil {
		return err
	}

	pool, err := d.openDB(ctx, "")
	if err != nil {
		return err
	}
	defer pool.Close()
	if err := d.migrate(ctx, pool); err != nil {
		return err
	}

	store := state.NewPgStore(pool)
	builder := rootfs.NewBuilder(wire.ExecRunner{})

	// ADR-038 / Tier 3 phase 3: validate the build-attestation
	// signing key at startup. The path defaults to
	// /etc/faas/secrets/sign.key (root:root 0400 or 0440);
	// FAAS_SIGN_KEY overrides for test harnesses + dev boxes
	// that don't have the canonical path. Fail-loud on
	// missing/insecure perms — silent insecure boots are the
	// failure mode ADR-038 §Consequences Compatibility calls
	// out. The actual signer is constructed below (after
	// storage is wired) via cosign.NewLocalSigner, which does
	// its own LoadPrivateKeyFile call — the parse is cheap
	// (PKCS8 in-memory, the previous read is cached by the
	// kernel), but holding the path string here also lets
	// NewLocalSigner own the single Load path.
	signKeyPath := envOr("FAAS_SIGN_KEY", cosign.DefaultSignKeyPath)
	// Eager mode check: fail-loud BEFORE we touch storage or
	// the DB pool so a missing sign.key gets the operator-facing
	// "run `faas sign-keys init`" message without a confusing
	// storage-dial error stacked on top.
	if _, err := os.Stat(signKeyPath); err != nil {
		return fmt.Errorf("imaged: sign key %q: %w (run `faas sign-keys init` to provision)", signKeyPath, err)
	}
	log.Info("imaged: build attestation sign key present", "key", signKeyPath)

	// Real registry v2 puller: resolves an image deploy's digest-pinned
	// reference against the public registry. The HTTP transport enforces
	// the egress denylist (RFC1918 / link-local / metadata / CGN / SMTP)
	// at dial time so a customer-side OCI reference that resolves (or
	// DNS-rebinds) to a private address is refused before any data leaves
	// the box (spec §11, issue #27).
	//
	// FAAS_OCI_INSECURE=1 swaps the egress-guarded client for a plain
	// http.Client AND flips the OCI scheme to http. Test harness only —
	// never set in production. Lets the e2e tests pull from an httptest
	// registry bound to loopback (which the egress guard denies and which
	// serves plain HTTP, not HTTPS).
	pullerOpts := []oci.Option{
		oci.WithHTTPClient(oci.NewEgressHTTPClient()),
		oci.WithTimeout(ociPullTimeout()),
	}
	if os.Getenv("FAAS_OCI_INSECURE") == "1" {
		log.Warn("FAAS_OCI_INSECURE=1 — egress guard disabled, e2e test mode only")
		pullerOpts = []oci.Option{
			oci.WithHTTPClient(&http.Client{}),
			oci.WithEndpoint("http", ""),
			oci.WithTimeout(ociPullTimeout()),
		}
	}
	puller := oci.NewRegistryClient(pullerOpts...)
	log.Info("imaged: oci puller ready", "timeout_s", int(ociPullTimeout().Seconds()))

	notifier := dbNotifier{pool: pool}
	guestInitPath := guestInitPathFromEnv()
	appsRoot := envOr("FAAS_APPS_ROOT", "/var/lib/faas/apps")

	// #96 / ADR-025 axis 2: build the StorageBackend the imaged Handler
	// publishes through. The env-driven fork (FAAS_STORAGE_BACKEND) lets
	// operators route the same call sites through a remote OCI
	// distribution-spec backend instead of the local FS layout — the
	// PrefixRouter / apps-fc split only applies to the local driver.
	storageBackend, err := storage.BackendFromEnv()
	if err != nil {
		return fmt.Errorf("imaged: %w", err)
	}
	if envOr("FAAS_STORAGE_BACKEND", "local") == "oci" {
		log.Info("imaged: storage backend = oci", "registry", envOr("FAAS_OCI_REGISTRY", ""))
	} else {
		log.Info("imaged: storage backend = local", "fc_root", envOr("FAAS_STORAGE_ROOT", "/srv/fc"),
			"apps_root", appsRoot)
	}

	// ADR-038: now that storage is wired, construct the production
	// LocalSigner. NewLocalSigner owns the canonical LoadPrivateKeyFile
	// call (mode check + PKCS8 parse); the earlier os.Stat guard above
	// is the eager fail-loud so a missing sign.key gets the
	// operator-facing "run `faas sign-keys init`" message BEFORE the
	// storage-backend dial errors confuse the failure surface.
	signer, err := cosign.NewLocalSigner(signKeyPath, storageBackend, log)
	if err != nil {
		return fmt.Errorf("imaged: build signer: %w", err)
	}
	builder.WithSigner(signer)
	// One per-daemon Prometheus registry, shared by the handler
	// (OCI-pull observations inside aboveBaseLayers + buildImageLayer)
	// and the /metrics listener below. PR #132 constructed two
	// separate registries — the handler recorded into one, the listener
	// served an empty one, so /metrics never showed observed series.
	// (Fixup for PR #132: rules in deploy/ansible/roles/prometheus/
	// files/faas.rules.yml depend on imaged_oci_pull_duration_seconds
	// being live, not empty.)
	ops := wire.NewOpsMetrics("imaged")
	wire.BootStamps(ctx, "imaged", ops)
	wire.RegisterDefaultOps(ops)
	// M-1 / ADR-136 §Decision 2: wire pkg/rootfs's per-layer
	// ownership-clamp + skipped-entry counters onto the daemon's
	// OpsMetrics so imaged_ownership_clamp_total{reason} and
	// imaged_layer_entry_skipped_total surface on /metrics. The
	// setter is idempotent — call once at boot.
	rootfs.SetOpsMetrics(ops)
	// ADR-054 acceptance: wire the LocalCacheBackend observer onto the
	// daemon's *wire.OpsMetrics so stale-fallback serves emit
	// `imaged_storage_cache_stale_fallback_total`. Uses
	// storage.AsCacheBackend so the observer attaches even when the
	// BackendFromEnv shape changes (a future metrics wrapper,
	// router-encloses-cache, etc.). Nil result is expected on
	// single-box local deploys — the cache is opt-in there and the
	// counter stays at zero forever.
	if cacheBE := storage.AsCacheBackend(storageBackend); cacheBE != nil {
		cacheBE.SetObserver(storage.LogCacheObserver{
			Logger: log,
			Next: storage.FuncCacheObserver(func() {
				ops.StorageCacheStaleFallback().Inc()
			}),
		})
	}
	// PR-E: wire oci.EgressDenyHook to the imaged-side counter so
	// the OCI dialer refusals surface as
	// imaged_oci_egress_deny_total{cidr,family}. The hook is
	// installed exactly once at daemon startup (the oci package
	// holds it as a package-level var; subsequent test runs would
	// override it but we never re-enter imaged's main in the same
	// process). Pre-instantiate the OCI-only extras so their
	// (cidr, family) series surface from boot (the catalog
	// portion is already pre-instantiated inside NewOpsMetrics).
	oci.EgressDenyHook = func(_ netip.Addr, counterName, family string) {
		ops.OCIEgressDeny(counterName, family).Inc()
	}
	for _, lbl := range oci.OCIOnlyDenyCounterLabels() {
		ops.OCIEgressDeny(lbl.CounterName, lbl.Family)
	}
	var artifactReplicator imaged.ArtifactReplicator
	if helper := os.Getenv("FAAS_ARTIFACT_REPLICATOR"); helper != "" {
		// The command helper copies a host-local apps/<slug>/<deployment>.ext4
		// file to the control plane. OCI storage is already the shared artifact
		// store, and its published layer has no required path under
		// FAAS_APPS_ROOT. Reject the mixed configuration at boot instead of
		// allowing every deployment to build successfully and fail during the
		// later snapshot handoff with "layer not found".
		if envOr("FAAS_STORAGE_BACKEND", "local") == "oci" {
			return fmt.Errorf("imaged: FAAS_ARTIFACT_REPLICATOR is incompatible with FAAS_STORAGE_BACKEND=oci; unset the replicator for OCI-backed deployments")
		}
		if !filepath.IsAbs(helper) {
			return fmt.Errorf("imaged: FAAS_ARTIFACT_REPLICATOR=%q must be absolute", helper)
		}
		st, statErr := os.Stat(helper)
		if statErr != nil {
			return fmt.Errorf("imaged: FAAS_ARTIFACT_REPLICATOR=%q: %w", helper, statErr)
		}
		if st.IsDir() {
			return fmt.Errorf("imaged: FAAS_ARTIFACT_REPLICATOR=%q is a directory", helper)
		}
		artifactReplicator = imaged.CommandArtifactReplicator{Path: helper}
		log.Info("imaged: split-box artifact replicator enabled", "helper", helper)
	}

	// ADR-053: imaged asks vmmd to mount parent ext4 layers. In a split-box
	// deployment vmmd serves TCP with mTLS rather than the legacy local Unix
	// socket, so load the optional client cluster before constructing the
	// lazy client. All three paths empty preserves single-box behaviour.
	vmmTLS, err := wire.LoadClientTLSConfigWithPrefix(
		"vmm_",
		envOr("FAAS_VMM_TLS_CERT_PATH", ""),
		envOr("FAAS_VMM_TLS_KEY_PATH", ""),
		envOr("FAAS_VMM_TLS_CA_PATH", ""),
	)
	if err != nil {
		return fmt.Errorf("imaged: load vmmd client TLS: %w", err)
	}
	vmmTarget := envOr("FAAS_VMM_SOCK", imaged.DefaultVMMSock)
	if vmmTLS != nil {
		log.Info("imaged: vmmd mTLS client configured", "target", vmmTarget)
	}

	h := imaged.New(store, notifier, puller, builder, guestInitPath, appsRoot, log).
		WithNodeName(os.Getenv("FAAS_NODE_NAME")).
		WithStorage(storageBackend).
		WithRuntimeBaseStaging().
		WithBaseArtifactValidator(imaged.ValidateBaseArtifact).
		WithArtifactReplicator(artifactReplicator).
		WithOpsMetrics(ops).
		// Issue #472 / ADR-054: per-app cosign signature-enforcement
		// at deploy time. Default off (the apps.require_signed=false
		// default means the open-deploy posture stays in place).
		// Operators populate /etc/faas/secrets/trusted-publishers/
		// and set FAAS_TRUSTED_PUBLISHERS_DIR to enable. apid emits
		// pg_notify('trusted_signer_changed') on every CRUD op, and
		// imaged's HandleNotification refreshes the in-memory cache.
		WithTrustedPublishersDir(os.Getenv("FAAS_TRUSTED_PUBLISHERS_DIR")).
		// Issue #299 / ADR-038 Phase 3 + Tier-2 ship blocker.
		// Wire the production runners explicitly so a misconfigured
		// imaged (e.g. an ansible role that forgets to install
		// grype/syft) is observable at startup rather than silently
		// falling through to a nil-runner scan/sbom that emits a
		// fail-closed CRITICAL=9999 placeholder on every build.
		// Without these wires the supply-chain gate at
		// pkg/fcvm/manager.go::bringUpScanCheck would refuse to boot
		// every staged ext4, masquerading the misconfig as a
		// "scan-critical" failure rather than the supply-chain
		// installation gap it actually is.
		WithGrypeRun(makeGrypeRunner(os.Getenv("FAAS_GRYPE_BIN"))).
		WithSyftRun(makeSyftRunner(os.Getenv("FAAS_SYFT_BIN"))).
		// PR-A: layer-side secret-scan walker. Wired
		// unconditionally so the default in
		// pkg/imaged/secretscan.go::runDeployLayerSecretScan
		// fires — the same engine cmd/apid
		// (cmd/apid/secretscan.go::scanExtractedTreeSecrets)
		// uses, so the apid source-tree path and the imaged
		// image-layer path agree on patterns + severities.
		// Loud-fail on findings — see handler.go runDeployLayerSecretScan.
		WithSecretScanRun(makeSecretScanRunner()).
		// ADR-053: imaged asks vmmd to loopback-mount the parent
		// ext4 read-only for the parent-ref staging path. The
		// client is constructed eagerly but the gRPC conn is lazy
		// (first MountParentExt4ReadOnly call dials) so a vmmd
		// restart doesn't delay imaged startup. Default target
		// matches /run/faas/vmmd.sock (ADR-015); operators can
		// override with FAAS_VMM_SOCK for dev (e.g. a bufconn
		// test on a Mac).
		WithVMMClient(imaged.NewVMMClientWithTLS(vmmTarget, vmmTLS, log))

	// Issue #461 / ADR-062: load the host age identity so imaged
	// can transiently unseal per-app private-registry Basic Auth
	// passwords in the pull path. Same FAAS_HOST_AGE_IDENTITY_PATH
	// env var apid loads for MFA — they're the SAME key file
	// (vmmd writes it on wake). The identity stays in-process; we
	// never log it or write it to disk. Required only when an
	// app has a private-registry credential stored; with no
	// identity wired, the registry credential lookup is skipped
	// and pulls stay anonymous (matches Free plan + no-cred
	// Hobby paths).
	if identityPath := envOr("FAAS_HOST_AGE_IDENTITY_PATH", ""); identityPath != "" {
		ident, err := secretbox.LoadHostKey(identityPath)
		if err != nil {
			return fmt.Errorf("imaged: load host age identity %q: %w", identityPath, err)
		}
		h.WithSecretboxIdentity(ident)
		log.Info("host age identity loaded for registry credential unseal",
			"path", identityPath)
	} else {
		log.Warn("FAAS_HOST_AGE_IDENTITY_PATH unset — registry credential unseal disabled (Free plan + anonymous-only deployments)")
	}

	// F3: function runner wiring. cmd/imaged refuses to come up if either
	// env var is set but the path doesn't exist on disk — silent omission
	// was the M6 bug (a function deploy would build a layer without
	// /usr/local/bin/faas-runner and FAILED on first wake).
	for _, kw := range []struct {
		envKey, runtime string
		apply           func(string)
	}{
		{"FAAS_FUNCTION_RUNNER_NODE22", imaged.RuntimeNode22, func(p string) { h.WithFunctionRunnerNode22(p) }},
		{"FAAS_FUNCTION_RUNNER_PYTHON312", imaged.RuntimePython312, func(p string) { h.WithFunctionRunnerPython312(p) }},
		{"FAAS_FUNCTION_RUNNER_GO124", imaged.RuntimeGo124, func(p string) { h.WithFunctionRunnerGo124(p) }},
		{"FAAS_FUNCTION_RUNNER_GO124_ALPINE", imaged.RuntimeGo124Alpine, func(p string) { h.WithFunctionRunnerGo124Alpine(p) }},
		{"FAAS_FUNCTION_RUNNER_NODE24", imaged.RuntimeNode24, func(p string) { h.WithFunctionRunnerNode24(p) }},
		{"FAAS_FUNCTION_RUNNER_PYTHON313", imaged.RuntimePython313, func(p string) { h.WithFunctionRunnerPython313(p) }},
	} {
		p := os.Getenv(kw.envKey)
		if p == "" {
			continue
		}
		if _, err := os.Stat(p); err != nil {
			return fmt.Errorf("imaged: %s=%q: %w", kw.envKey, p, err)
		}
		kw.apply(p)
		log.Info("imaged: function runner wired", "runtime", kw.runtime, "path", p)
	}

	// Production base selection is per-runtime and comes from the
	// FAAS_DEPLOY_BASE_REF_<RUNTIME> environment contract. The old global
	// FAAS_DEPLOY_BASE_REF could make builderd and imaged select different
	// layers, so fail closed if it is still present. Only the e2e harness may
	// use the explicitly test-only global redirect to a local registry.
	if dbr := os.Getenv("FAAS_DEPLOY_BASE_REF"); dbr != "" {
		return fmt.Errorf("imaged: FAAS_DEPLOY_BASE_REF is retired; set FAAS_DEPLOY_BASE_REF_<RUNTIME> instead")
	}
	if dbr := os.Getenv("FAAS_TEST_DEPLOY_BASE_REF"); dbr != "" {
		if node := strings.TrimSpace(os.Getenv("FAAS_NODE_NAME")); node != "" {
			return fmt.Errorf("imaged: FAAS_TEST_DEPLOY_BASE_REF is test-only and cannot be set on named node %q", node)
		}
		h.WithDeployBaseRef(dbr)
		log.Info("imaged: test deploy base ref override", "ref", dbr)
	}

	// F1 + F2: stage the builder-base ext4 on startup, then hand off to the
	// M8 loop which drives the LISTEN subscriber + nightly GC + one-shot FC
	// sweep. The stage is still required for cold-boot of builder microVMs
	// (see spec §4.6 two-drive scheme).
	baseRef, err := builderBaseRefFromEnv()
	if err != nil {
		return err
	}
	basePath := envOr("FAAS_BUILDER_BASE_PATH", "/srv/fc/base/builder-base.ext4")
	// #96 / ADR-025 axis 2: EnsureBaseExt4 publishes via the StorageBackend
	// under sched.BaseKeyForArch / sched.BaseDigestKeyForArch, partitioned
	// by the imaged binary's host arch (issue #197 B3.3). basePath is kept
	// as a resolution target (LocalStorageBackend joins it under
	// FAAS_STORAGE_ROOT) for one release — the migration slice flips to
	// key-only.
	arch := imaged.BuilderArch()
	baseKey := sched.BaseKeyForArch("builder", arch)
	digestKey := sched.BaseDigestKeyForArch("builder", arch)
	baseRes, err := h.EnsureBaseExt4(ctx, baseRef, baseKey, digestKey, basePath, "", "")
	if err != nil {
		return fmt.Errorf("imaged: stage builder base %s → %s: %w", baseRef, basePath, err)
	}

	loop := imaged.NewLoop(imaged.LoopConfig{
		Handler:   h,
		Store:     store,
		Pool:      pool,
		Log:       log,
		Now:       d.now,
		LvUsedPct: d.lvUsedPct,
		DetectFC:  d.detectFC,
		AppsRoot:  appsRoot,
		GCEvery:   envDuration("FAAS_GC_INTERVAL", 24*time.Hour),
		// PR-B: builderd owns the build-queue durability surface now;
		// imaged no longer runs a reaper tick or subscribes to
		// NotifyBuildQueued. FAAS_REAP_INTERVAL +
		// FAAS_REAP_THRESHOLD env vars retired.
	})

	log.Info("imaged ready",
		"min_layer_mb", rootfs.MinLayerMB,
		"arch", arch,
		"builder_base_path", basePath,
		"builder_base_ref", baseRef,
		"builder_base_digest", baseRes.ConfigDigest,
		"builder_base_skipped", baseRes.Skipped,
		"runtime_bases", "on-demand",
	)

	// Optional /metrics listener (this PR). Mirrors cmd/apid/main.go
	// and cmd/builderd/main.go:146-157 — separate bind so a port
	// collision can't take the daemon down. Defaults to 127.0.0.1:9102
	// so an operator typo (or a missing env var in prod) can't accidentally
	// expose the internal registry to the public network — series like
	// imaged_oci_pull_duration_seconds{op,result} leak per-deploy timing
	// shape (review finding #1 on PR #132). Loopback bind is safe because
	// the local Prometheus scrapes from the box itself.
	//
	// Disable semantic (ADR-122 follow-on): set `metrics_addr = ""` in
	// /etc/faas/imaged.toml to disable the listener. The env overlay
	// (FAAS_IMAGED_METRICS_ADDR) does NOT disable when set to empty
	// string — both unset and empty env fall through to the TOML
	// default via GetMetricsAddr's `v != ""` gate (same conflation
	// the legacy envOr had). The legacy behaviour was already broken
	// in this respect; ADR-122 propagated it rather than fixing it.
	// Future improvement: distinguish "unset" from "explicit empty"
	// in the env overlay (e.g. via os.LookupEnv). Out of scope for
	// this PR.
	//
	// Bind target resolves via cfg.GetMetricsAddr: env wins when
	// non-empty, else TOML metrics_addr, else the default in
	// cmd/imaged/config.go::LoadConfig.
	metricsAddr := imgCfg.GetMetricsAddr(os.Getenv)
	if metricsAddr != "" {
		// Issue #571 PR-A2: /readyz probe (storage root +
		// cache dir writability). Built before the metrics
		// listener so the ControlMuxLite registration below
		// can wire /readyz on the same mux as /metrics. defer
		// stop so the SIGTERM drain window surfaces in
		// daemon_ready as 0.
		imagedProbe := BuildReadinessProbe(envOr("FAAS_STORAGE_ROOT", defaultStorageRoot))
		imagedProbe.SetReadyObserver(func(ready bool, reason string) {
			ops.MarkReady("imaged", ready, reason)
		})
		mux := http.NewServeMux()
		mux.Handle("/metrics", ops.Handler())
		wire.ControlMuxLite(mux, imagedProbe.ReadyFunc(), imagedProbe.ReasonFunc())
		// ADR-122: apply the canonical metrics-listener shape —
		// RT/WT/IT/MHB from cfg.MetricsListener (cfg → constant
		// fallback). ReadHeaderTimeout=10s stays from before ADR-122.
		readTimeout, writeTimeout, idleTimeout, maxHeaderBytes := imgCfg.MetricsListener()
		msrv := &http.Server{
			Addr:              metricsAddr,
			Handler:           mux,
			ReadHeaderTimeout: 10 * time.Second,
			ReadTimeout:       readTimeout,
			WriteTimeout:      writeTimeout,
			IdleTimeout:       idleTimeout,
			MaxHeaderBytes:    int(maxHeaderBytes),
		}
		mlis, err := net.Listen("tcp", metricsAddr)
		if err != nil {
			return fmt.Errorf("imaged: metrics listen %q: %w", metricsAddr, err)
		}
		go func() {
			log.Info("imaged /metrics listening", "addr", metricsAddr)
			if err := msrv.Serve(mlis); err != nil && err != http.ErrServerClosed {
				log.Error("imaged /metrics serve", "err", err)
			}
		}()
		//nolint:contextcheck // shutdown ctx must outlive request ctx.
		defer func() {
			shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_ = msrv.Shutdown(shutdownCtx)
		}()
	}

	// ADR-053: release the vmmd gRPC conn on shutdown so SIGTERM
	// doesn't leak the dial. Idempotent on a nil receiver; the
	// VMMClient's Close is also nil-safe.
	defer func() {
		if err := h.CloseVMMClient(); err != nil {
			log.Warn("imaged: close vmm client", "err", err)
		}
	}()

	return loop.Run(ctx)
}

// dbNotifier adapts *pgxpool.Pool to imaged.Notifier by closing over the pool
// and delegating to db.Notify. Kept private here so pkg/imaged stays free of
// pgxpool imports.
type dbNotifier struct{ pool *pgxpool.Pool }

func (d dbNotifier) Notify(ctx context.Context, channel, payload string) error {
	if err := db.Notify(ctx, d.pool, channel, payload); err != nil {
		// A failed notification here is a soft error: the deployment row
		// is still authoritative. imaged logs the original event; the
		// notification is best-effort fan-out.
		return errors.New("imaged: notifier: " + err.Error())
	}
	return nil
}

// envOr returns the value of env key, or fallback when unset/empty.
func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// builderBaseRefFromEnv resolves the builder image reference. Single-box
// development keeps the historical latest default; a named multi-box host
// must receive an explicit digest-pinned ref from deployment configuration.
// This prevents a public compute node from silently changing its builder
// rootfs when GHCR's mutable latest tag advances.
func builderBaseRefFromEnv() (string, error) {
	v := os.Getenv("FAAS_BUILDER_BASE_REF")
	if v == "" {
		if os.Getenv("FAAS_NODE_NAME") != "" {
			return "", errors.New("imaged: FAAS_BUILDER_BASE_REF is required and must be digest-pinned on a named multi-box host")
		}
		return imaged.BaseRefBuilder, nil
	}
	ref, err := oci.ParseReference(v)
	if err != nil || ref.Digest == "" {
		return "", fmt.Errorf("imaged: FAAS_BUILDER_BASE_REF %q must be a digest-pinned reference (e.g. registry.gregale.dev/img@sha256:...)", v)
	}
	return v, nil
}

const (
	canonicalGuestInitPath = "/opt/faas/current/bin/init"
	legacyGuestInitPath    = "/usr/local/bin/faas-guest-init"
)

// guestInitPathFromEnv resolves the boot-critical PID 1 binary. Older
// single-box units defaulted to ./init, which depends on an implicit working
// directory and silently left a stale guest-init inside an already-published
// runtime base when the checkout was not present there. Prefer the paths used
// by release installs, then keep the local checkout path as a development
// fallback.
//
// A historical production override pointed FAAS_GUEST_INIT at
// /usr/local/bin/faas-guest-init. That path is not release-managed and can
// survive an otherwise successful daemon rollout, which makes the base image
// embed an old PID 1. Once the canonical release binary exists, ignore that
// one legacy override so a base refresh cannot silently preserve stale guest
// code. Other explicit paths remain supported for local development.
func guestInitPathFromEnv() string {
	explicit := os.Getenv("FAAS_GUEST_INIT")
	return resolveGuestInitPath(explicit, []string{
		canonicalGuestInitPath,
		legacyGuestInitPath,
		"./init",
	}, func(path string) bool {
		_, err := os.Stat(path)
		return err == nil
	})
}

func resolveGuestInitPath(explicit string, candidates []string, exists func(string) bool) string {
	if explicit != "" {
		if explicit != legacyGuestInitPath || !exists(canonicalGuestInitPath) {
			return explicit
		}
	}
	for _, candidate := range candidates {
		if exists(candidate) {
			return candidate
		}
	}
	// Return the release path even when the install is incomplete so the
	// subsequent base-build error names the missing boot contract directly.
	return canonicalGuestInitPath
}

// envDuration parses a duration env var, returning fallback on parse error
// or empty string. Used for the GC tick override (FAAS_GC_INTERVAL).
func envDuration(key string, fallback time.Duration) time.Duration {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		return fallback
	}
	return d
}

// ociPullTimeout returns the per-pull HTTP timeout for the OCI puller.
// The platform default lives at api.OCIPullTimeoutSeconds (currently 60s);
// operators may override on the daemon with FAAS_OCI_PULL_TIMEOUT_SECONDS.
// A non-positive or unparseable override falls back to the platform
// default — silent adoption of a garbage value would manifest as a wake
// that never returns.
func ociPullTimeout() time.Duration {
	v := os.Getenv("FAAS_OCI_PULL_TIMEOUT_SECONDS")
	if v == "" {
		return time.Duration(api.OCIPullTimeoutSeconds) * time.Second
	}
	secs, err := strconv.Atoi(v)
	if err != nil || secs <= 0 {
		return time.Duration(api.OCIPullTimeoutSeconds) * time.Second
	}
	return time.Duration(secs) * time.Second
}

// makeGrypeRunner wires an explicit Grype subprocess runner bound
// to the operator-supplied binary path (issue #299 / ADR-055
// PR-2). The default empty string means "use PATH lookup",
// matching the production behaviour pkg/imaged/grype.go's
// defaultGrypeRun implements natively; cmd/imaged calls
// WithGrypeRun regardless so the supply-chain gate at
// pkg/fcvm/manager.go::bringUpScanCheck reports the real install
// location rather than a nil-stub placeholder. The closure
// shape mirrors defaultGrypeRun — see that function for the
// JSON parse contract. The PR-2 refactor changed the return
// type from map[string]int to *imaged.ScanResult; the
// underlying subprocess invocation is unchanged.
func makeGrypeRunner(bin string) func(ctx context.Context, dir string) (*imaged.ScanResult, error) {
	return func(ctx context.Context, dir string) (*imaged.ScanResult, error) {
		if bin != "" {
			return imaged.RunGrypeAt(ctx, bin, dir)
		}
		return imaged.RunGrype(ctx, dir)
	}
}

// makeSyftRunner wires an explicit Syft subprocess runner bound
// to the operator-supplied binary path (issue #299 / ADR-038
// Phase 3). As with makeGrypeRunner, the empty-string path means
// "use PATH lookup"; cmd/imaged calls WithSyftRun regardless so
// the SBOM populator at pkg/imaged/sbom.go::writeBuildSBOM emits
// real CycloneDX on every build rather than persisting nothing.
func makeSyftRunner(bin string) func(ctx context.Context, dir string) ([]byte, error) {
	return func(ctx context.Context, dir string) ([]byte, error) {
		if bin != "" {
			return imaged.RunSyftAt(ctx, bin, dir)
		}
		return imaged.RunSyft(ctx, dir)
	}
}

// makeSecretScanRunner wires the package-level default walker
// (PR-A, imaged-layer secret scan). No subprocess binary to
// configure — the walker is a pure-Go filepath.WalkDir + secretscan.ScanFile
// caller (mirrors cmd/apid::scanExtractedTreeSecrets). The harness
// exists for symmetry with makeGrypeRunner / makeSyftRunner so a
// future operator override (e.g. a `FAAS_SECRETSCAN_BIN` shim
// against a custom scanner) can land here without churning the
// WithSecretScanRun wiring in main().
func makeSecretScanRunner() func(ctx context.Context, dir, layer string) ([]secretscan.Finding, error) {
	return imaged.RunDeployLayerSecretScan
}

// reconcileManifestBuilderBase is the PR-5 / issue #911 manifest
// reconcile step. It runs before the openDB call so a manifest
// mismatch fails the boot BEFORE we spend a Postgres connection.
//
// Behaviour:
//   - FAAS_MANIFEST_PATH unset (single-box dev today): no-op.
//   - FAAS_MANIFEST_PATH set but file unreadable / invalid: fatal
//     (load-failure surfaces the same `gregale manifest validate`
//     path operators already use).
//   - manifest.release.builder_base_digest empty: no-op (the
//     manifest does not pin a builder base — env-only mode).
//   - FAAS_BUILDER_BASE_REF resolves to a digest that does NOT
//     match the manifest's pinned digest: fatal with a message
//     pointing at `gregale manifest validate`.
//
// Single-box installs without a manifest keep today's env-only
// behaviour; split-box installs (post PR-2 renderer + PR-X secrets
// init) carry the manifest and benefit from the gate.
func reconcileManifestBuilderBase() error {
	manifestPath := os.Getenv("FAAS_MANIFEST_PATH")
	if manifestPath == "" {
		return nil
	}
	m, err := manifest.Load(manifestPath)
	if err != nil {
		return fmt.Errorf("imaged: reconcile manifest %s: %w (run `gregale manifest validate --file=%s` to inspect)",
			manifestPath, err, manifestPath)
	}
	pinned := m.Release.BuilderBaseDigest
	if pinned == "" {
		// Manifest does not pin a builder base digest — operator
		// opted out of the contract. Env-only behaviour applies.
		return nil
	}
	envRef := os.Getenv("FAAS_BUILDER_BASE_REF")
	if envRef == "" {
		// No env override — imaged will fall through to
		// imaged.BaseRefBuilder (the package default). The
		// reconcile still pins the manifest's digest; the gate
		// below fires only when an operator overrode the env.
		return nil
	}
	parsed, err := oci.ParseReference(envRef)
	if err != nil {
		return fmt.Errorf("imaged: reconcile manifest: FAAS_BUILDER_BASE_REF %q: %w", envRef, err)
	}
	if parsed.Digest == "" {
		return fmt.Errorf("imaged: reconcile manifest: FAAS_BUILDER_BASE_REF %q must be a digest-pinned reference (manifest pins %s)",
			envRef, pinned)
	}
	// OCI references carry the algorithm prefix (`sha256:<hex>`), while
	// production manifests historically store the same digest as raw
	// 64-character hex. Compare the canonical hex payload so both wire
	// forms describe the same immutable builder image.
	if digestHex(parsed.Digest) != digestHex(pinned) {
		return fmt.Errorf("imaged: reconcile manifest: FAAS_BUILDER_BASE_REF digest %q does not match manifest release.builder_base_digest %q (run `gregale manifest validate --file=%s` to inspect)",
			parsed.Digest, pinned, manifestPath)
	}
	return nil
}

func digestHex(digest string) string {
	return strings.TrimPrefix(strings.TrimSpace(digest), "sha256:")
}
