package s3gateway

import (
	"context"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// acquirePutSlot takes one of the gateway's staged-transfer slots for an
// account. One account may hold at most ObjectUploadAccountSlotSharePercent of
// them, so a single tenant's parallel uploads cannot refuse every other
// tenant's (ADR-20261023223000). A request past the share, or finding every
// slot taken, waits up to api.ObjectUploadSlotWait for one to free; it reads
// no body while it waits.
func (h *Handler) acquirePutSlot(ctx context.Context, account string) bool {
	return h.acquirePutSlotWithin(ctx, account, api.ObjectUploadSlotWait)
}

func (h *Handler) acquirePutSlotWithin(ctx context.Context, account string, wait time.Duration) bool {
	freed, ok := h.tryPutSlot(account)
	if ok {
		return true
	}
	timer := time.NewTimer(wait)
	defer timer.Stop()
	for {
		select {
		case <-freed:
		case <-ctx.Done():
			return false
		case <-timer.C:
			return false
		}
		if freed, ok = h.tryPutSlot(account); ok {
			return true
		}
	}
}

// tryPutSlot takes a slot without waiting. When it cannot, it returns a
// channel that closes on the next release.
func (h *Handler) tryPutSlot(account string) (<-chan struct{}, bool) {
	share := max(1, cap(h.putSlots)*api.ObjectUploadAccountSlotSharePercent/100)
	h.slotMu.Lock()
	defer h.slotMu.Unlock()
	if h.accountSlots == nil {
		h.accountSlots = map[string]int{}
	}
	if h.accountSlots[account] < share {
		select {
		case h.putSlots <- struct{}{}:
			h.accountSlots[account]++
			return nil, true
		default:
		}
	}
	if h.slotFreed == nil {
		h.slotFreed = make(chan struct{})
	}
	return h.slotFreed, false
}

func (h *Handler) releasePutSlot(account string) {
	h.slotMu.Lock()
	defer h.slotMu.Unlock()
	<-h.putSlots
	if h.accountSlots[account]--; h.accountSlots[account] <= 0 {
		delete(h.accountSlots, account)
	}
	if h.slotFreed != nil {
		close(h.slotFreed)
		h.slotFreed = nil
	}
}
