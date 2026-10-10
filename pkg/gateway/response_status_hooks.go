package gateway

import (
	"context"
	"sync"
	"time"
)

// responseStatusHooks run once the request's final status is known
// (ADR-965 response-counted throttles). ServeHTTP attaches one set per
// request and runs it on return.
type responseStatusHooks struct {
	mu  sync.Mutex
	fns []func(status int)
}

type responseStatusHooksKey struct{}

func withResponseStatusHooks(ctx context.Context) (context.Context, *responseStatusHooks) {
	h := &responseStatusHooks{}
	return context.WithValue(ctx, responseStatusHooksKey{}, h), h
}

// onResponseStatus registers fn to run with the final status. It reports
// false when the request carries no hook set (a caller outside ServeHTTP).
func onResponseStatus(ctx context.Context, fn func(status int)) bool {
	h, _ := ctx.Value(responseStatusHooksKey{}).(*responseStatusHooks)
	if h == nil {
		return false
	}
	h.mu.Lock()
	h.fns = append(h.fns, fn)
	h.mu.Unlock()
	return true
}

func (h *responseStatusHooks) run(status int) {
	if h == nil {
		return
	}
	h.mu.Lock()
	fns := h.fns
	h.fns = nil
	h.mu.Unlock()
	for _, fn := range fns {
		fn(status)
	}
}

func hasResponseStatusHooks(ctx context.Context) bool {
	h, _ := ctx.Value(responseStatusHooksKey{}).(*responseStatusHooks)
	return h != nil
}

// throttleChargeTimeout bounds a post-response central charge.
const throttleChargeTimeout = 2 * time.Second

// throttleHasToken peeks the bucket a throttle rule would charge.
func (h *Handler) throttleHasToken(rule *EdgeRuleThrottleResolved, dimensional bool, bucketKey, consumerID string, cap int) bool {
	if dimensional {
		return h.routeConsumerLimiter.HasConsumerToken(bucketKey, consumerID, rule.RequestsPerSecond, float64(rule.Burst), cap)
	}
	return h.routeLimiter.HasToken(bucketKey, rule.RequestsPerSecond, float64(rule.Burst))
}

// chargeThrottleOnResponse charges a response-counted rule's bucket when the
// final status is one it counts (ADR-965). A local-only limiter charges
// inline; a central charge runs off the request path so a Postgres round
// trip never delays the response.
func (h *Handler) chargeThrottleOnResponse(ctx context.Context, rule *EdgeRuleThrottleResolved, dimensional bool, charge func(context.Context) bool) {
	limiter := h.routeLimiter
	if dimensional {
		limiter = h.routeConsumerLimiter
	}
	detached := context.WithoutCancel(ctx)
	onResponseStatus(ctx, func(status int) {
		if !rule.CountStatuses[status] {
			return
		}
		if limiter.isNoopBackend() {
			charge(detached)
			return
		}
		go func() {
			chargeCtx, cancel := context.WithTimeout(detached, throttleChargeTimeout)
			defer cancel()
			charge(chargeCtx)
		}()
	})
}
