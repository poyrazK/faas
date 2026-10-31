package sched

// Proactive compression of frozen VMs (ADR 20261031143521).
//
// ADR-906 decision 6 compresses a frozen VM only from EvictUnderPressure,
// when a node's actual memory is over its ceiling. Admission keeps charges
// under the ceiling and a charge is at least the memory it measures, so a
// full node refuses wakes long before that, and nothing is compressed.
// Here the reaper compresses frozen VMs while a node's admission usage is
// near its ceiling, longest frozen first, so the RAM they free goes back to
// admission. A VM frozen only briefly keeps the fast uncompressed thaw.

import (
	"context"
	"sync"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// frozenCompressBackoff remembers until when a node's vmmd answered that it
// cannot compress (no zswap), so it is not asked again meanwhile.
type frozenCompressBackoff struct {
	mu    sync.Mutex
	until map[string]time.Time
}

// EnableFrozenProactiveCompress makes the reaper compress frozen VMs on
// nodes near their RAM ceiling. Nodes without zswap answer unavailable and
// back off, so it does nothing there.
func (e *Engine) EnableFrozenProactiveCompress() {
	if e == nil {
		return
	}
	e.frozenProactiveCompress = true
	if e.compressBackoff == nil {
		e.compressBackoff = &frozenCompressBackoff{until: map[string]time.Time{}}
	}
}

// CompressFrozenForDensity runs one proactive compression pass. On every
// node whose admission usage is at least FrozenCompressHighWaterPercent of
// its ceiling, it compresses frozen VMs frozen at least
// FrozenCompressMinAgeSeconds, longest frozen first, at most
// FrozenCompressPerNodePerTick, until the charges it freed bring the node
// under FrozenCompressLowWaterPercent. Each VM is asked once. An unavailable
// answer backs the node off for FrozenCompressUnavailableBackoffSeconds; no
// outcome parks a VM. Runs on the reaper tick.
func (e *Engine) CompressFrozenForDensity(ctx context.Context, now time.Time) {
	if e == nil || !e.frozenProactiveCompress {
		return
	}
	minAge := time.Duration(api.FrozenCompressMinAgeSeconds) * time.Second
	for nodeID, ids := range e.ledger.FrozenCompressCandidates(now, minAge) {
		if e.frozenCompressBackedOff(nodeID, now) {
			continue
		}
		used, ceiling, ok := e.ledger.FrozenCompressNeed(nodeID)
		if !ok || ceiling <= 0 || used*100 < ceiling*api.FrozenCompressHighWaterPercent {
			continue
		}
		target := ceiling * api.FrozenCompressLowWaterPercent / 100
		for i, id := range ids {
			if i == api.FrozenCompressPerNodePerTick || used < target {
				break
			}
			before, _ := e.ledger.FrozenChargeOf(id)
			outcome := e.compressFrozenProactive(ctx, id)
			if outcome == frozenCompressUnavailable {
				e.backOffFrozenCompress(nodeID, now)
				break
			}
			e.ledger.MarkFrozenCompressTried(id)
			// Under measured admission the node's usage changes only with
			// its next sample, so count what this pass freed.
			if after, ok := e.ledger.FrozenChargeOf(id); ok && after < before {
				used -= before - after
			}
		}
	}
}

// compressFrozenProactive compresses one frozen VM under its app lock. It
// never parks: unlike eviction, nothing needs the memory yet. A VM that is
// no longer frozen reports "gone".
func (e *Engine) compressFrozenProactive(ctx context.Context, instanceID string) string {
	if e.frozenCompressOne != nil {
		return e.frozenCompressOne(ctx, instanceID)
	}
	frozen, err := e.lockedInStates(ctx, instanceID, state.StateFrozen)
	if err != nil || frozen == nil {
		return "gone"
	}
	defer e.unlockApp(frozen.AppID)
	return e.compressFrozenLocked(ctx, *frozen, frozenCompressProactive)
}

func (e *Engine) frozenCompressBackedOff(nodeID string, now time.Time) bool {
	if e.compressBackoff == nil {
		return false
	}
	e.compressBackoff.mu.Lock()
	defer e.compressBackoff.mu.Unlock()
	return now.Before(e.compressBackoff.until[nodeID])
}

func (e *Engine) backOffFrozenCompress(nodeID string, now time.Time) {
	if e.compressBackoff == nil {
		return
	}
	e.compressBackoff.mu.Lock()
	defer e.compressBackoff.mu.Unlock()
	e.compressBackoff.until[nodeID] = now.Add(time.Duration(api.FrozenCompressUnavailableBackoffSeconds) * time.Second)
}
