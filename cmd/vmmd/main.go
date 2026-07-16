// Command vmmd — microVM supervisor: firecracker + jailer, the only root
// component (spec §4.4).
//
// vmmd owns everything that touches /usr/bin/firecracker and the jailer. It is
// the sole root-privileged daemon; per-VM work drops to the jailer immediately.
// Do not add a path that lets another component touch firecracker directly
// (spec §Component ownership). The gRPC control surface (CreateFromSnapshot,
// CreateColdBoot, Pause+Snapshot, Destroy, Stats) lands next in M1.
package main

import (
	"context"
	"log/slog"
	"time"

	"github.com/onebox-faas/faas/pkg/fcvm"
	"github.com/onebox-faas/faas/pkg/wire"
)

func main() {
	wire.Daemon("vmmd", run)
}

func run(ctx context.Context, log *slog.Logger) error {
	// Snapshots are pinned to the running Firecracker version (ADR-005); detect it
	// so restore only loads compatible snapshots and everything else cold boots.
	fcVersion, err := fcvm.DetectFirecrackerVersion(ctx)
	if err != nil {
		log.Warn("could not detect firecracker version; treating all snapshots as stale", "err", err)
	}

	// Production wiring: real command runner + jailer-backed VMM.
	mgr := fcvm.NewManager(
		wire.ExecRunner{},
		fcvm.NewJailerVMM(fcvm.JailChrootBase, 30*time.Second),
		fcvm.Paths{Kernel: "/srv/fc/base/vmlinux-6.1"},
		fcVersion,
		log,
	)
	log.Info("vmmd ready", "fc_version", fcVersion, "max_slots", fcvm.MaxSlots,
		"uid_lo", fcvm.JailUIDBase, "uid_hi", fcvm.JailUIDMax)

	// Until the gRPC control surface is wired, run a heartbeat that surfaces live
	// instance / lease counts (the leak signal — both must be 0 when idle).
	tick := time.NewTicker(30 * time.Second)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			log.Info("draining", "live", mgr.LiveCount())
			return nil
		case <-tick.C:
			log.Debug("heartbeat", "live", mgr.LiveCount(), "leased", mgr.LeasedCount())
		}
	}
}
