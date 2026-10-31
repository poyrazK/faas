package neon

import (
	"context"
	"math"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

const defaultRateLimitCooldown = time.Minute

// backgroundRequestInterval spaces the requests of background sweeps (usage,
// health, caps): at most five a second, 300 a minute, whatever the fleet's
// size. Neon does not publish its general API limit, and a 429 defers every
// request through the provider, customer actions included, so sweeps keep
// well clear of it. Requests without the background mark are never paced.
const backgroundRequestInterval = 200 * time.Millisecond

// requestPacer hands out background request slots in order.
type requestPacer struct {
	mu   sync.Mutex
	next time.Time
}

// paceBackground reserves the next background slot and waits for it.
func (p *Provider) paceBackground(ctx context.Context) error {
	now := p.now()
	p.pacer.mu.Lock()
	slot := p.pacer.next
	if slot.Before(now) {
		slot = now
	}
	p.pacer.next = slot.Add(p.backgroundInterval)
	p.pacer.mu.Unlock()
	if wait := slot.Sub(now); wait > 0 {
		return sleepContext(ctx, wait)
	}
	return nil
}

// Cooldowns are shared by requests through one provider instance. Neon's
// consumption endpoints have a separate, lower account quota; exhausting it
// must not block lifecycle or credential operations. This is reactive local
// backoff, not an account-wide request budget across backends or processes.
type requestCooldown struct {
	mu               sync.Mutex
	apiUntil         time.Time
	consumptionUntil time.Time
}

func (p *Provider) requestsDeferred(path string) bool {
	now := p.now()
	p.cooldown.mu.Lock()
	defer p.cooldown.mu.Unlock()
	return now.Before(p.cooldown.apiUntil) ||
		(isConsumptionPath(path) && now.Before(p.cooldown.consumptionUntil))
}

func (p *Provider) deferRequests(path, retryAfter string) {
	until := retryAfterDeadline(p.now(), retryAfter)
	p.cooldown.mu.Lock()
	defer p.cooldown.mu.Unlock()
	deadline := &p.cooldown.apiUntil
	if isConsumptionPath(path) {
		deadline = &p.cooldown.consumptionUntil
	}
	// Responses already in flight can arrive out of order. A shorter delay
	// must not erase a longer cooldown from another concurrent request.
	if until.After(*deadline) {
		*deadline = until
	}
}

func isConsumptionPath(path string) bool {
	return strings.HasPrefix(path, "/consumption_history/")
}

func retryAfterDeadline(now time.Time, value string) time.Time {
	value = strings.TrimSpace(value)
	if seconds, err := strconv.ParseInt(value, 10, 64); err == nil && strings.Trim(value, "0123456789") == "" &&
		seconds > 0 && seconds <= math.MaxInt64/int64(time.Second) {
		return now.Add(time.Duration(seconds) * time.Second)
	}
	if deadline, err := http.ParseTime(value); err == nil && deadline.After(now) {
		return deadline
	}
	// Missing, zero, expired, malformed, and overflowing headers must not
	// turn a rate limit into an immediate retry loop.
	return now.Add(defaultRateLimitCooldown)
}
