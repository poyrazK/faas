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
	"log/slog"
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

	// Real registry v2 puller (M6 groundwork, gap G1): resolves an image deploy's
	// digest-pinned reference against the public registry. Egress hardening
	// (deny RFC1918 / metadata ranges, spec §11) is a follow-up — inject a
	// policy-aware *http.Client via oci.WithHTTPClient when it lands.
	puller := oci.NewRegistryClient()

	notifier := dbNotifier{pool: pool}
	guestInitPath := envOr("FAAS_GUEST_INIT", "./init")
	appsRoot := envOr("FAAS_APPS_ROOT", "/var/lib/faas/apps")
	h := imaged.New(store, notifier, puller, builder, guestInitPath, appsRoot, log)

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

	log.Info("imaged ready",
		"min_layer_mb", rootfs.MinLayerMB,
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
