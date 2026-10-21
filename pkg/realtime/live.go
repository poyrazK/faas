package realtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/gorilla/websocket"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/livequery/liveproto"
	"github.com/onebox-faas/faas/pkg/livequery/seal"
)

// Live queries (ADR-20261016151947) ride v2 connections as live.* frames. realtimed
// only carries them: Config.Live (pkg/livequery/liveproxy in cmd/realtimed)
// sends each client's frames to the hub serving the sealed query's database
// and queues the hub's frames back. Live frames are not channel traffic, so
// channel access policies and read-only tickets do not apply to them; the
// seal is the capability.

// LiveQueries carries a connection's live frames.
type LiveQueries interface {
	// Attach starts a connection's live session. routes returns the
	// endpoint's current live routes; it is read at each subscription and
	// renewal, so a re-registration applies to open connections. send queues
	// a frame to the client and may be called from any goroutine.
	Attach(routes func() []LiveRoute, send func(frame []byte) error) LiveSession
}

// LiveRoute is where seals made with one live secret are served, and the
// logins the endpoint's app reads as there: sealed queries as its
// read-only (else read-write) binding, RLS-mode reads as its Data API
// binding. An empty role refuses that mode. DataAPIAccess is what the app's
// Data API runtime last reported about access without a user token; without
// it, anonymous and server-key reads are refused.
type LiveRoute struct {
	KID           string                   `json:"kid"`      // the secret's key ID
	Database      string                   `json:"database"` // the database its seals name
	URL           string                   `json:"url"`      // the hub, ws:// or wss://
	SealedRole    string                   `json:"sealed_role,omitempty"`
	DataAPIRole   string                   `json:"data_api_role,omitempty"`
	DataAPIAccess *api.DataAPIAccessConfig `json:"data_api_access,omitempty"`
}

// validLiveRoutes checks an endpoint's live routes.
func validLiveRoutes(routes []LiveRoute) ([]LiveRoute, error) {
	if len(routes) > api.LiveQueryRoutesPerEndpoint {
		return nil, fmt.Errorf("realtime: %d live routes, at most %d", len(routes), api.LiveQueryRoutesPerEndpoint)
	}
	seen := make(map[string]bool, len(routes))
	for _, r := range routes {
		if !seal.ValidKeyID(r.KID) || seen[r.KID] {
			return nil, fmt.Errorf("realtime: invalid or repeated live route key %q", r.KID)
		}
		if r.Database == "" || len(r.Database) > api.LiveQueryDatabaseIDMaxBytes {
			return nil, fmt.Errorf("realtime: live route %s: invalid database", r.KID)
		}
		if u, err := url.Parse(r.URL); err != nil || (u.Scheme != "ws" && u.Scheme != "wss") || u.Host == "" || u.User != nil {
			return nil, fmt.Errorf("realtime: live route %s: the hub must be a ws:// or wss:// URL", r.KID)
		}
		if len(r.SealedRole) > 63 || len(r.DataAPIRole) > 63 || strings.ContainsRune(r.SealedRole+r.DataAPIRole, 0) {
			return nil, fmt.Errorf("realtime: live route %s: invalid role", r.KID)
		}
		if r.DataAPIAccess != nil {
			if err := r.DataAPIAccess.Validate(); err != nil {
				return nil, fmt.Errorf("realtime: live route %s: %w", r.KID, err)
			}
		}
		seen[r.KID] = true
	}
	return append([]LiveRoute(nil), routes...), nil
}

// verifyLiveToken replaces an RLS-mode frame's token with its verified
// claims, so the hub reads as the user without ever seeing the token. A
// sealed frame passes through; claims sent by a client are refused.
func (m *Manager) verifyLiveToken(ctx context.Context, c *connection, data []byte) ([]byte, string, string) {
	f, err := liveproto.ParseClientFrame(data)
	if err != nil {
		return data, "", "" // the hub answers a malformed frame
	}
	if len(f.Claims) > 0 || f.Role != "" || f.Access != "" {
		return nil, liveproto.CodeProtocol, "realtimed adds the claims, the role and the access kind; send only the user's token"
	}
	if f.ServerKey != "" && c.browser {
		return nil, liveproto.CodeForbidden, "Data API server keys stay out of browsers; use the user's token or anonymous access"
	}
	if !f.RLS() || f.Anonymous || f.ServerKey != "" {
		// Sealed queries carry their own capability; anonymous and server-key
		// reads are admitted by the live proxy against the route's settings.
		return data, "", ""
	}
	policy, ok := dataAPITokenPolicy(c.liveRoutes(), f.Database)
	if !ok {
		policy = c.endpointConfig().ClientAuth
	}
	if policy.Mode != AuthModeOIDCJWT {
		return nil, liveproto.CodeForbidden, "RLS-mode live queries need the Data API's token settings (usage reporting on) or a realtime endpoint that verifies OIDC tokens"
	}
	verifier, ok := m.cfg.JWTAuthorizer.(ClaimsAuthorizer)
	if !ok {
		return nil, liveproto.CodeUnavailable, "this realtime node cannot verify tokens for live queries"
	}
	claims, expires, err := verifier.AuthorizeClaims(ctx, f.Token, policy)
	if err != nil {
		return nil, liveproto.CodeSealRejected, "the token was not accepted"
	}
	f.Token, f.Claims, f.ClaimsExpires = "", claims, expires.Unix()
	out, err := json.Marshal(f)
	if err != nil {
		return nil, liveproto.CodeInternal, "encode the verified frame"
	}
	return out, "", ""
}

// dataAPITokenPolicy returns how the Data API serving database verifies user
// tokens, as its runtime last reported, so a live read accepts exactly the
// tokens a REST read does. ok is false when no route reports it.
func dataAPITokenPolicy(routes []LiveRoute, database string) (AuthPolicy, bool) {
	for _, r := range routes {
		if r.Database != database || r.DataAPIRole == "" || r.DataAPIAccess == nil || r.DataAPIAccess.Auth == nil {
			continue
		}
		issuer, jwks, audience, algorithms := r.DataAPIAccess.Auth.Policy()
		return AuthPolicy{Mode: AuthModeOIDCJWT, Issuer: issuer, JWKSURL: jwks, Audience: audience, Algorithms: algorithms}, true
	}
	return AuthPolicy{}, false
}

// endpointConfig returns the connection's endpoint as currently registered.
func (c *connection) endpointConfig() Endpoint {
	if c.state != nil {
		if e := c.state.config.Load(); e != nil {
			return *e
		}
	}
	return c.endpoint
}

// liveRoutes returns the connection's endpoint's current live routes.
func (c *connection) liveRoutes() []LiveRoute { return c.endpointConfig().LiveRoutes }

// LiveSession is one connection's live queries.
type LiveSession interface {
	Receive(ctx context.Context, frame []byte)
	Close()
}

// ClaimsAuthorizer is an optional JWTAuthorizer extension for RLS-mode live
// queries: it verifies a user's token against the endpoint's policy and
// returns the token's claims (a JSON object) and expiry, which the database
// reads as the user.
type ClaimsAuthorizer interface {
	AuthorizeClaims(ctx context.Context, token string, policy AuthPolicy) (json.RawMessage, time.Time, error)
}

// liveFrameType returns the type of a live.* frame, or "".
func liveFrameType(data []byte) (string, string) {
	var head struct {
		Type         string `json:"type"`
		Subscription string `json:"subscription"`
	}
	if json.Unmarshal(data, &head) != nil || !strings.HasPrefix(head.Type, "live.") {
		return "", ""
	}
	return head.Type, head.Subscription
}

// handleLive passes a live frame to the connection's live session.
func (m *Manager) handleLive(ctx context.Context, c *connection, data []byte, subscription string) {
	c.mu.Lock()
	c.info.LastSeen = time.Now().UTC()
	c.mu.Unlock()
	if m.cfg.Live == nil {
		frame, _ := json.Marshal(map[string]any{
			"type": "live.error", "subscription": subscription, "code": "unavailable",
			"message": "live queries are not enabled on this endpoint", "closed": true,
		})
		_ = m.queueLive(ctx, c, frame)
		return
	}
	data, code, msg := m.verifyLiveToken(ctx, c, data)
	if code != "" {
		frame, _ := json.Marshal(map[string]any{
			"type": "live.error", "subscription": subscription, "code": code, "message": msg, "closed": true,
		})
		_ = m.queueLive(ctx, c, frame)
		return
	}
	// The session's frames arrive from the hub, outside this frame's context.
	c.liveOnce.Do(func() { //nolint:contextcheck // queued frames outlive the client frame that started the session
		c.live = m.cfg.Live.Attach(c.liveRoutes, func(frame []byte) error { return m.queueLive(context.Background(), c, frame) })
	})
	c.live.Receive(ctx, data)
}

// queueLive queues a live frame. Live frames count against the connection's
// outbound budget and its endpoint's delivered usage (ADR-1323) like any
// other; a client too slow for them is closed.
func (m *Manager) queueLive(ctx context.Context, c *connection, frame []byte) error {
	err := m.queueOutbound(ctx, c, Message{Data: frame})
	if errors.Is(err, ErrOutboundQueueFull) {
		_ = c.close(websocket.CloseTryAgainLater, resumeOutputQueueFullReason)
	}
	if err == nil {
		m.sentMessages.Add(1)
		m.sentBytes.Add(uint64(len(frame)))
		c.state.usage.delivered(len(frame))
	}
	return err
}

// closeLive ends the connection's live session.
func (c *connection) closeLive() {
	c.liveOnce.Do(func() {}) // no session may start after this
	if c.live != nil {
		c.live.Close()
	}
}
