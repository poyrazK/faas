package main

// live_vm_reap.go — destroy the VMs this vmmd still runs for an instance whose
// row is no longer live (ADR-191 amendment 2026-10-12; production-us hunt #9,
// H9-171).
//
// ADR-191 split instance/VM divergence in two: schedd reconciles a live row
// whose VM is gone, and vmmd reaps VMs without a live row, but only at boot.
// While vmmd is up, a VM outlives its row whenever schedd parks the row and no
// destroy reaches vmmd. The recovery arbiter does exactly that when a node is
// only slow to answer: production-us kept 53 VMs (5.5 GB) running behind
// parked rows, invisible to admission.
//
// The sweep acts only on what is unambiguous:
//   - ordinary app VMs (Manager.AppInstanceIDs);
//   - whose row exists and is not live. A missing row is not evidence, so
//     those VMs are left to the boot reap;
//   - seen that way on two consecutive sweeps, so a row parked a moment
//     before its own destroy lands is never raced.

import (
	"context"
	"log/slog"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
)

const liveVMReapInterval = time.Minute

type liveVMSource interface {
	AppInstanceIDs() []string
	Destroy(ctx context.Context, instance string) error
}

type liveVMReaper struct {
	vms      liveVMSource
	rows     instanceReader
	log      *slog.Logger
	suspects map[string]bool
}

func runLiveVMReap(ctx context.Context, log *slog.Logger, vms liveVMSource, rows instanceReader) {
	r := &liveVMReaper{vms: vms, rows: rows, log: log}
	tick := time.NewTicker(liveVMReapInterval)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			r.sweep(ctx)
		}
	}
}

// sweep destroys the VMs whose row was not live on this and the previous
// sweep, and returns how many it destroyed.
func (r *liveVMReaper) sweep(ctx context.Context) int {
	seen := map[string]bool{}
	destroyed := 0
	for _, id := range r.vms.AppInstanceIDs() {
		ins, err := r.rows.InstanceByID(ctx, id)
		if err != nil || state.IsLive(ins.State) {
			continue
		}
		if !r.suspects[id] {
			seen[id] = true
			continue
		}
		if err := r.vms.Destroy(ctx, id); err != nil {
			seen[id] = true
			r.log.Warn("vmmd: live VM reap: destroy failed", "instance", id, "row_state", ins.State, "err", err)
			continue
		}
		destroyed++
		r.log.Warn("vmmd: live VM reap: destroyed a VM whose instance row is not live",
			"instance", id, "app_id", ins.AppID, "row_state", ins.State)
	}
	r.suspects = seen
	return destroyed
}
