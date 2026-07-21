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
	"net/http"
	"os"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/imaged"
	"github.com/onebox-faas/faas/pkg/oci"
	"github.com/onebox-faas/faas/pkg/rootfs"
	"github.com/onebox-faas/faas/pkg/state"
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
	pullerOpts := []oci.Option{oci.WithHTTPClient(oci.NewEgressHTTPClient())}
	if os.Getenv("FAAS_OCI_INSECURE") == "1" {
		log.Warn("FAAS_OCI_INSECURE=1 — egress guard disabled, e2e test mode only")
		pullerOpts = []oci.Option{
			oci.WithHTTPClient(&http.Client{}),
			oci.WithEndpoint("http", ""),
		}
	}
	puller := oci.NewRegistryClient(pullerOpts...)

	notifier := dbNotifier{pool: pool}
	guestInitPath := envOr("FAAS_GUEST_INIT", "./init")
	appsRoot := envOr("FAAS_APPS_ROOT", "/var/lib/faas/apps")
	h := imaged.New(store, notifier, puller, builder, guestInitPath, appsRoot, log)

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
	// aboveBaseLayers at deploy time. Test-only.
	if dbr := os.Getenv("FAAS_DEPLOY_BASE_REF"); dbr != "" {
		h.WithDeployBaseRef(dbr)
	}

	// F1 + F2: stage the builder-base ext4 on startup, then hand off to the
	// M8 loop which drives the LISTEN subscriber + nightly GC + one-shot FC
	// sweep. The stage is still required for cold-boot of builder microVMs
	// (see spec §4.6 two-drive scheme).
	baseRef := envOr("FAAS_BUILDER_BASE_REF", imaged.BaseRefBuilder)
	basePath := envOr("FAAS_BUILDER_BASE_PATH", "/srv/fc/base/builder-base.ext4")
	baseRes, err := h.EnsureBaseExt4(ctx, baseRef, basePath)
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
	})

	log.Info("imaged ready",
		"min_layer_mb", rootfs.MinLayerMB,
		"builder_base_path", basePath,
		"builder_base_ref", baseRef,
		"builder_base_digest", baseRes.ConfigDigest,
		"builder_base_skipped", baseRes.Skipped,
	)
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
