package main

// meterd lease (ADR-1237 §4).
//
// meterd walks every app each minute, keeps per-process CPU baselines, runs
// the overage-cap and dunning ticks and pushes billing. Two copies would
// double cpu_usec and race the overage cap, so with three control nodes
// exactly one meterd may run. Each waits as a standby until it holds the
// meterd lease, then starts normally under the lease's context, and exits
// with an error when it loses the lease so systemd restarts it as a standby.

import (
	"context"
	"log/slog"
	"strings"

	"github.com/onebox-faas/faas/pkg/singleton"
	"github.com/onebox-faas/faas/pkg/state"
)

const (
	meterdLeaseName = "meterd"
	// meterdLeaseEnv set to "0" turns the gate off.
	meterdLeaseEnv = "FAAS_METERD_LEASE"
)

// meterdLeaseEnabled reads name through getenv; a nil getenv (test wiring)
// means unset, which keeps the gate on.
func meterdLeaseEnabled(getenv func(string) string, name string) bool {
	if getenv == nil {
		return true
	}
	return strings.TrimSpace(getenv(name)) != "0"
}

// acquireMeterdLease blocks as a standby until this process holds the meterd
// lease. The returned stop function ends the standby's systemd watchdog feed.
func acquireMeterdLease(ctx context.Context, store state.ControlPlaneLeaseStore, log *slog.Logger) (*singleton.Holding, func(), error) {
	runner, err := singleton.New(store, meterdLeaseName, singleton.NewHolderID(), singleton.Timings{}, log)
	if err != nil {
		return nil, nil, err
	}
	hold, stop, err := runner.AcquireAsService(ctx)
	if err != nil {
		return nil, nil, err
	}
	log.Info("meterd: meterd lease held; starting", "epoch", hold.Epoch())
	return hold, stop, nil
}
