package udpd

import (
	"sync"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

type rateBucket struct {
	packets, bytes float64
	at             time.Time
}
type accountRate struct {
	directions [2]rateBucket
	last       time.Time
}

// RateLimits shares account budgets across listener sockets. Entries survive
// peer churn, so opening another session does not reset an account's burst.
// The bounded cache fails closed when all entries are recently active.
type RateLimits struct {
	mu       sync.Mutex
	accounts map[string]*accountRate
}

func NewRateLimits() *RateLimits { return &RateLimits{accounts: make(map[string]*accountRate)} }
func (r *RateLimits) Allow(account string, payloadBytes int, outbound bool) bool {
	return r.allowAt(account, payloadBytes, outbound, time.Now())
}
func (r *RateLimits) allowAt(account string, size int, outbound bool, now time.Time) bool {
	if r == nil || account == "" || size < 0 || size > api.UDPDatagramMaxBytes {
		return false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	entry := r.accounts[account]
	if entry == nil {
		if len(r.accounts) >= api.UDPRateLimitMaxAccounts {
			for key, old := range r.accounts {
				if now.Sub(old.last) >= api.UDPRateLimitIdleTTL {
					delete(r.accounts, key)
				}
			}
			if len(r.accounts) >= api.UDPRateLimitMaxAccounts {
				return false
			}
		}
		entry = &accountRate{}
		for i := range entry.directions {
			entry.directions[i] = rateBucket{packets: api.UDPPacketBurstPerAccount, bytes: api.UDPByteBurstPerAccount, at: now}
		}
		r.accounts[account] = entry
	}
	entry.last = now
	direction := 0
	if outbound {
		direction = 1
	}
	bucket := &entry.directions[direction]
	elapsed := now.Sub(bucket.at).Seconds()
	if elapsed > 0 {
		bucket.packets = min(float64(api.UDPPacketBurstPerAccount), bucket.packets+elapsed*api.UDPPacketsPerSecondPerAccount)
		bucket.bytes = min(float64(api.UDPByteBurstPerAccount), bucket.bytes+elapsed*api.UDPBytesPerSecondPerAccount)
		bucket.at = now
	}
	if bucket.packets < 1 {
		return false
	}
	// Attempts consume packet credit even when their payload exceeds byte credit.
	bucket.packets--
	if bucket.bytes < float64(size) {
		return false
	}
	bucket.bytes -= float64(size)
	return true
}
