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
	"strconv"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/capdecl/runtimecheck"
	"github.com/onebox-faas/faas/pkg/cosign"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/imaged"
	"github.com/onebox-faas/faas/pkg/oci"
	"github.com/onebox-faas/faas/pkg/role"
	"github.com/onebox-faas/faas/pkg/rootfs"
	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/secretbox"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/storage"
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
			return db.MigrateUp(ctx, pool)
		},
		lvUsedPct: imaged.DefaultLvFcUsedPct(imaged.LvFcName),
		detectFC:  imaged.DetectFirecrackerVersion,
		now:       time.Now,
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

	// Gate-B box-role gate. imaged is a compute-only daemon — it
	// refuses to start under RoleControlPlane. The role is set
	// from FAAS_IMAGED_ROLE at deploy time (imaged has no
	// config.go today; the env is the only source); default is
	// RoleSingleBox so single-box dev boots unmoved. The gate
	// runs before d.openDB so a misconfigured boot doesn't waste
	// a Postgres connection.
	if err := role.Require("imaged", role.FromConfig("", "FAAS_IMAGED_ROLE"),
		role.RoleSingleBox, role.RoleComputeOnly); err != nil {
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
	guestInitPath := envOr("FAAS_GUEST_INIT", "./init")
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
	h := imaged.New(store, notifier, puller, builder, guestInitPath, appsRoot, log).
		WithStorage(storageBackend).
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
		// ADR-053: imaged asks vmmd to loopback-mount the parent
		// ext4 read-only for the parent-ref staging path. The
		// client is constructed eagerly but the gRPC conn is lazy
		// (first MountParentExt4ReadOnly call dials) so a vmmd
		// restart doesn't delay imaged startup. Default target
		// matches /run/faas/vmmd.sock (ADR-015); operators can
		// override with FAAS_VMM_SOCK for dev (e.g. a bufconn
		// test on a Mac).
		WithVMMClient(imaged.NewVMMClient(envOr("FAAS_VMM_SOCK", imaged.DefaultVMMSock), log))

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

	// FAAS_DEPLOY_BASE_REF overrides the per-runtime base ref used by
	// aboveBaseLayers at deploy time. Operator overrides must be
	// digest-pinned (ADR-021 D4): a tag reference like `:latest` would
	// resolve to whatever the registry serves TODAY, not when a deploy
	// was first queued — and a per-app build that pins against today's
	// `latest` would suddenly change what /srv/fc/base/<runtime>.ext4
	// contains on the next imaged restart, invalidating the per-app
	// diff_ids above. Refusing bare tags here makes the override an
	// explicit, reproducible operator choice.
	if dbr := os.Getenv("FAAS_DEPLOY_BASE_REF"); dbr != "" {
		ref, err := oci.ParseReference(dbr)
		if err != nil || ref.Digest == "" {
			return fmt.Errorf("imaged: FAAS_DEPLOY_BASE_REF %q must be a digest-pinned reference (e.g. registry.gregale.dev/img@sha256:...)", dbr)
		}
		h.WithDeployBaseRef(dbr)
		log.Info("imaged: deploy base ref override", "ref", dbr)
	}

	// F1 + F2: stage the builder-base ext4 on startup, then hand off to the
	// M8 loop which drives the LISTEN subscriber + nightly GC + one-shot FC
	// sweep. The stage is still required for cold-boot of builder microVMs
	// (see spec §4.6 two-drive scheme).
	baseRef := envOr("FAAS_BUILDER_BASE_REF", imaged.BaseRefBuilder)
	if v := os.Getenv("FAAS_BUILDER_BASE_REF"); v != "" {
		// Same digest-pinned gate as the deploy base ref above. The
		// builder base ext4 is shared across every cold-boot and
		// snapshot-prime — flipping it without a digest would corrupt
		// every parked app's restore path.
		ref, err := oci.ParseReference(v)
		if err != nil || ref.Digest == "" {
			return fmt.Errorf("imaged: FAAS_BUILDER_BASE_REF %q must be a digest-pinned reference (e.g. registry.gregale.dev/img@sha256:...)", v)
		}
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

	// PR 2 of Tier 1: auto-stage every runtime base so the operator
	// recipe ("docker build + mkfs.ext4 + scp to /srv/fc/base/...") is
	// replaced by a single seeded table at imaged startup. Mirrors the
	// builder-base loop above; failure aborts imaged — a partial fleet
	// (some runtimes staged, others skipped mid-loop) silently breaks
	// the first wake of the missing runtime, so we fail closed instead.
	baseLoop, err := h.EnsureBases(ctx, arch, imaged.DefaultRuntimeBaseRefs, os.Getenv)
	if err != nil {
		return fmt.Errorf("imaged: stage runtime bases: %w", err)
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
		"runtime_bases", baseLoop,
	)

	// Optional /metrics listener (this PR). Mirrors cmd/apid/main.go
	// and cmd/builderd/main.go:146-157 — separate bind so a port
	// collision can't take the daemon down. Defaults to 127.0.0.1:9102
	// so an operator typo (or a missing env var in prod) can't accidentally
	// expose the internal registry to the public network — series like
	// imaged_oci_pull_duration_seconds{op,result} leak per-deploy timing
	// shape (review finding #1 on PR #132). Loopback bind is safe because
	// the local Prometheus scrapes from the box itself. Set
	// FAAS_IMAGED_METRICS_ADDR= to disable the listener (unit tests that
	// don't want a port reserved).
	metricsAddr := envOr("FAAS_IMAGED_METRICS_ADDR", "127.0.0.1:9102")
	if metricsAddr != "" {
		mux := http.NewServeMux()
		mux.Handle("/metrics", ops.Handler())
		msrv := &http.Server{
			Addr:              metricsAddr,
			Handler:           mux,
			ReadHeaderTimeout: 10 * time.Second,
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
