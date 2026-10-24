package faas

import (
	"crypto/sha256"
	_ "embed"
	"encoding/binary"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// EventOutboxRetryPolicySchema upgrades the existing application-owned last_code
// CHECK. Apply explicitly inside an application migration transaction before
// enabling RetryPolicy on an existing outbox. Helpers never execute this DDL.
//
//go:embed event_outbox_retry_policy_schema.sql
var EventOutboxRetryPolicySchema string

type eventOutboxPolicyContextKey struct{}

// EventOutboxRetryPolicy opts into capped exponential jitter and Retry-After on
// HTTP 429/503. Durations are whole milliseconds (1 ms..1 hour). Initial defaults
// to 5 seconds, cap to 5 minutes. MaxAttempts is a lifetime recovery-claim limit;
// zero disables it. Unbound rows use worker settings; bound rows use their
// persisted policy instead.
type EventOutboxRetryPolicy struct {
	InitialBackoff time.Duration
	MaxBackoff     time.Duration
	MaxAttempts    int64
}

func eventOutboxRetryPolicy(p EventOutboxRetryPolicy) (EventOutboxRetryPolicy, error) {
	if p.InitialBackoff == 0 {
		p.InitialBackoff = 5 * time.Second
	}
	if p.MaxBackoff == 0 {
		p.MaxBackoff = 5 * time.Minute
	}
	if p.InitialBackoff < time.Millisecond || p.InitialBackoff > time.Hour || p.MaxBackoff < p.InitialBackoff || p.MaxBackoff > time.Hour || p.InitialBackoff%time.Millisecond != 0 || p.MaxBackoff%time.Millisecond != 0 || p.MaxAttempts < 0 || p.MaxAttempts > 2147483647 {
		return p, ErrInvalidEventConsumerRequest
	}
	return p, nil
}

func (c eventOutboxClaim) retryDelay(options EventOutboxRecoveryOptions) time.Duration {
	if options.RetryPolicy == nil {
		return options.Backoff
	}
	p := options.RetryPolicy
	capDelay := p.InitialBackoff
	for n := int64(1); n < c.attempts && capDelay < p.MaxBackoff; n++ {
		capDelay = min(p.MaxBackoff, 2*capDelay)
	}
	// The token was randomly generated for this claim before HTTP. Reusing its
	// entropy through a domain-separated hash avoids a random-source failure
	// after uncertain publication without exposing raw token bits as delay.
	entropy := sha256.Sum256(append([]byte("gregale-event-outbox-jitter-v1\n"), c.token[:]...))
	milliseconds := binary.BigEndian.Uint64(entropy[:8])%uint64(capDelay.Milliseconds()) + 1
	return time.Duration(milliseconds) * time.Millisecond
}

// Relative guidance is capped to the longest possible retry window. An absolute
// HTTP-date is passed to PostgreSQL, avoiding dependence on the client's clock.
func eventOutboxRetryAfter(err error) (time.Duration, *time.Time) {
	apiErr, ok := AsAPIError(err)
	if !ok || apiErr.Problem.Status != http.StatusTooManyRequests && apiErr.Problem.Status != http.StatusServiceUnavailable {
		return 0, nil
	}
	const maxSeconds = 7 * 24 * 60 * 60
	value := strings.TrimSpace(apiErr.Problem.RetryAfter)
	if len(value) > 0 && len(value) <= 256 {
		digits := true
		for _, b := range []byte(value) {
			if b < '0' || b > '9' {
				digits = false
				break
			}
		}
		if digits {
			seconds, parseErr := strconv.ParseUint(value, 10, 64)
			if parseErr != nil || seconds > maxSeconds {
				return time.Duration(maxSeconds) * time.Second, nil
			}
			return time.Duration(seconds) * time.Second, nil
		}
		if instant, parseErr := http.ParseTime(value); parseErr == nil && instant.Year() >= 1 && instant.Year() <= 9999 {
			return 0, &instant
		}
	}
	if seconds := apiErr.Problem.RetryAfterSeconds; seconds != nil && *seconds >= 0 {
		return time.Duration(min(*seconds, int64(maxSeconds))) * time.Second, nil
	}
	return 0, nil
}
