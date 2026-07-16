// Command imaged — image and snapshot service (spec §4.6).
//
// imaged owns OCI→bootable-rootfs conversion (the two-drive scheme), base/runner
// images, and snapshot GC. It turns the layers ABOVE a shared base into a per-app
// ext4 app layer, injects guest-init + the app.json contract, and enforces the
// plan's app-layer cap. Never flatten to one rootfs per app (spec §4.6). Snapshot
// GC + the fleet-size metrics land later in M2/M3.
package main

import (
	"context"
	"log/slog"

	"github.com/onebox-faas/faas/pkg/rootfs"
	"github.com/onebox-faas/faas/pkg/wire"
)

func main() {
	wire.Daemon("imaged", run)
}

func run(ctx context.Context, log *slog.Logger) error {
	// The app-layer builder uses the shared exec runner for the unprivileged
	// mkfs.ext4 -d step; layer application, injection, and cap enforcement are
	// pure Go.
	builder := rootfs.NewBuilder(wire.ExecRunner{})
	_ = builder // the deploy-pipeline listener that drives it lands in M2/M5.

	log.Info("imaged ready", "min_layer_mb", rootfs.MinLayerMB)
	<-ctx.Done()
	return nil
}
