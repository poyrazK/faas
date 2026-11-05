package object

import (
	"sync"
	"time"
)

// providerRefusalHold is how long a credential refusal withdraws a backend.
// After it, the next bucket operation reaches the provider again and either
// clears the mark or renews it. It matches the configuration retry
// (ObjectRetryPolicy), so a stalled bucket that keeps failing keeps the mark.
const providerRefusalHold = time.Hour

// providerHealth remembers backends whose provider refused this
// installation's credentials (objectstorage.ErrConfiguration, a 401/403).
// Every bucket created on production-us since 10-14 stalled on
// gcs-us-east1-prod-v1 with error_code configuration, yet capabilities kept
// advertising object storage, because readiness read static configuration
// only (Hunt 14 H14-14). A later successful provider operation clears the
// mark, and it lapses after providerRefusalHold: while it holds, creates are
// refused before reaching the provider, so without the lapse an installation
// with no stalled bucket left to retry stayed withdrawn until apid restarted.
// The mark is per process: after a restart it returns with the next failed
// attempt.
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

// anyFailing reports whether any of backendIDs refused credentials within
// providerRefusalHold of now.
func (h *providerHealth) anyFailing(backendIDs []string, now time.Time) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, id := range backendIDs {
		if at, failed := h.failed[id]; failed {
			if now.Sub(at) < providerRefusalHold {
				return true
			}
			delete(h.failed, id)
		}
	}
	return false
}
