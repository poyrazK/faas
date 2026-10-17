package realtime

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// SSE shares the managed endpoint path, connection limits and retained history.
func (m *Manager) serveSSE(w http.ResponseWriter, r *http.Request) {
	id, ok := endpointIDFromPath(r.URL.Path)
	if !ok {
		http.NotFound(w, r)
		return
	}
	value, ok := m.endpoints.Load(id)
	if !ok {
		http.NotFound(w, r)
		return
	}
	node := value.(*endpointState)
	endpoint := *node.config.Load()
	w.Header().Set("Cache-Control", "no-store")
	if origin := r.Header.Get("Origin"); origin != "" {
		if len(endpoint.AllowedOrigins) == 0 || !checkAllowedOrigins(endpoint.AllowedOrigins)(r) {
			http.Error(w, "origin forbidden", http.StatusForbidden)
			return
		}
		w.Header().Set("Access-Control-Allow-Origin", origin)
		w.Header().Add("Vary", "Origin")
	}
	if r.Method == http.MethodOptions {
		w.Header().Set("Access-Control-Allow-Methods", "GET, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Authorization, Last-Event-ID")
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	token, _ := bearerToken(r)
	usesTicket := isTicket(token)
	if usesTicket && (!m.cfg.TicketPreview || m.cfg.TicketReader == nil) {
		http.Error(w, "ticket unavailable", http.StatusUnauthorized)
		return
	}
	authorizer, ok := m.hooks.(ChannelAuthorizer)
	if !m.cfg.ResumePreview || m.cfg.HistoryReader == nil || (!ok && !usesTicket) {
		http.Error(w, "realtime history unavailable", http.StatusServiceUnavailable)
		return
	}
	if !usesTicket && (endpoint.ClientAuth.Mode != AuthModeOIDCJWT || m.cfg.JWTAuthorizer == nil) {
		http.Error(w, "OIDC required", http.StatusForbidden)
		return
	}
	channel := r.URL.Query().Get("channel")
	if !validChannel(channel) {
		http.Error(w, "invalid channel", 400)
		return
	}
	cursor := r.Header.Get("Last-Event-ID")
	if values, exists := r.URL.Query()["after"]; exists {
		if len(values) != 1 {
			http.Error(w, "invalid cursor", 400)
			return
		}
		cursor = values[0]
	}
	after := int64(0)
	if cursor != "" {
		var err error
		after, err = strconv.ParseInt(cursor, 10, 64)
		if err != nil || after < 0 {
			http.Error(w, "invalid cursor", 400)
			return
		}
	}
	token, ok = bearerToken(r)
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), min(endpoint.MaxConnectionAge, 5*time.Minute))
	defer cancel()
	var ticketExpiry time.Time
	var principal string
	var identity JWTIdentity
	var err error
	if usesTicket {
		claims, e := m.readTicket(ctx, id, token)
		principal, err = claims.Principal, e
		ticketExpiry = claims.ExpiresAt
		if e == nil {
			var ticketCancel context.CancelFunc
			ctx, ticketCancel = context.WithDeadline(ctx, claims.ExpiresAt)
			defer ticketCancel()
		}
	} else {
		identity, err = m.authorizeIdentity(ctx, token, endpoint.ClientAuth)
		principal = identity.Principal
	}
	if err != nil || strings.TrimSpace(principal) == "" {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	if !usesTicket && endpoint.Authorize != nil {
		principal, err = endpoint.Authorize(ctx, r)
		if err != nil || strings.TrimSpace(principal) == "" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
	}
	var issued time.Time
	if !usesTicket && m.cfg.AccessPreview {
		issued, err = verifiedTokenIssuedAt(token)
		if err != nil || m.cfg.AccessReader == nil {
			http.Error(w, "access unavailable", http.StatusUnauthorized)
			return
		}
	}
	auth := &sseAuthorization{m: m, node: node, r: r, authorizer: authorizer, endpointID: id, channel: channel,
		principal: principal, token: token, connectionID: "sse_" + uuid.NewString(), usesTicket: usesTicket,
		issued: issued, ticketExpiry: ticketExpiry, identity: identity}
	switch auth.check(ctx) {
	case sseDenied:
		http.Error(w, "not authorized", http.StatusForbidden)
		return
	case sseUnavailable:
		http.Error(w, "authorization unavailable", http.StatusServiceUnavailable)
		return
	}
	if !m.tryReserve(node) {
		http.Error(w, "connection limit reached", http.StatusTooManyRequests)
		return
	}
	defer m.release(node)
	if !m.reserveResume() {
		http.Error(w, "subscription limit reached", http.StatusTooManyRequests)
		return
	}
	defer m.resumeCount.Add(-1)
	read := func() (state.ManagedRealtimeChannelHistory, error) {
		readCtx, stop := context.WithTimeout(ctx, resumeHistoryReadTimeout)
		defer stop()
		return m.readResumeHistory(readCtx, id, channel, after)
	}
	page, err := read()
	if err != nil {
		http.Error(w, "history unavailable", http.StatusServiceUnavailable)
		return
	}
	if _, ok := w.(http.Flusher); !ok {
		http.Error(w, "streaming unavailable", http.StatusServiceUnavailable)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("X-Accel-Buffering", "no")
	controller := http.NewResponseController(w)
	send := func(event, eventID string, data any) bool {
		encoded, e := json.Marshal(data)
		if e != nil {
			return false
		}
		if controller.SetWriteDeadline(time.Now().Add(m.cfg.WriteWait)) != nil {
			return false
		}
		if eventID != "" {
			if _, e = fmt.Fprintf(w, "id: %s\n", eventID); e != nil {
				return false
			}
		}
		if _, e = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, encoded); e != nil {
			return false
		}
		return controller.Flush() == nil
	}
	if !auth.admit(ctx, send) {
		return
	}
	if !send("subscribed", "", map[string]string{
		"channel":         channel,
		"sequence":        strconv.FormatInt(after, 10),
		"oldest_sequence": strconv.FormatInt(page.OldestSequence, 10),
		"latest_sequence": strconv.FormatInt(page.LatestSequence, 10),
	}) {
		return
	}
	for {
		if !auth.admit(ctx, send) {
			return
		}
		if page.HistoryUnavailable || after > page.LatestSequence || (len(page.Messages) == 0 && after < page.LatestSequence) {
			send("resync_required", "", map[string]string{"oldest_sequence": strconv.FormatInt(page.OldestSequence, 10), "latest_sequence": strconv.FormatInt(page.LatestSequence, 10)})
			return
		}
		for _, message := range page.Messages {
			if message.Sequence != after+1 {
				send("resync_required", "", map[string]string{"code": "sequence_gap"})
				return
			}
			// Recheck durable denies before each outbound event against a
			// snapshot at most realtimeAccessRefreshInterval old.
			if !auth.admit(ctx, send) {
				return
			}
			seq := strconv.FormatInt(message.Sequence, 10)
			if message.Expired(time.Now().UTC()) {
				if !send("checkpoint", seq, map[string]string{"channel": channel, "sequence": seq, "code": "message_expired"}) {
					return
				}
				m.expiredSSECheckpoints.Add(1)
				after = message.Sequence
				continue
			}
			if !send("message", seq, map[string]any{"expires_at": message.ExpiresAt, "channel": channel, "sequence": seq, "data_base64": base64.StdEncoding.EncodeToString(message.Data), "binary": message.Binary, "metadata": message.Metadata, "target_message_id": message.TargetMessageID, "version": message.Version, "message_event": message.MessageEvent, "deleted": message.Deleted}) {
				return
			}
			after = message.Sequence
		}
		if after >= page.LatestSequence {
			if !send("heartbeat", "", map[string]string{"channel": channel}) {
				return
			}
			timer := time.NewTimer(min(m.cfg.ResumePollInterval, realtimeAccessRefreshInterval))
			select {
			case <-ctx.Done():
				timer.Stop()
				return
			case <-m.ctx.Done():
				timer.Stop()
				return
			case <-timer.C:
			}
		}
		page, err = read()
		if err != nil {
			// Transient: end the stream so the client reconnects from its cursor.
			return
		}
	}
}

type sseVerdict uint8

const (
	sseAllowed sseVerdict = iota
	sseDenied
	// sseUnavailable covers lookup failures, timeouts and stream expiry. The
	// stream ends without an error event, so clients reconnect and the next
	// request repeats full authentication; only definitive denies are terminal.
	sseUnavailable
)

type sseAuthorization struct {
	identity                                            JWTIdentity
	m                                                   *Manager
	node                                                *endpointState
	r                                                   *http.Request
	authorizer                                          ChannelAuthorizer
	endpointID, channel, principal, token, connectionID string
	usesTicket                                          bool
	issued, ticketExpiry                                time.Time
	authorizedAt, refreshedAt                           time.Time
}

// admit reports whether the stream may continue. A definitive deny is sent to
// the client as access_revoked; anything else ends the stream silently.
func (a *sseAuthorization) admit(ctx context.Context, send func(string, string, any) bool) bool {
	switch a.check(ctx) {
	case sseAllowed:
		return true
	case sseDenied:
		send("error", "", map[string]string{"code": "access_revoked"})
	}
	return false
}

func (a *sseAuthorization) check(ctx context.Context) sseVerdict {
	if ctx.Err() != nil || a.m.closed.Load() || a.node.revoked.Load() {
		return sseUnavailable
	}
	now := time.Now()
	if a.usesTicket {
		return a.checkTicket(ctx, now)
	}
	if !a.identity.ExpiresAt.IsZero() && !now.Before(a.identity.ExpiresAt) {
		return sseDenied
	}
	if now.Sub(a.authorizedAt) >= channelReauthorizeInterval {
		if verdict := a.authorize(ctx); verdict != sseAllowed {
			return verdict
		}
		a.authorizedAt = now
	}
	verdict := a.checkAccess(ctx, now)
	if verdict == sseAllowed && !a.identity.ExpiresAt.IsZero() && !time.Now().Before(a.identity.ExpiresAt) {
		return sseDenied
	}
	return verdict
}

func (a *sseAuthorization) checkTicket(ctx context.Context, now time.Time) sseVerdict {
	if !now.Before(a.ticketExpiry) {
		return sseUnavailable
	}
	if now.Sub(a.refreshedAt) < realtimeAccessRefreshInterval {
		return sseAllowed
	}
	claims, err := a.m.readTicket(ctx, a.endpointID, a.token)
	if err != nil {
		return sseUnavailable
	}
	if claims.Principal != a.principal || !slices.Contains(claims.Channels, a.channel) {
		return sseDenied
	}
	a.refreshedAt = now
	return sseAllowed
}

func (a *sseAuthorization) authorize(ctx context.Context) sseVerdict {
	current := *a.node.config.Load()
	if current.ClientAuth.Mode != AuthModeOIDCJWT {
		return sseDenied
	}
	authCtx, stop := context.WithTimeout(ctx, a.m.cfg.CallbackTimeout)
	defer stop()
	identity, err := a.m.authorizeIdentity(authCtx, a.token, current.ClientAuth)
	if err != nil {
		return sseUnavailable
	}
	if (a.identity.Principal != "" && identity.Principal != a.identity.Principal) || identity.SessionID != a.identity.SessionID {
		return sseDenied
	}
	verified := identity.Principal
	// Custom hooks may project a principal; revalidate the same projection.
	if err == nil && current.Authorize != nil {
		verified, err = current.Authorize(authCtx, a.r)
	}
	if err != nil {
		return sseUnavailable
	}
	if verified != a.principal {
		return sseDenied
	}
	parent, scope, err := api.ParseRealtimeActivityScope(a.channel)
	if err != nil {
		return sseDenied
	}
	permission := "read"
	if scope != "" {
		permission = "read_activity"
	}
	allow, err := a.authorizer.AuthorizeChannel(authCtx, Event{ID: "evt_" + uuid.NewString(), Type: EventAuthorizeChannel,
		EndpointID: a.endpointID, AppID: current.AppID, AccountID: current.AccountID, ConnectionID: a.connectionID,
		Principal: a.principal, SessionID: a.identity.SessionID, Channel: a.channel, Permission: permission, ActivityParentChannel: parent, ActivityScope: scope,
		At: time.Now().UTC(), CallbackURL: current.CallbackURL, CallbackAuthToken: current.CallbackAuthToken})
	if err != nil || authCtx.Err() != nil {
		return sseUnavailable
	}
	if !a.identity.ExpiresAt.IsZero() && !time.Now().Before(a.identity.ExpiresAt) {
		return sseDenied
	}
	if !allow {
		return sseDenied
	}
	return sseAllowed
}

func (a *sseAuthorization) checkAccess(ctx context.Context, now time.Time) sseVerdict {
	if !a.m.cfg.AccessPreview || now.Sub(a.refreshedAt) < realtimeAccessRefreshInterval {
		return sseAllowed
	}
	accessCtx, done := context.WithTimeout(ctx, realtimeAccessReadTimeout)
	access, err := a.m.cfg.AccessReader.ReadRealtimeAccess(accessCtx, a.endpointID, a.principal)
	done()
	if err != nil {
		return sseUnavailable
	}
	if sessionRevoked(access, a.issued) || channelDenied(access, a.channel) {
		return sseDenied
	}
	a.refreshedAt = now
	return sseAllowed
}
