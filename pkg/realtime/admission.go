package realtime

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"slices"
	"time"

	"github.com/gorilla/websocket"
)

// connectionAdmission records what admitted a connection, so an endpoint
// update can close the sockets it no longer accepts: a finalized or expired
// static token (the reason to finalize is often an exposed credential), a
// changed auth mode or OIDC policy, or an origin removed from allowed_origins.
// Without it those sockets lived until their maximum age, up to 7 days.
type connectionAdmission struct {
	// recorded is false for a connection admitted without a record (the zero
	// value); such a connection is never closed by an endpoint update.
	recorded          bool
	ticket            bool
	mode              AuthMode
	oidcPolicy        string
	staticToken       [sha256.Size]byte
	origin            string
	browserCredential bool
}

func newConnectionAdmission(endpoint Endpoint, ticket, browserCredential bool, staticToken, origin string) connectionAdmission {
	a := connectionAdmission{recorded: true, ticket: ticket, mode: endpoint.ClientAuth.Mode, browserCredential: browserCredential}
	if endpoint.ClientAuth.Mode == AuthModeOIDCJWT {
		a.oidcPolicy = oidcPolicyFingerprint(endpoint.ClientAuth)
	}
	if staticToken != "" {
		a.staticToken = sha256.Sum256([]byte(staticToken))
	}
	if origin != "" {
		if normalized, err := normalizeOrigin(origin); err == nil {
			origin = normalized
		}
		a.origin = origin
	}
	return a
}

// oidcPolicyFingerprint covers what verifies a token, not the claims channel
// rules extract: a channel-rule change must not drop connections.
func oidcPolicyFingerprint(policy AuthPolicy) string {
	audience := slices.Sorted(slices.Values(policy.Audience))
	algorithms := slices.Sorted(slices.Values(policy.Algorithms))
	raw, _ := json.Marshal([]any{policy.Issuer, policy.JWKSURL, audience, algorithms, policy.RequiredClaims})
	return string(raw)
}

// admits reports whether endpoint, as configured now, would still accept
// the connection. Tickets are rechecked against their own store every second.
func (a connectionAdmission) admits(endpoint *Endpoint, now time.Time) bool {
	if !a.recorded {
		return true
	}
	if a.origin != "" && (a.browserCredential || len(endpoint.AllowedOrigins) > 0) && !slices.Contains(endpoint.AllowedOrigins, a.origin) {
		return false
	}
	if a.ticket {
		return true
	}
	if endpoint.ClientAuth.Mode != a.mode {
		return false
	}
	switch a.mode {
	case AuthModeOIDCJWT:
		return a.oidcPolicy == oidcPolicyFingerprint(endpoint.ClientAuth)
	case AuthModeStaticBearer:
		current := sha256.Sum256([]byte(endpoint.AuthToken))
		if subtle.ConstantTimeCompare(a.staticToken[:], current[:]) == 1 {
			return true
		}
		if endpoint.AuthTokenPrevious == "" || (!endpoint.AuthTokenPreviousExpiresAt.IsZero() && !now.Before(endpoint.AuthTokenPreviousExpiresAt)) {
			return false
		}
		previous := sha256.Sum256([]byte(endpoint.AuthTokenPrevious))
		return subtle.ConstantTimeCompare(a.staticToken[:], previous[:]) == 1
	}
	return true
}

// closeConnectionsNoLongerAdmitted closes the endpoint's sockets its new
// configuration would refuse. apid re-registers every endpoint every 30
// seconds, which also closes static-token sockets whose previous token's
// grace has passed.
func (m *Manager) closeConnectionsNoLongerAdmitted(state *endpointState, endpoint *Endpoint) {
	now := time.Now()
	var stale []*connection
	m.mu.RLock()
	for _, conn := range m.conns {
		if conn.state == state && !conn.admission.admits(endpoint, now) {
			stale = append(stale, conn)
		}
	}
	m.mu.RUnlock()
	for _, conn := range stale {
		conn.terminate(websocket.ClosePolicyViolation, "endpoint credentials or origins changed")
	}
}
