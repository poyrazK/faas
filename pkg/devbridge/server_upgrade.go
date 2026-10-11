package devbridge

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
)

func upgradeProblem(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrUpgradeLimit):
		bridgeProblem(w, http.StatusTooManyRequests, "dev_bridge_upgrade_limit")
	case errors.Is(err, ErrUpgradeDisabled):
		bridgeProblem(w, http.StatusNotImplemented, "dev_bridge_upgrade_unsupported")
	default:
		bridgeProblem(w, http.StatusServiceUnavailable, "dev_bridge_disconnected")
	}
}

// upgradeTraffic forwards a scoped WebSocket handshake to the laptop over one
// HTTP/2 tunnel stream and, once the local process switches protocols,
// hijacks the caller's connection. Only WebSocket is accepted (ADR-742).
func (s *Server) upgradeTraffic(w http.ResponseWriter, r *http.Request, session Session) {
	if !IsWebSocketUpgrade(r) {
		upgradeProblem(w, ErrUpgradeDisabled)
		return
	}
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	release, err := s.relay.admitUpgrade(session.ID, cancel)
	if err != nil {
		upgradeProblem(w, err)
		return
	}
	defer release()
	token := r.Header.Get(TokenHeader)
	reader, writer := io.Pipe()
	defer func() { _ = writer.Close() }()
	out, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://bridge.invalid/", reader)
	if err != nil {
		upgradeProblem(w, err)
		return
	}
	out.URL.Path = "/" + r.PathValue("path")
	out.URL.RawPath = strings.TrimPrefix(r.URL.EscapedPath(), "/v1/dev/bridges/"+session.ID+"/traffic")
	out.URL.RawQuery = r.URL.RawQuery
	out.ContentLength = -1
	out.Header = r.Header.Clone()
	removeHopHeaders(out.Header)
	ClearCredentials(out.Header)
	ClearRequestContext(out.Header)
	out.Header.Set(ContextHeader, (RequestContext{session.Scope.AccountID, session.ID, token}).Encode())
	out.Header.Set(UpgradeHeader, upgradeWebSocket)
	stripDashboardCookie(out)
	response, connectionDone, err := s.relay.roundTripUpgrade(session.ID, out)
	if err != nil {
		upgradeProblem(w, err)
		return
	}
	defer func() { _ = response.Body.Close() }()
	header := response.Header.Clone()
	removeHopHeaders(header)
	ClearCredentials(header)
	if response.StatusCode != http.StatusOK || response.Header.Get(UpgradeHeader) != upgradeWebSocket {
		// The local process declined; relay its ordinary answer unchanged.
		for key, values := range header {
			w.Header()[key] = values
		}
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(response.StatusCode)
		_, _ = io.Copy(w, response.Body)
		return
	}
	conn, buffered, err := http.NewResponseController(w).Hijack()
	if err != nil {
		bridgeProblem(w, http.StatusInternalServerError, "dev_bridge_upgrade_failed")
		return
	}
	defer func() { _ = conn.Close() }()
	_, _ = io.WriteString(buffered, "HTTP/1.1 101 Switching Protocols\r\nConnection: Upgrade\r\nUpgrade: websocket\r\n")
	_ = header.Write(buffered)
	_, _ = io.WriteString(buffered, "\r\n")
	if err := buffered.Flush(); err != nil {
		return
	}
	stop := make(chan struct{})
	var stopOnce sync.Once
	watching := make(chan struct{})
	defer close(watching)
	go func() {
		select {
		case <-ctx.Done():
		case <-connectionDone:
		case <-watching:
		}
		stopOnce.Do(func() { close(stop) })
	}()
	go s.watchAuthorization(ctx, cancel, watching, session, func(current Session) error {
		return current.AuthorizeRequest(time.Now(), token, session.Scope.AccountID, session.Scope.EnvironmentID, session.Scope.TargetAppID)
	})
	tunnel(stop, s.relay.limitsForUpgrade(), func() { _ = conn.Close(); _ = writer.Close(); _ = response.Body.Close() },
		writer, buffered.Reader, conn, response.Body, nil)
}

// upgradeDependency lets the local process open a WebSocket to an allowed
// remote dependency. httputil.ReverseProxy performs the switch; the hijacked
// caller connection is bounded and closed on revocation or expiry.
func (s *Server) upgradeDependency(w http.ResponseWriter, r *http.Request, session Session) {
	if !IsWebSocketUpgrade(r) {
		upgradeProblem(w, ErrUpgradeDisabled)
		return
	}
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	limited := &limitedHijacker{ResponseWriter: w, limits: s.relay.limitsForUpgrade()}
	stop := func() { cancel(); limited.close() }
	release, err := s.relay.admitUpgrade(session.ID, stop)
	if err != nil {
		upgradeProblem(w, err)
		return
	}
	defer release()
	token := r.Header.Get(TokenHeader)
	app := r.PathValue("app")
	watching := make(chan struct{})
	defer close(watching)
	go s.watchAuthorization(ctx, stop, watching, session, func(current Session) error {
		return current.AuthorizeDependency(time.Now(), token, session.Scope.AccountID, session.Scope.EnvironmentID, app)
	})
	s.serveDependency(limited, r.WithContext(ctx), session)
}
