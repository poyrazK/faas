package s3gateway

import "github.com/onebox-faas/faas/pkg/api"

// acquirePutSlot takes one of the gateway's staged-transfer slots for an
// account. One account may hold at most ObjectUploadAccountSlotSharePercent of
// them, so a single tenant's parallel uploads cannot refuse every other
// tenant's (ADR-20261023223000). It never blocks.
func (h *Handler) acquirePutSlot(account string) bool {
	share := max(1, cap(h.putSlots)*api.ObjectUploadAccountSlotSharePercent/100)
	h.slotMu.Lock()
	defer h.slotMu.Unlock()
	if h.accountSlots == nil {
		h.accountSlots = map[string]int{}
	}
	if h.accountSlots[account] >= share {
		return false
	}
	select {
	case h.putSlots <- struct{}{}:
		h.accountSlots[account]++
		return true
	default:
		return false
	}
}

func (h *Handler) releasePutSlot(account string) {
	h.slotMu.Lock()
	defer h.slotMu.Unlock()
	<-h.putSlots
	if h.accountSlots[account]--; h.accountSlots[account] <= 0 {
		delete(h.accountSlots, account)
	}
}
