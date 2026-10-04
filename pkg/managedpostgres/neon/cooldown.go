package neon

import (
	"math"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

const defaultRateLimitCooldown = time.Minute

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
