package mcpmanagement

import (
	"net"
	"net/http"
	"strings"
	"time"
)

// Bounds of the hosted bridge (ADR-20261026195952). A self-hosted bridge
// keeps maxOAuthEntries.
const (
	hostedOAuthEntries      = 2000
	hostedGrantsPerAccount  = 10
	hostedRegistrationLimit = 20
	registrationWindowSize  = time.Hour
)

type registrationWindow struct {
	start time.Time
	count int
}

// capacity bounds clients, pending flows, codes and grants each.
func (r *remoteServer) capacity() int {
	if r.opts.Hosted {
		return hostedOAuthEntries
	}
	return maxOAuthEntries
}

// accountGrantsLocked counts an account's grants that can still be used.
func (r *remoteServer) accountGrantsLocked(accountID string) int {
	n := 0
	for _, g := range r.grants {
		if g.accountID == accountID && !g.revoked && time.Now().Before(g.credentialExpires) {
			n++
		}
	}
	return n
}

// registrationAllowedLocked rate-limits dynamic client registration per
// client address on the hosted bridge. gatewayd-public sets X-Forwarded-For
// to the canonical client address; it is trusted only over loopback.
func (r *remoteServer) registrationAllowedLocked(req *http.Request) bool {
	if !r.opts.Hosted {
		return true
	}
	address, _, err := net.SplitHostPort(req.RemoteAddr)
	if err != nil {
		address = req.RemoteAddr
	}
	if ip := net.ParseIP(address); ip != nil && ip.IsLoopback() {
		if forwarded := strings.TrimSpace(strings.Split(req.Header.Get("X-Forwarded-For"), ",")[0]); forwarded != "" {
			address = forwarded
		}
	}
	now := time.Now()
	if r.registrations == nil || len(r.registrations) > 4*hostedOAuthEntries {
		r.registrations = map[string]registrationWindow{}
	}
	window := r.registrations[address]
	if now.Sub(window.start) >= registrationWindowSize {
		window = registrationWindow{start: now}
	}
	if window.count >= hostedRegistrationLimit {
		return false
	}
	window.count++
	r.registrations[address] = window
	return true
}
