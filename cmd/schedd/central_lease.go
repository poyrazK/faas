package main

// Central scheduler lease (ADR-1237 §4).
//
// Every control node runs a central schedd (empty FAAS_NODE_NAME): it places
// unplaced apps, fires cron, sweeps the fleet and owns default-local work.
// Several of those paths assume one process (legacy cron marks a fire with an
// unconditional UPDATE, wake coordination is per process, the dead-node and
// stale-peer verdicts act on the whole fleet). So with three control nodes
// exactly one central schedd may run. Each waits here as a standby until it
// holds the central-scheduler lease, then starts normally under the lease's
// context, and exits with an error when it loses the lease so systemd
// restarts it as a standby.
//
// Compute-node schedds own one node each and are not gated.

import (
	"context"
	"log/slog"
	"strings"

	"github.com/onebox-faas/faas/pkg/singleton"
	"github.com/onebox-faas/faas/pkg/state"
)

const (
	centralSchedulerLeaseName = "central-scheduler"
	// centralSchedulerLeaseEnv set to "0" turns the gate off (a single-box
	// deployment that wants the pre-ADR-1237 start path).
	centralSchedulerLeaseEnv = "FAAS_SCHEDD_CENTRAL_LEASE"
)

func centralSchedulerLeaseEnabled(raw string) bool {
	return strings.TrimSpace(raw) != "0"
}

// acquireCentralSchedulerLease blocks as a standby until this process holds
// the central-scheduler lease. The returned stop function ends the standby's
// systemd watchdog feed; call it once the daemon's own watchdog runs.
func acquireCentralSchedulerLease(ctx context.Context, store state.ControlPlaneLeaseStore, log *slog.Logger) (*singleton.Holding, func(), error) {
	runner, err := singleton.New(store, centralSchedulerLeaseName, singleton.NewHolderID(), singleton.Timings{}, log)
	if err != nil {
		return nil, nil, err
	}
	hold, stop, err := runner.AcquireAsService(ctx)
	if err != nil {
		return nil, nil, err
	}
	log.Info("schedd: central-scheduler lease held; starting", "epoch", hold.Epoch())
	return hold, stop, nil
}
