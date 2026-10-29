// Package liveproxy carries live-query frames between clients on realtime
// connections and the hubs that serve each database (ADR-20261016151947). realtimed
// owns client connections; each database's hub runs in livequeryd beside the
// database and opens seals itself, so live secrets never leave the database
// node. The proxy needs only routing: a seal's clear header names its key,
// and the session's router maps the key to the database and its hub. A
// realtime endpoint's router knows only the keys of databases bound to its
// app, so a seal for any other database is refused before it reaches a hub.
//
// One upstream WebSocket per database carries every client subscription to
// that database, each under an ID unique to its client session. The proxy
// rewrites IDs and versions on the way back, fans each round's progress out
// once per session, and when an upstream drops it marks the subscriptions
// unavailable, reconnects, and subscribes them again with their latest
// seals. Versions are offset so a client's sequence never goes backwards.
package liveproxy

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/livequery/liveproto"
	"github.com/onebox-faas/faas/pkg/livequery/seal"
)

// Route is where a database's live queries are served, and the logins the
// session's app reads as there ("" refuses that mode).
type Route struct {
	Database    string
	URL         string // the hub's WebSocket, ws://host/live
	SealedRole  string
	DataAPIRole string
	// Access is what the app's Data API runtime reported for reads without a
	// user token; nil refuses them.
	Access *api.DataAPIAccessConfig
}

// ErrNoRoute means no database is known for a seal's key.
var ErrNoRoute = errors.New("liveproxy: no route for the seal's key")

// RouteKey is what a subscription is routed by: a seal's key ID, or for an
// RLS-mode read the database it names.
type RouteKey struct {
	KID      string
	Database string
}

// Router finds the route for a key.
type Router func(ctx context.Context, key RouteKey) (Route, error)

// Proxy carries live frames for many client sessions.
type Proxy struct {
	dialer *websocket.Dialer
	log    *slog.Logger
	// Metrics, when set before the first Attach, records the proxy's work.
	Metrics *Metrics

	mu        sync.Mutex
	upstreams map[string]*upstream // by database
	nextID    atomic.Uint64
	closed    bool
}

// New creates a proxy. tlsConfig, when set, is used to dial wss:// hubs:
// its root CAs verify them and its certificate is presented to them.
func New(log *slog.Logger, tlsConfig *tls.Config) *Proxy {
	if log == nil {
		log = slog.Default()
	}
	return &Proxy{log: log, upstreams: map[string]*upstream{},
		dialer: &websocket.Dialer{HandshakeTimeout: 10 * time.Second, TLSClientConfig: tlsConfig}}
}

// Close drops every upstream.
func (p *Proxy) Close() {
	p.mu.Lock()
	p.closed = true
	ups := p.upstreams
	p.upstreams = map[string]*upstream{}
	p.mu.Unlock()
	for _, up := range ups {
		up.stop()
	}
}

// Session is one client connection's live queries.
type Session struct {
	proxy  *Proxy
	router Router
	id     uint64
	send   func(frame []byte) error

	mu     sync.Mutex
	subs   map[string]*sub // by client subscription ID
	closed bool
}

// sub is one client subscription and its upstream.
type sub struct {
	session *Session
	client  string
	up      *upstream
	// cred is the latest credential and query: a seal, or an RLS-mode read
	// with its verified claims. A resubscription sends it again.
	cred liveproto.ClientFrame
	// offset maps upstream versions to the client's: an upstream session
	// that starts over must not move the client's version backwards.
	offset    uint64
	version   uint64 // the client's current version
	attached  bool   // the upstream holds the subscription
	resubbing bool   // waiting for the upstream's first result after resubscribing
}

func (s *sub) upstreamID() string {
	return strconv.FormatUint(s.session.id, 10) + "-" + s.client
}

// Attach starts a client session that may reach the databases router
// routes to. send queues a frame to the client; it must be safe for
// concurrent use.
func (p *Proxy) Attach(router Router, send func(frame []byte) error) *Session {
	p.Metrics.addSessions(1)
	return &Session{proxy: p, router: router, id: p.nextID.Add(1), send: send, subs: map[string]*sub{}}
}

// Receive handles one client frame.
func (s *Session) Receive(ctx context.Context, data []byte) {
	f, err := liveproto.ParseClientFrame(data)
	if err != nil {
		s.deliver(&liveproto.Error{Type: liveproto.TypeError, Code: liveproto.CodeProtocol, Message: err.Error()})
		return
	}
	switch f.Type {
	case liveproto.TypeSubscribe:
		s.subscribe(ctx, f)
	case liveproto.TypeRenew:
		s.renew(ctx, f)
	case liveproto.TypeUnsubscribe:
		s.mu.Lock()
		sb := s.subs[f.Subscription]
		delete(s.subs, f.Subscription)
		s.mu.Unlock()
		if sb != nil {
			sb.up.forget(sb, true)
		}
	}
}

// Close ends the session and its subscriptions.
func (s *Session) Close() {
	s.mu.Lock()
	if !s.closed {
		s.proxy.Metrics.addSessions(-1)
	}
	s.closed = true
	subs := s.subs
	s.subs = map[string]*sub{}
	s.mu.Unlock()
	for _, sb := range subs {
		sb.up.forget(sb, true)
	}
}

func (s *Session) deliver(frame any) {
	s.mu.Lock()
	closed := s.closed
	s.mu.Unlock()
	if closed {
		return
	}
	data, err := json.Marshal(frame)
	if err != nil {
		s.proxy.log.Error("liveproxy: encode frame", "err", err)
		return
	}
	if err := s.send(data); err != nil {
		s.proxy.log.Debug("liveproxy: client send failed", "session", s.id, "err", err)
		return
	}
	s.proxy.Metrics.carried("to_client", len(data))
}

func (s *Session) refuse(id, code, msg string, closed bool) {
	s.proxy.Metrics.refused(code)
	s.deliver(&liveproto.Error{Type: liveproto.TypeError, Subscription: id, Code: code, Message: msg, Closed: closed})
}

func (s *Session) subscribe(ctx context.Context, f liveproto.ClientFrame) {
	route, code, msg := s.route(ctx, f)
	if code == "" {
		f.Role, code, msg = loginFor(route, f)
	}
	switch {
	case code == "" && keyless(f):
		code, msg = admitKeyless(route, &f, f.Rest, f.Schema, time.Now())
	case code == "" && f.RLS():
		// A renewal keeps the schema it subscribed, so subscribe is the
		// only place a token read chooses one.
		code, msg = admitTokenSchema(route, &f)
	}
	if code != "" {
		s.refuse(f.Subscription, code, msg, true)
		return
	}
	up, err := s.proxy.upstream(route) //nolint:contextcheck // the upstream outlives the client frame that dialed it
	if err != nil {
		s.refuse(f.Subscription, liveproto.CodeUnavailable, err.Error(), true)
		return
	}
	s.mu.Lock()
	if old := s.subs[f.Subscription]; old != nil {
		s.mu.Unlock()
		old.up.forget(old, true)
		s.mu.Lock()
	}
	if len(s.subs) >= api.LiveQuerySubscriptionsPerConnection {
		s.mu.Unlock()
		s.deliver(&liveproto.Error{Type: liveproto.TypeError, Subscription: f.Subscription, Code: liveproto.CodeLimit,
			Message: "too many live queries on this connection", Closed: true,
			Limit: api.LiveQuerySubscriptionsPerConnection, Observed: int64(api.LiveQuerySubscriptionsPerConnection + 1)})
		return
	}
	// A client resuming from a version continues above it.
	sb := &sub{session: s, client: f.Subscription, up: up, cred: credential(f), offset: f.Version, version: f.Version}
	s.subs[f.Subscription] = sb
	s.mu.Unlock()
	up.add(sb)
}

func (s *Session) renew(ctx context.Context, f liveproto.ClientFrame) {
	s.mu.Lock()
	sb := s.subs[f.Subscription]
	s.mu.Unlock()
	if sb == nil {
		s.refuse(f.Subscription, liveproto.CodeProtocol, "no such subscription", false)
		return
	}
	// A renewal may use a rotated key, but it must still route to the
	// subscription's database through this endpoint; an RLS-mode renewal
	// renews the read it subscribed.
	if f.RLS() {
		f.Database = sb.cred.Database
	}
	route, code, msg := s.route(ctx, f)
	if code == "" && route.Database != sb.up.route.Database {
		code, msg = liveproto.CodeForbidden, "the renewal seals a query for another database"
	}
	if code == "" {
		f.Role, code, msg = loginFor(route, f)
	}
	switch {
	case f.RLS() != (sb.cred.Sealed == ""):
		code, msg = liveproto.CodeRenewMismatch, "a renewal keeps the subscription's mode"
	case keyless(f) != (sb.cred.Access != "") || keyless(f) && keylessKind(f) != sb.cred.Access:
		code, msg = liveproto.CodeRenewMismatch, "a renewal keeps the subscription's access kind"
	case code == "" && keyless(f):
		f.Database = ""
		code, msg = admitKeyless(route, &f, sb.cred.Rest, sb.cred.Schema, time.Now())
	}
	if code != "" {
		s.refuse(f.Subscription, code, msg, false)
		return
	}
	sb.up.renew(sb, f)
}

// route finds where a subscription is served, or the live.error code
// refusing it.
func (s *Session) route(ctx context.Context, f liveproto.ClientFrame) (Route, string, string) {
	key := RouteKey{Database: f.Database}
	if !f.RLS() {
		if key.KID = seal.KeyID(f.Sealed); key.KID == "" {
			return Route{}, liveproto.CodeSealRejected, "malformed seal"
		}
		key.Database = ""
	}
	if s.router == nil {
		return Route{}, liveproto.CodeForbidden, "this endpoint serves no live database"
	}
	route, err := s.router(ctx, key)
	if err != nil {
		return Route{}, liveproto.CodeForbidden, "this endpoint's app is not bound to a live database for this query"
	}
	return route, "", ""
}

// loginFor is the login a query reads as: the endpoint app's own binding,
// never a role the client names.
func loginFor(route Route, f liveproto.ClientFrame) (string, string, string) {
	switch {
	case f.RLS() && route.DataAPIRole == "":
		return "", liveproto.CodeForbidden, "the endpoint's app has no Data API binding to this database"
	case f.RLS():
		return route.DataAPIRole, "", ""
	case route.SealedRole == "":
		return "", liveproto.CodeForbidden, "the endpoint's app has no read binding to this database"
	}
	return route.SealedRole, "", ""
}

// credential keeps what a subscribe or renew frame authorizes with.
func credential(f liveproto.ClientFrame) liveproto.ClientFrame {
	return liveproto.ClientFrame{Sealed: f.Sealed, Database: f.Database, Rest: f.Rest, Schema: f.Schema,
		Claims: f.Claims, ClaimsExpires: f.ClaimsExpires, Role: f.Role, Access: f.Access}
}

// upstream returns the database's upstream, dialing it when new.
func (p *Proxy) upstream(route Route) (*upstream, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return nil, errors.New("liveproxy: closed")
	}
	if up := p.upstreams[route.Database]; up != nil {
		up.retarget(route.URL)
		return up, nil
	}
	up := &upstream{proxy: p, route: route, subs: map[string]*sub{}, wake: make(chan struct{}, 1), done: make(chan struct{})}
	p.upstreams[route.Database] = up
	p.Metrics.addUpstreams(1)
	go up.run()
	return up, nil
}

// upstream is one WebSocket to a database's hub, redialed when it drops.
type upstream struct {
	proxy *Proxy
	route Route

	mu      sync.Mutex
	conn    *websocket.Conn
	subs    map[string]*sub // by upstream subscription ID
	stopped bool
	wake    chan struct{}
	done    chan struct{}
	once    sync.Once
}

// retarget points the upstream at a moved hub: the current connection is
// dropped, and the redial subscribes everything again at the new URL.
func (u *upstream) retarget(url string) {
	u.mu.Lock()
	defer u.mu.Unlock()
	if u.route.URL == url {
		return
	}
	u.proxy.log.Info("liveproxy: hub moved", "database", u.route.Database, "url", url)
	u.route.URL = url
	if u.conn != nil {
		_ = u.conn.Close()
	}
}

func (u *upstream) stop() {
	u.once.Do(func() {
		u.mu.Lock()
		u.stopped = true
		conn := u.conn
		u.mu.Unlock()
		if conn != nil {
			_ = conn.Close()
		}
		close(u.done)
		u.proxy.Metrics.addUpstreams(-1)
	})
}

// write sends a frame upstream if connected; reconnecting subscribes
// everything again.
func (u *upstream) write(frame liveproto.ClientFrame) {
	u.mu.Lock()
	conn := u.conn
	u.mu.Unlock()
	if conn == nil {
		return
	}
	data, _ := json.Marshal(frame)
	u.mu.Lock()
	err := conn.WriteMessage(websocket.TextMessage, data)
	u.mu.Unlock()
	if err != nil {
		_ = conn.Close() // the reader sees it and reconnects
		return
	}
	u.proxy.Metrics.carried("to_hub", len(data))
}

func (u *upstream) add(sb *sub) {
	u.mu.Lock()
	if _, ok := u.subs[sb.upstreamID()]; !ok {
		u.proxy.Metrics.addSubscriptions(1)
	}
	u.subs[sb.upstreamID()] = sb
	connected := u.conn != nil
	u.mu.Unlock()
	if connected {
		u.attach(sb)
	}
}

func (u *upstream) attach(sb *sub) {
	u.mu.Lock()
	sb.attached = true
	sb.resubbing = true
	frame := sb.cred
	u.mu.Unlock()
	frame.Type, frame.Subscription = liveproto.TypeSubscribe, sb.upstreamID()
	u.write(frame)
}

func (u *upstream) renew(sb *sub, f liveproto.ClientFrame) {
	u.mu.Lock()
	if f.RLS() {
		sb.cred.Claims, sb.cred.ClaimsExpires, sb.cred.Access = f.Claims, f.ClaimsExpires, f.Access
	} else {
		sb.cred.Sealed = f.Sealed
	}
	sb.cred.Role = f.Role
	attached := sb.attached
	connected := u.conn != nil
	u.mu.Unlock()
	switch {
	case !connected:
		// Kept for the resubscription.
	case attached:
		u.write(liveproto.ClientFrame{Type: liveproto.TypeRenew, Subscription: sb.upstreamID(), Sealed: f.Sealed,
			Claims: f.Claims, ClaimsExpires: f.ClaimsExpires, Role: f.Role, Access: f.Access})
	default:
		// The upstream refused the last seal (it had expired across a
		// reconnect): subscribe with the fresh one.
		u.attach(sb)
	}
}

func (u *upstream) forget(sb *sub, unsubscribe bool) {
	u.mu.Lock()
	_, ok := u.subs[sb.upstreamID()]
	delete(u.subs, sb.upstreamID())
	if ok {
		u.proxy.Metrics.addSubscriptions(-1)
	}
	attached := sb.attached
	u.mu.Unlock()
	if ok && unsubscribe && attached {
		u.write(liveproto.ClientFrame{Type: liveproto.TypeUnsubscribe, Subscription: sb.upstreamID()})
	}
}

// run keeps the upstream connected until stopped.
func (u *upstream) run() {
	for attempt := 0; ; attempt++ {
		select {
		case <-u.done:
			return
		default:
		}
		u.mu.Lock()
		target := u.route.URL
		u.mu.Unlock()
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		conn, resp, err := u.proxy.dialer.DialContext(ctx, target, nil)
		cancel()
		if resp != nil {
			_ = resp.Body.Close()
		}
		if err != nil {
			u.proxy.log.Warn("liveproxy: upstream unavailable", "database", u.route.Database, "err", err)
			u.unavailable("the live query service is unreachable")
			select {
			case <-u.done:
				return
			case <-time.After(backoff(attempt)):
			}
			continue
		}
		conn.SetReadLimit(api.LiveQueryServerFrameMaxBytes * 2)
		u.mu.Lock()
		u.conn = conn
		subs := make([]*sub, 0, len(u.subs))
		for _, sb := range u.subs {
			subs = append(subs, sb)
		}
		u.mu.Unlock()
		attempt = -1
		for _, sb := range subs {
			u.attach(sb)
		}
		u.read(conn)
		u.mu.Lock()
		u.conn = nil
		for _, sb := range u.subs {
			sb.attached = false
		}
		stopped := u.stopped
		u.mu.Unlock()
		if stopped {
			return
		}
		u.proxy.Metrics.upstreamDropped()
		u.unavailable("the live query service connection dropped; resuming")
	}
}

func backoff(attempt int) time.Duration {
	d := 100 * time.Millisecond << min(attempt, 8)
	return min(d, 15*time.Second)
}

// unavailable marks every subscription stale until its upstream answers.
func (u *upstream) unavailable(msg string) {
	u.mu.Lock()
	subs := make([]*sub, 0, len(u.subs))
	for _, sb := range u.subs {
		subs = append(subs, sb)
	}
	u.mu.Unlock()
	for _, sb := range subs {
		sb.session.refuse(sb.client, liveproto.CodeUnavailable, msg, false)
	}
}

func (u *upstream) read(conn *websocket.Conn) {
	defer func() { _ = conn.Close() }()
	for {
		_, data, err := conn.ReadMessage()
		if err != nil {
			return
		}
		frame, err := liveproto.ParseServerFrame(data)
		if err != nil {
			u.proxy.log.Warn("liveproxy: bad upstream frame", "database", u.route.Database, "err", err)
			continue
		}
		u.dispatch(frame)
	}
}

// dispatch rewrites an upstream frame for its client.
func (u *upstream) dispatch(frame any) {
	if p, ok := frame.(*liveproto.Progress); ok {
		u.mu.Lock()
		sessions := map[*Session]bool{}
		for _, sb := range u.subs {
			sessions[sb.session] = true
		}
		u.mu.Unlock()
		for s := range sessions {
			s.deliver(p)
		}
		return
	}
	id := frameSubscription(frame)
	u.mu.Lock()
	sb := u.subs[id]
	if sb == nil {
		u.mu.Unlock()
		return
	}
	switch f := frame.(type) {
	case *liveproto.Rows:
		if sb.resubbing {
			// The upstream's sequence starts over: continue above the
			// client's version.
			sb.offset = sb.version
			sb.resubbing = false
		}
		f.Version += sb.offset
		if f.Complete {
			sb.version = f.Version
		}
		f.Subscription = sb.client
	case *liveproto.Changes:
		f.Base += sb.offset
		f.Version += sb.offset
		sb.version = f.Version
		f.Subscription = sb.client
	case *liveproto.Renewed:
		f.Subscription = sb.client
	case *liveproto.Error:
		f.Subscription = sb.client
		if f.Closed {
			delete(u.subs, id)
			sb.attached = false
			// A seal that expired while the upstream was away: the client
			// renews and the proxy subscribes again.
			if f.Code == liveproto.CodeSealExpired {
				u.subs[id] = sb
				f.Closed = false
			} else {
				sb.session.mu.Lock()
				delete(sb.session.subs, sb.client)
				sb.session.mu.Unlock()
				u.proxy.Metrics.addSubscriptions(-1)
			}
		}
	}
	session := sb.session
	u.mu.Unlock()
	session.deliver(frame)
}

func frameSubscription(frame any) string {
	switch f := frame.(type) {
	case *liveproto.Rows:
		return f.Subscription
	case *liveproto.Changes:
		return f.Subscription
	case *liveproto.Renewed:
		return f.Subscription
	case *liveproto.Error:
		return f.Subscription
	}
	return ""
}

// StaticRouter routes from a fixed table of key IDs; an RLS-mode read
// routes to any of the table's routes for its database.
func StaticRouter(routes map[string]Route) Router {
	return func(_ context.Context, key RouteKey) (Route, error) {
		if r, ok := routes[key.KID]; ok && key.KID != "" {
			return r, nil
		}
		for _, r := range routes {
			if key.KID == "" && key.Database != "" && r.Database == key.Database {
				return r, nil
			}
		}
		return Route{}, fmt.Errorf("%w %+v", ErrNoRoute, key)
	}
}
