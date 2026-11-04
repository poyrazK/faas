package gateway

import (
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// publicAuthGuessLimiter counts wrong passwords at an app's basic-auth gate
// per app and client prefix (bug hunt H76). The gate compared every guess at
// full request rate with no failure limit, before the per-account rate limit
// and the wake gate, so a short password on an app or PR preview could be
// guessed from as many addresses as an attacker had. Past
// api.PublicAuthBasicFailuresPerWindow failures in the window the prefix gets
// 429 without its guess being checked, until the failures age out.
type publicAuthGuessLimiter struct {
	mu      sync.Mutex
	now     func() time.Time
	entries map[string]*publicAuthGuesses
}

type publicAuthGuesses struct {
	at      []time.Time // failures inside the window, oldest first
	audited time.Time   // when the last audit row for this key was written
}

func newPublicAuthGuessLimiter() *publicAuthGuessLimiter {
	return &publicAuthGuessLimiter{now: time.Now, entries: map[string]*publicAuthGuesses{}}
}

// publicAuthClientPrefix is the client's IPv4 /24 or IPv6 /64: one host or
// one home network shares a budget, so rotating addresses inside one
// allocation does not buy more guesses.
func publicAuthClientPrefix(r *http.Request) string {
	ip, ok := TrustedClientIP(r)
	if !ok {
		return "unknown"
	}
	bits, total := api.PublicAuthGuessIPv6Prefix, 128
	if v4 := ip.To4(); v4 != nil {
		ip, bits, total = v4, api.PublicAuthGuessIPv4Prefix, 32
	}
	mask := net.CIDRMask(bits, total)
	return (&net.IPNet{IP: ip.Mask(mask), Mask: mask}).String()
}

func publicAuthGuessKey(appID, prefix string) string { return appID + "\x00" + prefix }

func (l *publicAuthGuessLimiter) recentLocked(key string, now time.Time) *publicAuthGuesses {
	g := l.entries[key]
	if g == nil {
		return nil
	}
	i := 0
	for i < len(g.at) && now.Sub(g.at[i]) >= api.PublicAuthBasicFailureWindow {
		i++
	}
	g.at = g.at[i:]
	if len(g.at) == 0 && now.Sub(g.audited) >= api.PublicAuthBasicFailureWindow {
		delete(l.entries, key)
		return nil
	}
	return g
}

// retryAfter is how long key's guesses stay refused; zero when a guess may be
// checked.
func (l *publicAuthGuessLimiter) retryAfter(key string) time.Duration {
	if l == nil {
		return 0
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	g := l.recentLocked(key, now)
	if g == nil || len(g.at) < api.PublicAuthBasicFailuresPerWindow {
		return 0
	}
	return g.at[len(g.at)-api.PublicAuthBasicFailuresPerWindow].Add(api.PublicAuthBasicFailureWindow).Sub(now)
}

// fail counts a wrong guess for key. It reports whether an audit row should
// be written: one per key per window, so a guessing flood cannot fill the
// gateway's bounded audit queue and push out other apps' security rows.
func (l *publicAuthGuessLimiter) fail(key string) bool {
	if l == nil {
		return true
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	g := l.recentLocked(key, now)
	if g == nil {
		if len(l.entries) >= api.PublicAuthGuessKeysMax {
			l.evictLocked(now)
		}
		g = &publicAuthGuesses{}
		l.entries[key] = g
	}
	g.at = append(g.at, now)
	if len(g.at) > api.PublicAuthBasicFailuresPerWindow {
		g.at = g.at[len(g.at)-api.PublicAuthBasicFailuresPerWindow:]
	}
	if now.Sub(g.audited) < api.PublicAuthBasicFailureWindow {
		return false
	}
	g.audited = now
	return true
}

// evictLocked drops expired keys, else the key with the fewest failures in a
// random sample, so filler keys cannot push out one being guessed against.
func (l *publicAuthGuessLimiter) evictLocked(now time.Time) {
	var victim string
	fewest := -1
	seen := 0
	for key := range l.entries {
		g := l.recentLocked(key, now)
		if g == nil {
			return
		}
		if fewest < 0 || len(g.at) < fewest {
			victim, fewest = key, len(g.at)
		}
		if seen++; seen >= api.PublicAuthGuessEvictionSample {
			break
		}
	}
	if fewest >= 0 {
		delete(l.entries, victim)
	}
}
