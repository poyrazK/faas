package pgproxy

import (
	"net"
	"net/netip"
	"sync"
	"time"
)

// Connection admission (bug hunt 34). pgproxyd is the public listener of a
// database node, so a handshake slot must not be cheap to hold, the platform's
// own compute nodes must not be locked out by strangers filling those slots,
// and a source must not guess one database's passwords without bound
// (ADR-1027: "rate-limits per database and per source").

const (
	// defaultStartupTimeout bounds the wait for TLS and the startup message.
	// A client sends both at once; holding a slot idle costs an attacker
	// a new connection this often.
	defaultStartupTimeout = 5 * time.Second
	// defaultMaxAuthFailures is how many failed logins one source may make
	// against one database within authFailureWindow.
	defaultMaxAuthFailures = 10
	authFailureWindow      = time.Minute
	// authFailureEntries bounds the failure table; expired entries are
	// dropped when it is full.
	authFailureEntries = 100_000
)

// trustedSource reports whether addr is one of the configured trusted
// networks (the platform's compute nodes). Trusted connections skip the
// handshake caps, which bound strangers.
func (p *Proxy) trustedSource(addr net.Addr) bool {
	if len(p.cfg.TrustedSources) == 0 {
		return false
	}
	tcp, ok := addr.(*net.TCPAddr)
	if !ok {
		return false
	}
	ip, ok := netip.AddrFromSlice(tcp.IP)
	if !ok {
		return false
	}
	ip = ip.Unmap()
	for _, prefix := range p.cfg.TrustedSources {
		if prefix.Contains(ip) {
			return true
		}
	}
	return false
}

// authFailures counts failed logins per database and source in fixed
// windows.
type authFailures struct {
	mu      sync.Mutex
	limit   int
	entries map[string]authFailureCount
}

type authFailureCount struct {
	start time.Time
	n     int
}

func newAuthFailures(limit int) *authFailures {
	return &authFailures{limit: limit, entries: map[string]authFailureCount{}}
}

func authFailureKey(databaseID, source string) string { return databaseID + "\x00" + source }

// blocked reports whether key reached its limit in the current window.
func (a *authFailures) blocked(key string, now time.Time) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	e, ok := a.entries[key]
	return ok && now.Sub(e.start) < authFailureWindow && e.n >= a.limit
}

// fail records one failed login for key.
func (a *authFailures) fail(key string, now time.Time) {
	a.mu.Lock()
	defer a.mu.Unlock()
	e, ok := a.entries[key]
	if !ok || now.Sub(e.start) >= authFailureWindow {
		if !ok && len(a.entries) >= authFailureEntries {
			for k, old := range a.entries {
				if now.Sub(old.start) >= authFailureWindow {
					delete(a.entries, k)
				}
			}
			if len(a.entries) >= authFailureEntries {
				return // full of live windows: count nothing new rather than grow
			}
		}
		e = authFailureCount{start: now}
	}
	e.n++
	a.entries[key] = e
}
