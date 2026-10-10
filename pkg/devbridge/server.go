package devbridge

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httputil"
	"strings"
	"time"

	"github.com/gorilla/websocket"
)

const AccountHeader = "X-Gregale-Dev-Bridge-Account"

type LookupSession func(context.Context, string, string) (Session, error)

// Server is the unprivileged relay's HTTP surface. The edge/apid can proxy the
// same endpoints; credentials are checked here again against durable state.
type Server struct {
	relay        *Relay
	lookup       LookupSession
	dependencies http.Handler
	mux          *http.ServeMux
}

func NewServer(relay *Relay, lookup LookupSession, dependencies http.Handler) *Server {
	s := &Server{relay: relay, lookup: lookup, dependencies: dependencies, mux: http.NewServeMux()}
	s.mux.HandleFunc("GET /v1/dev/bridges/{id}/connect", s.connect)
	s.mux.HandleFunc("GET /v1/dev/bridges/{id}/status", s.status)
	s.mux.HandleFunc("/v1/dev/bridges/{id}/traffic/{path...}", s.traffic)
	s.mux.HandleFunc("/v1/dev/bridges/{id}/dependencies/{app}/{path...}", s.dependency)
	return s
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) { s.mux.ServeHTTP(w, r) }

func (s *Server) status(w http.ResponseWriter, r *http.Request) {
	session, err := s.session(r)
	if err != nil || session.AuthorizeAttachment(time.Now(), r.Header.Get(TokenHeader)) != nil {
		bridgeProblem(w, 403, "dev_bridge_unauthorized")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(map[string]bool{"connected": s.relay.Connected(session.ID)})
}

func bridgeProblem(w http.ResponseWriter, status int, code string) {
	w.Header().Set("Content-Type", "application/problem+json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"type": "about:blank", "title": http.StatusText(status), "status": status, "code": code})
}

func (s *Server) session(r *http.Request) (Session, error) {
	for _, key := range []string{AccountHeader, SessionHeader, TokenHeader} {
		if len(r.Header.Values(key)) > 1 {
			return Session{}, ErrUnauthorized
		}
	}
	if len(r.PathValue("id")) != 43 || len(r.Header.Get(AccountHeader)) > 64 || len(r.Header.Get(TokenHeader)) > 128 {
		return Session{}, ErrUnauthorized
	}
	return s.lookup(r.Context(), r.Header.Get(AccountHeader), r.PathValue("id"))
}

func (s *Server) connect(w http.ResponseWriter, r *http.Request) {
	session, err := s.session(r)
	if err != nil || session.AuthorizeAttachment(time.Now(), r.Header.Get(TokenHeader)) != nil {
		bridgeProblem(w, 403, "dev_bridge_unauthorized")
		return
	}
	// Browser connections cannot attach a laptop or replace its owner.
	if r.Header.Get("Origin") != "" {
		bridgeProblem(w, 403, "dev_bridge_unauthorized")
		return
	}
	socket, err := (&websocket.Upgrader{Subprotocols: []string{"gregale-dev-bridge-v1"}}).Upgrade(w, r, nil)
	if err != nil {
		return
	}
	_ = socket.UnderlyingConn().SetDeadline(time.Time{})
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	done := make(chan struct{})
	token := r.Header.Get(TokenHeader)
	go s.watchAuthorization(ctx, cancel, done, session, func(current Session) error {
		return current.AuthorizeAttachment(time.Now(), token)
	})
	_ = s.relay.Attach(ctx, session, NewWebSocketConn(socket))
	close(done)
}

// Revocation by any apid replica also closes idle laptop connections and
// long-lived upgraded sockets; authorize repeats the entry check.
func (s *Server) watchAuthorization(ctx context.Context, cancel func(), done <-chan struct{}, session Session, authorize func(Session) error) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-done:
			return
		case <-ctx.Done():
			return
		case <-ticker.C:
			current, err := s.lookup(ctx, session.Scope.AccountID, session.ID)
			if err != nil || authorize(current) != nil {
				cancel()
				return
			}
		}
	}
}

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func (s *Server) traffic(w http.ResponseWriter, r *http.Request) {
	session, err := s.session(r)
	if err != nil || session.AuthorizeRequest(time.Now(), r.Header.Get(TokenHeader), session.Scope.AccountID, session.Scope.EnvironmentID, session.Scope.TargetAppID) != nil {
		bridgeProblem(w, 403, "dev_bridge_unauthorized")
		return
	}
	if IsUpgradeRequest(r) {
		s.upgradeTraffic(w, r, session)
		return
	}
	proxy := &httputil.ReverseProxy{
		Rewrite: func(p *httputil.ProxyRequest) {
			p.Out.URL.Scheme = "https"
			p.Out.URL.Host = "bridge.invalid"
			p.Out.URL.Path = "/" + r.PathValue("path")
			p.Out.URL.RawPath = strings.TrimPrefix(r.URL.EscapedPath(), "/v1/dev/bridges/"+session.ID+"/traffic")
			ClearCredentials(p.Out.Header)
			ClearRequestContext(p.Out.Header)
			p.Out.Header.Set(ContextHeader, (RequestContext{session.Scope.AccountID, session.ID, r.Header.Get(TokenHeader)}).Encode())
			stripDashboardCookie(p.Out)
		},
		Transport: roundTripperFunc(func(request *http.Request) (*http.Response, error) { return s.relay.RoundTrip(session.ID, request) }),
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			bridgeProblem(w, 503, "dev_bridge_disconnected")
		},
		FlushInterval: -1,
	}
	_ = http.NewResponseController(w).EnableFullDuplex()
	proxy.ServeHTTP(w, r)
}

func (s *Server) dependency(w http.ResponseWriter, r *http.Request) {
	session, err := s.session(r)
	if err != nil || session.AuthorizeDependency(time.Now(), r.Header.Get(TokenHeader), session.Scope.AccountID, session.Scope.EnvironmentID, r.PathValue("app")) != nil {
		bridgeProblem(w, 403, "dev_bridge_dependency_denied")
		return
	}
	if value := r.Header.Get(ContextHeader); value != "" {
		c, err := ParseRequestContext(value)
		if err != nil || len(r.Header.Values(ContextHeader)) != 1 || session.AuthorizeContextRoute(time.Now(), c, session.Scope.EnvironmentID, r.PathValue("app")) != nil {
			bridgeProblem(w, 403, "dev_bridge_dependency_denied")
			return
		}
	}
	if s.dependencies == nil {
		bridgeProblem(w, 503, "dev_bridge_dependency_unavailable")
		return
	}
	if IsUpgradeRequest(r) {
		s.upgradeDependency(w, r, session)
		return
	}
	s.serveDependency(w, r, session)
}

func (s *Server) serveDependency(w http.ResponseWriter, r *http.Request, session Session) {
	clone := r.Clone(r.Context())
	clone.URL.Path = "/" + r.PathValue("path")
	clone.URL.RawPath = strings.TrimPrefix(r.URL.EscapedPath(), "/v1/dev/bridges/"+session.ID+"/dependencies/"+r.PathValue("app"))
	ClearCredentials(clone.Header)
	stripDashboardCookie(clone)
	// Only this verified relay handler publishes the dependency identities.
	clone.Header.Set("X-Gregale-Dev-Environment", session.Scope.EnvironmentID)
	clone.Header.Set("X-Gregale-Dev-Dependency", r.PathValue("app"))
	clone.Header.Set("X-Gregale-Dev-Account", session.Scope.AccountID)
	clone.Header.Set(SessionHeader, session.ID)
	clone.Header.Set(TokenHeader, r.Header.Get(TokenHeader))
	clone.Header.Set(AccountHeader, session.Scope.AccountID)
	s.dependencies.ServeHTTP(w, clone)
}

// The relay's public API path shares an origin with the dashboard. Keep its
// session cookie off developer machines while retaining application cookies
// and application Authorization headers needed for local authentication.
func stripDashboardCookie(r *http.Request) {
	cookies := r.Cookies()
	r.Header.Del("Cookie")
	for _, cookie := range cookies {
		if cookie.Name != "faas_sid" {
			r.AddCookie(cookie)
		}
	}
}

// ClearCredentials removes bridge credentials before a normal application hop.
func ClearCredentials(h http.Header) {
	for key := range h {
		if strings.HasPrefix(strings.ToLower(key), "x-gregale-dev-bridge-") {
			delete(h, key)
		}
	}
}

func ClearRequestContext(h http.Header) {
	for key := range h {
		if strings.EqualFold(key, ContextHeader) {
			delete(h, key)
		}
	}
}
