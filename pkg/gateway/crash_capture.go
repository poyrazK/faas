package gateway

import (
	"context"
	"net/http"
	"sync"
	"time"
)

// crashCaptureRequester asks for an ADR-733 crash capture of the instance
// that answered 5xx. The store decides (opt-in, one in flight, cooldown); a
// refusal is not an error.
type crashCaptureRequester interface {
	RequestCrashCapture(ctx context.Context, appID, instanceID string, statusCode int, route string)
}

// crashCaptureAttemptInterval throttles requests per app inside one gateway
// so a burst of 5xx costs one store round-trip, not one per response.
const crashCaptureAttemptInterval = 30 * time.Second

// crashCaptureRequestTimeout bounds the detached store call.
const crashCaptureRequestTimeout = 2 * time.Second

type crashCaptureThrottle struct {
	mu   sync.Mutex
	last map[string]time.Time
}

func (t *crashCaptureThrottle) allow(appID string, now time.Time) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.last == nil {
		t.last = make(map[string]time.Time)
	}
	if last, ok := t.last[appID]; ok && now.Sub(last) < crashCaptureAttemptInterval {
		return false
	}
	t.last[appID] = now
	return true
}

func (h *Handler) maybeRequestCrashCapture(r *http.Request, app App, target Target, status int) {
	if status < 500 || status > 599 || target.InstanceID == "" {
		return
	}
	requester, ok := h.backend.(crashCaptureRequester)
	if !ok || !h.crashThrottle.allow(app.ID, time.Now()) {
		return
	}
	route := r.URL.Path
	// Detached from the client: the request is already answered.
	base := context.WithoutCancel(r.Context())
	go func() {
		ctx, cancel := context.WithTimeout(base, crashCaptureRequestTimeout)
		defer cancel()
		requester.RequestCrashCapture(ctx, app.ID, target.InstanceID, status, route)
	}()
}
