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
	// hostedAuthorizeLimit bounds authorization starts per client address an
	// hour. GET /oauth/authorize is unauthenticated and created a pending
	// flow per call, so about 7 requests a second filled the pool and every
	// real sign-in got "Authorization capacity reached" (bug hunt 2026-10-28
	// H40).
	hostedAuthorizeLimit   = 60
	registrationWindowSize = time.Hour
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
	return r.addressAllowedLocked(&r.registrations, req, hostedRegistrationLimit)
}

// authorizeAllowedLocked rate-limits authorization starts per client address
// on the hosted bridge.
func (r *remoteServer) authorizeAllowedLocked(req *http.Request) bool {
	return r.addressAllowedLocked(&r.authorizations, req, hostedAuthorizeLimit)
}

func (r *remoteServer) addressAllowedLocked(windows *map[string]registrationWindow, req *http.Request, limit int) bool {
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
	if *windows == nil {
		*windows = map[string]registrationWindow{}
	}
	if len(*windows) > 4*hostedOAuthEntries {
		// Drop finished windows first: wiping them all let an address that
		// cycled through many others start over.
		for key, w := range *windows {
			if now.Sub(w.start) >= registrationWindowSize {
				delete(*windows, key)
			}
		}
		if len(*windows) > 4*hostedOAuthEntries {
			*windows = map[string]registrationWindow{}
		}
	}
	window := (*windows)[address]
	if now.Sub(window.start) >= registrationWindowSize {
		window = registrationWindow{start: now}
	}
	if window.count >= limit {
		return false
	}
	window.count++
	(*windows)[address] = window
	return true
}

// evictOldestRequestLocked drops the pending authorization that expires
// first, making room for a new one instead of refusing every sign-in while
// the pool is full.
func (r *remoteServer) evictOldestRequestLocked() {
	var oldest string
	for id, flow := range r.requests {
		if oldest == "" || flow.Expires.Before(r.requests[oldest].Expires) {
			oldest = id
		}
	}
	delete(r.requests, oldest)
}
