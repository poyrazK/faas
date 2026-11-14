package outbound

import (
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/middleware"
)

// accessTokenGuessLimiter counts unknown access tokens per client network
// on the public AI host (bug hunt H82). Each unknown token costs a CP
// Postgres lookup, so a client past its budget is refused before the
// lookup until the window ends.
type accessTokenGuessLimiter struct {
	mu      sync.Mutex
	clients map[string]accessTokenGuesses
}

type accessTokenGuesses struct {
	start    time.Time
	failures int
}

// accessTokenClientKey is the caller's network: the address Caddy pins in
// X-Forwarded-For on the loopback hop (middleware.ClientIP), as an IPv4 /24
// or IPv6 /64, so one host cannot spread guesses over its addresses.
func accessTokenClientKey(r *http.Request) string {
	ip := net.ParseIP(middleware.ClientIP(r))
	if ip == nil {
		return "unknown"
	}
	if v4 := ip.To4(); v4 != nil {
		return v4.Mask(net.CIDRMask(24, 32)).String()
	}
	return ip.Mask(net.CIDRMask(64, 128)).String()
}

// blocked reports whether client has used its budget in the current window.
func (l *accessTokenGuessLimiter) blocked(client string, now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	g, ok := l.clients[client]
	return ok && now.Sub(g.start) < api.OutboundAIAccessTokenGuessWindow && g.failures >= api.OutboundAIAccessTokenGuessesPerWindow
}

// fail counts one unknown token for client.
func (l *accessTokenGuessLimiter) fail(client string, now time.Time) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.clients == nil {
		l.clients = make(map[string]accessTokenGuesses)
	}
	g, ok := l.clients[client]
	if !ok || now.Sub(g.start) >= api.OutboundAIAccessTokenGuessWindow {
		if !ok && len(l.clients) >= api.OutboundAIAccessTokenGuessClientsMax {
			l.evictLocked(now)
		}
		g = accessTokenGuesses{start: now}
	}
	g.failures++
	l.clients[client] = g
}

// evictLocked drops finished windows, then, if the table is still full,
// the clients with the fewest failures in a sample, so a flood of new
// networks cannot reset a busy guesser's count.
func (l *accessTokenGuessLimiter) evictLocked(now time.Time) {
	for k, g := range l.clients {
		if now.Sub(g.start) >= api.OutboundAIAccessTokenGuessWindow {
			delete(l.clients, k)
		}
	}
	for len(l.clients) >= api.OutboundAIAccessTokenGuessClientsMax {
		victim, fewest, seen := "", 0, 0
		for k, g := range l.clients {
			if victim == "" || g.failures < fewest {
				victim, fewest = k, g.failures
			}
			if seen++; seen >= 32 {
				break
			}
		}
		delete(l.clients, victim)
	}
}

// trimCache removes expired entries from a full cache, then arbitrary ones
// down to nine tenths of max, instead of clearing it: a flood of new
// entries then costs the cache a tenth of its entries, not all of them.
func trimCache[K comparable, V any](m map[K]V, max int, expired func(V) bool) {
	if len(m) < max {
		return
	}
	for k, v := range m {
		if expired(v) {
			delete(m, k)
		}
	}
	for k := range m {
		if len(m) < max*9/10 {
			return
		}
		delete(m, k)
	}
}
