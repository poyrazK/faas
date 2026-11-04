package object

import (
	"sync"
	"time"
)

// providerHealth remembers backends whose provider refused this
// installation's credentials (objectstorage.ErrConfiguration, a 401/403).
// Every bucket created on production-us since 10-14 stalled on
// gcs-us-east1-prod-v1 with error_code configuration, yet capabilities kept
// advertising object storage, because readiness read static configuration
// only (Hunt 14 H14-14). A later successful provider operation clears the
// mark. The mark is per process: after a restart it returns with the next
// failed attempt (stalled buckets retry hourly; a new bucket fails at once).
type providerHealth struct {
	mu     sync.Mutex
	failed map[string]time.Time
}

func (h *providerHealth) markConfigurationFailure(backendID string, at time.Time) {
	if backendID == "" {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.failed == nil {
		h.failed = map[string]time.Time{}
	}
	h.failed[backendID] = at
}

func (h *providerHealth) markSuccess(backendID string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	delete(h.failed, backendID)
}

// anyFailing reports whether any of backendIDs last failed on credentials.
func (h *providerHealth) anyFailing(backendIDs []string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, id := range backendIDs {
		if _, failed := h.failed[id]; failed {
			return true
		}
	}
	return false
}
