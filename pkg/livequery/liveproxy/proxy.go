// Package liveproxy carries live-query frames between clients on realtime
// connections and the hubs that serve each database (ADR-1384). realtimed
// owns client connections; each database's hub runs in livequeryd beside the
// database and opens seals itself, so live secrets never leave the database
// node. The proxy needs only routing: a seal's clear header names its key,
// and the router maps the key to the database and its hub.
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
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/livequery/liveproto"
)

// Route is where a database's live queries are served.
type Route struct {
	Database string
	URL      string // the hub's WebSocket, ws://host/live
}

// ErrNoRoute means no database is known for a seal's key.
var ErrNoRoute = errors.New("liveproxy: no route for the seal's key")

// Router finds the route for a seal's key ID.
type Router func(ctx context.Context, kid string) (Route, error)

// Proxy carries live frames for many client sessions.
type Proxy struct {
	router Router
	dialer *websocket.Dialer
	log    *slog.Logger

	mu        sync.Mutex
	upstreams map[string]*upstream // by database
	nextID    atomic.Uint64
	closed    bool
}

// New creates a proxy.
func New(router Router, log *slog.Logger) *Proxy {
	if log == nil {
		log = slog.Default()
	}
	return &Proxy{router: router, log: log, upstreams: map[string]*upstream{},
		dialer: &websocket.Dialer{HandshakeTimeout: 10 * time.Second}}
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
	proxy *Proxy
	id    uint64
	send  func(frame []byte) error

	mu     sync.Mutex
	subs   map[string]*sub // by client subscription ID
	closed bool
}

// sub is one client subscription and its upstream.
type sub struct {
	session *Session
	client  string
	up      *upstream
	sealed  string
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

// Attach starts a client session. send queues a frame to the client; it must
// be safe for concurrent use.
func (p *Proxy) Attach(send func(frame []byte) error) *Session {
	return &Session{proxy: p, id: p.nextID.Add(1), send: send, subs: map[string]*sub{}}
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
	}
}

func (s *Session) refuse(id, code, msg string, closed bool) {
	s.deliver(&liveproto.Error{Type: liveproto.TypeError, Subscription: id, Code: code, Message: msg, Closed: closed})
}

func (s *Session) subscribe(ctx context.Context, f liveproto.ClientFrame) {
	kid, ok := sealKID(f.Sealed)
	if !ok {
		s.refuse(f.Subscription, liveproto.CodeSealRejected, "malformed seal", true)
		return
	}
	route, err := s.proxy.router(ctx, kid)
	if err != nil {
		s.refuse(f.Subscription, liveproto.CodeForbidden, "no live database for this seal", true)
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
	sb := &sub{session: s, client: f.Subscription, up: up, sealed: f.Sealed, offset: f.Version, version: f.Version}
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
	sb.up.renew(sb, f.Sealed)
}

// sealKID reads the key ID from a seal's clear header, gls1.<kid>.<body>.
func sealKID(sealed string) (string, bool) {
	parts := strings.SplitN(sealed, ".", 3)
	if len(parts) != 3 || parts[0] != "gls1" || len(parts[1]) != 16 {
		return "", false
	}
	return parts[1], true
}

// upstream returns the database's upstream, dialing it when new.
func (p *Proxy) upstream(route Route) (*upstream, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return nil, errors.New("liveproxy: closed")
	}
	if up := p.upstreams[route.Database]; up != nil {
		return up, nil
	}
	up := &upstream{proxy: p, route: route, subs: map[string]*sub{}, wake: make(chan struct{}, 1), done: make(chan struct{})}
	p.upstreams[route.Database] = up
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
	}
}

func (u *upstream) add(sb *sub) {
	u.mu.Lock()
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
	sealed := sb.sealed
	u.mu.Unlock()
	u.write(liveproto.ClientFrame{Type: liveproto.TypeSubscribe, Subscription: sb.upstreamID(), Sealed: sealed})
}

func (u *upstream) renew(sb *sub, sealed string) {
	u.mu.Lock()
	sb.sealed = sealed
	attached := sb.attached
	connected := u.conn != nil
	u.mu.Unlock()
	switch {
	case !connected:
		// Kept for the resubscription.
	case attached:
		u.write(liveproto.ClientFrame{Type: liveproto.TypeRenew, Subscription: sb.upstreamID(), Sealed: sealed})
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
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		conn, resp, err := u.proxy.dialer.DialContext(ctx, u.route.URL, nil)
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

// StaticRouter routes from a fixed table of key IDs.
func StaticRouter(routes map[string]Route) Router {
	return func(_ context.Context, kid string) (Route, error) {
		r, ok := routes[kid]
		if !ok {
			return Route{}, fmt.Errorf("%w %s", ErrNoRoute, kid)
		}
		return r, nil
	}
}
