package s3gateway

import "github.com/onebox-faas/faas/pkg/api"

func (h *DiskHandler) reserve(bytes int64) bool {
	return h.reserveBytes(bytes, api.MaxObjectUploadSpoolBytes)
}

func (h *DiskHandler) reserveBytes(bytes, limit int64) bool {
	if bytes < 0 || bytes > limit {
		return false
	}
	select {
	case h.writes <- struct{}{}:
	default:
		return false
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	available, err := h.config.Store.AvailableBytes()
	need := h.reserved + uint64(bytes) // bytes has passed the nonnegative PUT length check.
	if err != nil || need > uint64(limit) || available < need || available-need < uint64(h.config.MinFreeBytes) {
		<-h.writes
		return false
	}
	h.reserved = need
	return true
}

func (h *DiskHandler) release(bytes int64) {
	h.mu.Lock()
	h.reserved -= uint64(bytes)
	h.mu.Unlock()
	<-h.writes
}
