package sched

import (
	"context"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// minFrozenCompressFreedBytes is the least a compression must free to keep
// a frozen VM; below it, or below half the VM's resident memory, the VM
// parks as before.
const minFrozenCompressFreedBytes = 16 << 20

// EvictUnderPressure is Evict for RAM pressure. A frozen instance is first
// compressed into memory-only swap (ADR-906 decision 6): when that frees at
// least half its memory it stays frozen and thawable, otherwise it parks.
// Any other instance is evicted as by Evict.
func (e *Engine) EvictUnderPressure(ctx context.Context, instanceID string) error {
	frozen, err := e.lockedInStates(ctx, instanceID, state.StateFrozen)
	if err != nil {
		return err
	}
	if frozen == nil {
		return e.Evict(ctx, instanceID)
	}
	defer e.unlockApp(frozen.AppID)
	if !e.compressFrozenLocked(ctx, *frozen) {
		e.parkFrozenLocked(ctx, *frozen, "memory_pressure")
	}
	return nil
}

// compressFrozenLocked asks vmmd to compress a frozen VM's memory and
// reports whether it freed enough to keep the VM. The reservation then
// books the smaller footprint; the node's next measured report shows the
// memory freed. A VM already compressed frees little the second time, so
// continued pressure parks it. The caller holds the app lock.
func (e *Engine) compressFrozenLocked(ctx context.Context, frozen state.Instance) bool {
	freezer, ok := e.vmm.(FrozenVMM)
	if !ok {
		return false
	}
	rpcCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), frozenRPCTimeout)
	rep, err := freezer.FreezeInstance(rpcCtx, e.nodeForRoute(frozen.NodeID), frozen.ID, true)
	cancel()
	switch {
	case err != nil && problemCode(err) == api.CodeFrozenCompressUnavailable:
		e.observeFrozenCompress("unavailable")
		return false
	case err != nil:
		e.log.Warn("sched: compress frozen instance", "instance", frozen.ID, "err", err)
		e.observeFrozenCompress("error")
		return false
	}
	freed := rep.ResidentBytesBefore - rep.ResidentBytesAfter
	if rep.ResidentBytesBefore <= 0 || freed < minFrozenCompressFreedBytes || freed*2 < rep.ResidentBytesBefore {
		e.observeFrozenCompress("insufficient")
		return false
	}
	e.ledger.ShrinkFrozenCharge(frozen.ID, frozenChargeMB(frozen.RAMMB, float64(rep.ResidentBytesAfter)/(1<<20)))
	e.observeFrozenCompress("compressed")
	e.log.Info("sched: compressed frozen instance under memory pressure", "instance", frozen.ID,
		"before_bytes", rep.ResidentBytesBefore, "after_bytes", rep.ResidentBytesAfter)
	return true
}

func (e *Engine) observeFrozenCompress(outcome string) {
	if e != nil && e.ops != nil {
		e.ops.FrozenCompressTotal(outcome).Inc()
	}
}
