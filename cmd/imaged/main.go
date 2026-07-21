// Command imaged — image and snapshot service (spec §4.6).
//
// imaged owns OCI→bootable-rootfs conversion (the two-drive scheme), base/runner
// images, and snapshot GC. It turns the layers ABOVE a shared base into a per-app
// ext4 app layer, injects guest-init + the app.json contract, and enforces the
// plan's app-layer cap. Never flatten to one rootfs per app (spec §4.6). Snapshot
// GC + the fleet-size metrics land later in M2/M3.
//
// M5: imaged is a real consumer of apid's pg_notify events. It subscribes to
// `deployment_changed` (image: digest) and `build_queued` (tarball/dockerfile),
// advances the deployment row through every status transition, and writes the
// snapshots row that schedd uses to restore instances on wake (ADR-005).
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

func run(ctx context.Context, log *slog.Logger) error {
	pool, err := db.Open(ctx, "")
	if err != nil {
		return err
	}
	defer pool.Close()
	if err := db.MigrateUp(ctx, pool); err != nil {
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
	// serves plain HTTP, not HTTPS). WithEndpoint("http", "") sets the
	// scheme while leaving the per-reference host derivation intact (the
	// puller falls back to r.APIHost() when its pinned host is empty).
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

	// FAAS_DEPLOY_BASE_REF overrides the per-runtime base ref used by
	// aboveBaseLayers at deploy time. Wired from the test harness via the
	// same FAAS_TEST_BUILDER_BASE_REF it already uses for startup-time
	// staging — production never sets this (see Handler.WithDeployBaseRef).
	if dbr := os.Getenv("FAAS_DEPLOY_BASE_REF"); dbr != "" {
		h.WithDeployBaseRef(dbr)
	}

	channels := []string{
		db.NotifyDeploymentChanged,
		db.NotifyBuildQueued,
		db.NotifySnapshotWritten,
	}
	notif, cancel, err := db.Subscribe(ctx, pool, channels)
	if err != nil {
		return err
	}
	defer cancel()

	// M6 closure: stage the builder-base ext4 on startup so cold-boot can
	// pass it as drive0 (spec §4.6 two-drive). Pull the configured base ref
	// (or the pinned default), assemble all layers into the shared read-only
	// ext4 at FAAS_BUILDER_BASE_PATH. Idempotent across restarts — see
	// imaged.EnsureBaseExt4 for the digest-pinned skip path. A failure here
	// refuses to come up: a half-built base would mask every later builder
	// crash as a "base missing" error and make root-causing painful.
	baseRef := envOr("FAAS_BUILDER_BASE_REF", imaged.BaseRefBuilder)
	basePath := envOr("FAAS_BUILDER_BASE_PATH", "/srv/fc/base/builder-base.ext4")
	baseRes, err := h.EnsureBaseExt4(ctx, baseRef, basePath)
	if err != nil {
		return fmt.Errorf("imaged: stage builder base %s → %s: %w", baseRef, basePath, err)
	}

	log.Info("imaged ready",
		"min_layer_mb", rootfs.MinLayerMB,
		"builder_base_path", basePath,
		"builder_base_ref", baseRef,
		"builder_base_digest", baseRes.ConfigDigest,
		"builder_base_skipped", baseRes.Skipped,
		"channels", channels)

	for {
		select {
		case <-ctx.Done():
			return nil
		case n, ok := <-notif:
			if !ok {
				return nil
			}
			h.HandleNotification(ctx, n)
		}
	}
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

// silence unused-import in builds where rootfs isn't referenced yet.
var _ = time.Now

// envOr returns the value of env key, or fallback when unset/empty.
func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
