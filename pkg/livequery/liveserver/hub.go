// Package liveserver serves live queries to clients (ADR-20261016151947): the realtimed
// half of the frame contract in pkg/livequery/liveproto, over one database's
// engine. A Hub owns the engine and runs its rounds; each client connection
// is a Session. realtimed embeds a Hub per database it serves; livequeryd's
// development server exposes one over a WebSocket.
//
// The engine is not safe for concurrent use, so every engine call runs on
// the hub's loop goroutine: sessions hand their frames to the loop, and the
// loop hands frames back to each session's writer.
package liveserver

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log/slog"
	"strconv"
	"sync/atomic"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/livequery"
	"github.com/onebox-faas/faas/pkg/livequery/liveproto"
	"github.com/onebox-faas/faas/pkg/livequery/rest"
	"github.com/onebox-faas/faas/pkg/livequery/seal"
)

// Config configures a Hub.
type Config struct {
	// Database is the database the engine maintains; seals must name it.
	Database string
	// Keyring opens seals; SetKeyring replaces it while the hub runs. Nil
	// opens none.
	Keyring *seal.Keyring
	Engine  *livequery.Engine
	// Checkpoint, when set, is the file the engine checkpoints to every
	// CheckpointEvery (default one minute) and when Run returns.
	Checkpoint      string
	CheckpointEvery time.Duration
	// Idle is how often an idle hub checks the slot for new transactions
	// (default 20 ms).
	Idle    time.Duration
	Now     func() time.Time
	Metrics *Metrics
	Logger  *slog.Logger
	// Limits; zero means the pkg/api default.
	MaxSubscriptions, MaxQueries, MaxPerConnection, QueueFrames int
	// Pool is what RLS-mode Data API reads are compiled against (nil
	// disables RLS mode). MaxRLSSubscriptions bounds RLS-mode
	// subscriptions, which share maintenance only with identical claims.
	Pool                *pgxpool.Pool
	MaxRLSSubscriptions int
	// Scale to zero (release.go). After ReleaseAfter without a transaction
	// to apply, the hub releases the database; zero never releases.
	// Reconnect opens the engine's slot again (livequery.OpenSlot). Awake,
	// polled every AwakeEvery (default five seconds) while released,
	// reports whether the database is running again; without it only Wake
	// and subscriptions that need the database resume the hub.
	ReleaseAfter time.Duration
	Reconnect    func(ctx context.Context) (*livequery.Capture, error)
	Awake        func(ctx context.Context) (bool, error)
	AwakeEvery   time.Duration
	// RestoredGrace is how long subscriptions restored from a checkpoint
	// stay registered (default two minutes). Their connections are gone,
	// but reconnecting clients that subscribe to the same queries join the
	// results they keep current and receive them at once.
	RestoredGrace time.Duration
}

// Hub serves one database's live queries.
type Hub struct {
	cfg      Config
	keys     *seal.Keyring
	engine   *livequery.Engine
	cmds     chan func(ctx context.Context)
	subs     map[string]*sub // by engine subscription ID
	sessions map[*Session]bool
	nextID   uint64
	dirty    bool          // registrations changed since the last round
	done     chan struct{} // closed when Run returns
	prefix   string        // of this hub's engine subscription IDs
	rlsSubs  int           // RLS-mode subscriptions
	// Scale to zero: when the database last gave the hub work, and whether
	// the hub has released it (read by other goroutines through Released).
	lastActive time.Time
	released   atomic.Bool
}

// sub is one client subscription.
type sub struct {
	session     *Session
	client      string                // the client's subscription ID
	kid         string                // key ID of the latest seal
	rls         bool                  // an RLS-mode read, authorized by claims, not a seal
	rest        liveproto.ClientFrame // an RLS-mode read: its database, schema and read
	engineID    string
	fingerprint string
	expires     time.Time
	version     uint64
	ready       bool // the client has a full result
	stale       bool // the seal lapsed: nothing is sent until renewal
}

// New creates a hub. Run must be running for sessions to be served.
func New(cfg Config) *Hub {
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	if cfg.Idle <= 0 {
		cfg.Idle = 20 * time.Millisecond
	}
	if cfg.CheckpointEvery <= 0 {
		cfg.CheckpointEvery = time.Minute
	}
	def := func(v *int, d int) {
		if *v <= 0 {
			*v = d
		}
	}
	def(&cfg.MaxSubscriptions, api.LiveQuerySubscriptionsPerDatabase)
	def(&cfg.MaxQueries, api.LiveQueryCompiledQueriesPerDatabase)
	def(&cfg.MaxPerConnection, api.LiveQuerySubscriptionsPerConnection)
	def(&cfg.QueueFrames, api.LiveQueryConnectionQueueFrames)
	def(&cfg.MaxRLSSubscriptions, api.LiveQueryRLSSubscriptionsPerDatabase)
	if cfg.RestoredGrace <= 0 {
		cfg.RestoredGrace = 2 * time.Minute
	}
	if cfg.AwakeEvery <= 0 {
		cfg.AwakeEvery = 5 * time.Second
	}
	// Engine subscription IDs must not repeat those a checkpoint restored
	// from an earlier process, so each hub prefixes its own.
	prefix := make([]byte, 6)
	_, _ = rand.Read(prefix) // crypto/rand.Read never fails
	keys := cfg.Keyring
	if keys == nil {
		keys, _ = seal.NewKeyring() // an empty ring never fails
	}
	return &Hub{cfg: cfg, keys: keys, engine: cfg.Engine, cmds: make(chan func(context.Context), 256), subs: map[string]*sub{},
		sessions: map[*Session]bool{}, done: make(chan struct{}), prefix: hex.EncodeToString(prefix) + "."}
}

// do runs f on the loop goroutine, with the loop's context. It gives up
// when ctx ends or the hub has stopped.
func (h *Hub) do(ctx context.Context, f func(context.Context)) {
	select {
	case h.cmds <- f:
	case <-ctx.Done():
	case <-h.done:
	}
}

// Run maintains the live queries until ctx ends. It returns the error that
// stopped the engine, or nil on cancellation after a final checkpoint.
func (h *Hub) Run(ctx context.Context) error {
	defer close(h.done)
	if h.cfg.ReleaseAfter > 0 && h.cfg.Reconnect == nil {
		return errReleaseConfig
	}
	h.lastActive = h.cfg.Now()
	idle := time.NewTicker(h.cfg.Idle)
	defer idle.Stop()
	awake := time.NewTicker(h.cfg.AwakeEvery)
	defer awake.Stop()
	checkpoint := time.NewTicker(h.cfg.CheckpointEvery)
	defer checkpoint.Stop()
	// Subscriptions restored from a checkpoint belong to no session.
	restored := h.engine.SubscriptionIDs()
	release := time.NewTimer(h.cfg.RestoredGrace)
	defer release.Stop()
	if len(restored) == 0 {
		release.Stop()
	}
	failures := 0
	for {
		select {
		case <-ctx.Done():
			h.shutdown()
			return h.checkpoint()
		case f := <-h.cmds:
			f(ctx)
			continue
		case <-checkpoint.C:
			if h.released.Load() {
				break // nothing changed since the checkpoint written at release
			}
			if err := h.checkpoint(); err != nil {
				h.cfg.Logger.Error("live query checkpoint failed", "err", err)
			}
		case <-awake.C:
			h.probe(ctx)
		case <-release.C:
			for _, id := range restored {
				if h.subs[id] == nil {
					_ = h.engine.Unregister(id)
				}
			}
			h.cfg.Logger.Info("released subscriptions restored from the checkpoint", "count", len(restored))
			restored = nil
			h.dirty = true
		case <-idle.C:
		}
		h.expire()
		if h.released.Load() {
			continue // no rounds until something resumes the hub
		}
		if !h.dirty && !h.engine.Pending() {
			if h.idleLongEnough() {
				h.release() //nolint:contextcheck // Capture.Close bounds its own shutdown
			}
			continue
		}
		if err := h.round(ctx); err != nil {
			if ctx.Err() != nil {
				continue
			}
			failures++
			h.cfg.Logger.Error("live query round failed", "err", err, "failures", failures)
			if errors.Is(err, livequery.ErrCaptureStopped) || failures >= 10 {
				h.shutdown()
				return err
			}
			time.Sleep(time.Duration(failures) * 100 * time.Millisecond)
			continue
		}
		failures = 0
	}
}

func (h *Hub) checkpoint() error {
	if h.cfg.Checkpoint == "" {
		return nil
	}
	start := h.cfg.Now()
	if err := h.engine.WriteCheckpointFile(h.cfg.Checkpoint); err != nil {
		return err
	}
	h.cfg.Metrics.observeCheckpoint(h.cfg.Now().Sub(start))
	return nil
}

// round runs one engine round and sends its frames.
func (h *Hub) round(ctx context.Context) error {
	before := h.engine.Stats
	start := h.cfg.Now()
	h.dirty = false
	deltas, err := h.engine.Round(ctx)
	if err != nil {
		return err
	}
	now := h.cfg.Now()
	if len(h.engine.LastApplied) > 0 || len(deltas) > 0 {
		h.lastActive = now
	}
	h.cfg.Logger.Debug("live round", "deltas", len(deltas), "applied", len(h.engine.LastApplied), "took", now.Sub(start))
	h.cfg.Metrics.observeRound(now.Sub(start), before, h.engine.Stats, h.engine.Subscriptions(), h.engine.Queries())
	for _, d := range deltas {
		s := h.subs[d.Query]
		if s == nil {
			continue
		}
		if d.Closed != nil {
			s.session.send(&liveproto.Error{Type: liveproto.TypeError, Subscription: s.client, Code: d.Closed.Code,
				Message: d.Closed.Message, Closed: true, Limit: d.Closed.Limit, Observed: d.Closed.Observed})
			h.forget(s, false)
			continue
		}
		if s.stale {
			continue
		}
		h.deliver(s, d)
	}
	for _, txn := range h.engine.LastApplied {
		h.cfg.Metrics.observeCommitToDelta(now.Sub(txn.CommitTime))
	}
	if snap := h.engine.LastSnapshot(); snap != "" {
		for sess := range h.sessions {
			if sess.live() {
				sess.send(&liveproto.Progress{Type: liveproto.TypeProgress, Database: h.cfg.Database, Snapshot: snap})
			}
		}
	}
	return nil
}

// deliver sends a delta to its subscriber as frames.
func (h *Hub) deliver(s *sub, d livequery.Delta) {
	frames, err := d.Frames(h.cfg.Database, s.version, s.expires.Unix())
	if err == nil && oversized(frames) {
		// One round's changes cannot be split without showing a half-applied
		// snapshot: send the whole result, which can be.
		if cur, ok := h.engine.Current(s.engineID); ok {
			frames, err = cur.Frames(h.cfg.Database, s.version, s.expires.Unix())
		}
	}
	if err != nil {
		refusal := &liveproto.Error{Type: liveproto.TypeError, Subscription: s.client, Code: liveproto.CodeLimit,
			Message: err.Error(), Closed: true}
		if limit := (*liveproto.LimitError)(nil); errors.As(err, &limit) {
			refusal.Limit, refusal.Observed = limit.Limit, limit.Observed
		}
		s.session.send(refusal)
		h.forget(s, true)
		return
	}
	for _, f := range frames {
		if c, ok := f.(*liveproto.Changes); ok {
			c.Subscription = s.client
		}
		if r, ok := f.(*liveproto.Rows); ok {
			r.Subscription = s.client
			s.ready = true
		}
		s.session.send(f)
	}
	if len(frames) > 0 {
		s.version++
	}
}

// expire turns subscriptions whose seal lapsed stale.
func (h *Hub) expire() {
	now := h.cfg.Now()
	for _, s := range h.subs {
		if !s.stale && !now.Before(s.expires) {
			s.stale = true
			s.session.send(&liveproto.Error{Type: liveproto.TypeError, Subscription: s.client, Code: liveproto.CodeSealExpired,
				Message: "the sealed query expired; renew it to keep receiving changes"})
		}
	}
}

// SetKeyring replaces the key ring. Subscriptions whose latest seal used a
// key the new ring no longer opens are closed at once: removing a key (a
// rotation without overlap, disabling live queries, or a retirement) revokes
// its outstanding seals.
func (h *Hub) SetKeyring(ctx context.Context, ring *seal.Keyring) {
	if ring == nil {
		ring, _ = seal.NewKeyring() // an empty ring never fails
	}
	h.do(ctx, func(context.Context) {
		h.keys = ring
		now := h.cfg.Now()
		for _, s := range h.subs {
			if s.session == nil || s.rls || h.keys.Opens(s.kid, now) {
				continue
			}
			s.session.send(&liveproto.Error{Type: liveproto.TypeError, Subscription: s.client, Code: liveproto.CodeSealRejected,
				Message: "the live secret that sealed this query was revoked", Closed: true})
			h.forget(s, true)
		}
	})
}

// forget removes a subscription; with unregister it also leaves the engine.
func (h *Hub) forget(s *sub, unregister bool) {
	if unregister {
		_ = h.engine.Unregister(s.engineID)
	}
	if s.rls && h.subs[s.engineID] == s {
		h.rlsSubs--
	}
	delete(h.subs, s.engineID)
	delete(s.session.subs, s.client)
	h.dirty = true
}

func (h *Hub) shutdown() {
	for sess := range h.sessions {
		sess.stop()
	}
}

// handle runs a client frame on the loop.
func (h *Hub) handle(ctx context.Context, sess *Session, data []byte) {
	f, err := liveproto.ParseClientFrame(data)
	if err != nil {
		sess.send(&liveproto.Error{Type: liveproto.TypeError, Code: liveproto.CodeProtocol, Message: err.Error()})
		return
	}
	switch f.Type {
	case liveproto.TypeSubscribe:
		h.subscribe(ctx, sess, f)
	case liveproto.TypeRenew:
		h.renew(sess, f)
	case liveproto.TypeUnsubscribe:
		if s := sess.subs[f.Subscription]; s != nil {
			h.forget(s, true)
		}
	}
}

func (h *Hub) subscribe(ctx context.Context, sess *Session, f liveproto.ClientFrame) {
	refuse := func(code, msg string, limit, observed int) {
		sess.send(&liveproto.Error{Type: liveproto.TypeError, Subscription: f.Subscription, Code: code, Message: msg,
			Closed: true, Limit: int64(limit), Observed: int64(observed)})
	}
	if f.RLS() {
		h.subscribeRLS(ctx, sess, f, refuse)
		return
	}
	q, err := h.keys.Open(f.Sealed, h.cfg.Now())
	if err != nil {
		refuse(seal.Code(err), err.Error(), 0, 0)
		return
	}
	if q.Database != h.cfg.Database {
		refuse(liveproto.CodeForbidden, "the seal names another database", 0, 0)
		return
	}
	session, no := sealedSession(sess, f)
	if no != nil {
		refuse(no.code, no.msg, 0, 0)
		return
	}
	h.register(ctx, sess, f, q.SQL, q.Args(), session, q.Fingerprint(), q.ExpiresAt(), refuse)
}

// register subscribes a client to sql, read as session (nil: sealed mode).
func (h *Hub) register(ctx context.Context, sess *Session, f liveproto.ClientFrame, sql string, args []any,
	session *livequery.Session, fingerprint string, expires time.Time, refuse func(code, msg string, limit, observed int)) {
	// A subscription ID used again replaces the earlier subscription.
	if old := sess.subs[f.Subscription]; old != nil {
		h.forget(old, true)
	}
	switch {
	case len(sess.subs) >= h.cfg.MaxPerConnection:
		refuse(liveproto.CodeLimit, "too many live queries on this connection", h.cfg.MaxPerConnection, len(sess.subs)+1)
		return
	case h.engine.Subscriptions() >= h.cfg.MaxSubscriptions:
		refuse(liveproto.CodeLimit, "too many live queries on this database", h.cfg.MaxSubscriptions, h.engine.Subscriptions()+1)
		return
	case !h.engine.Compiled(sql) && h.engine.Queries() >= h.cfg.MaxQueries:
		refuse(liveproto.CodeLimit, "too many distinct live queries on this database", h.cfg.MaxQueries, h.engine.Queries()+1)
		return
	case f.RLS() && h.rlsSubs >= h.cfg.MaxRLSSubscriptions:
		refuse(liveproto.CodeLimit, "too many RLS-mode live queries on this database", h.cfg.MaxRLSSubscriptions, h.rlsSubs+1)
		return
	}
	h.nextID++
	engineID := h.prefix + strconv.FormatUint(sess.id, 10) + "." + strconv.FormatUint(h.nextID, 10)
	if h.join(engineID, sql, session, args) {
		// A held result, served without waking the database.
		s := h.newSub(sess, f, session, engineID, fingerprint, expires)
		if d, ok := h.engine.Current(engineID); ok {
			h.deliver(s, d)
		}
		return
	}
	if err := h.resume(ctx, "subscription"); err != nil {
		refuse(liveproto.CodeUnavailable, "the database is unavailable", 0, 0)
		return
	}
	h.lastActive = h.cfg.Now()
	if _, err := h.engine.RegisterAs(ctx, engineID, sql, session, args...); err != nil {
		code := liveproto.CodeInvalidQuery
		if errors.Is(err, livequery.ErrUnsupported) {
			code = liveproto.CodeUnsupported
		}
		refuse(code, err.Error(), 0, 0)
		return
	}
	h.cfg.Logger.Debug("live subscribe", "session", sess.id, "subscription", f.Subscription, "engine", engineID, "rls", session != nil)
	h.newSub(sess, f, session, engineID, fingerprint, expires)
	h.dirty = true
}

// newSub records a client subscription registered with the engine.
func (h *Hub) newSub(sess *Session, f liveproto.ClientFrame, session *livequery.Session, engineID, fingerprint string, expires time.Time) *sub {
	s := &sub{session: sess, client: f.Subscription, kid: seal.KeyID(f.Sealed), engineID: engineID, fingerprint: fingerprint, expires: expires}
	if f.RLS() {
		s.rls, s.rest = true, liveproto.ClientFrame{Database: f.Database, Schema: f.Schema, Rest: f.Rest}
		h.rlsSubs++
	}
	h.subs[engineID] = s
	sess.subs[f.Subscription] = s
	return s
}

func (h *Hub) renew(sess *Session, f liveproto.ClientFrame) {
	s := sess.subs[f.Subscription]
	refuse := func(code, msg string) {
		sess.send(&liveproto.Error{Type: liveproto.TypeError, Subscription: f.Subscription, Code: code, Message: msg})
	}
	if s == nil {
		refuse(liveproto.CodeProtocol, "no such subscription")
		return
	}
	if f.RLS() != s.rls {
		refuse(liveproto.CodeRenewMismatch, "a renewal keeps the subscription's mode")
		return
	}
	if s.rls {
		session, expires, no := h.session(sess, f)
		switch {
		case no != nil:
			refuse(no.code, no.msg)
			return
		case rlsFingerprint(liveproto.ClientFrame{Database: s.rest.Database, Schema: s.rest.Schema, Rest: s.rest.Rest, Role: f.Role}, session) != s.fingerprint:
			refuse(liveproto.CodeRenewMismatch, "the renewed token carries different claims; subscribe again")
			return
		}
		s.expires = expires
	} else {
		q, err := h.keys.Open(f.Sealed, h.cfg.Now())
		switch {
		case err != nil:
			refuse(seal.Code(err), err.Error())
			return
		case q.Fingerprint() != s.fingerprint:
			refuse(liveproto.CodeRenewMismatch, "the renewal seals a different query")
			return
		}
		s.expires, s.kid = q.ExpiresAt(), seal.KeyID(f.Sealed)
	}
	sess.send(&liveproto.Renewed{Type: liveproto.TypeRenewed, Subscription: s.client, Expires: s.expires.Unix()})
	if !s.stale {
		return
	}
	// Changes were withheld while stale: send the whole result again. A
	// result not built yet arrives whole at the next round anyway.
	s.stale = false
	if d, ok := h.engine.Current(s.engineID); ok {
		h.deliver(s, d)
	}
}

// subscribeRLS registers an RLS-mode read as its user's session.
func (h *Hub) subscribeRLS(ctx context.Context, sess *Session, f liveproto.ClientFrame, refuse func(code, msg string, limit, observed int)) {
	session, expires, no := h.session(sess, f)
	if no == nil && h.resume(ctx, "subscription") != nil {
		// A Data API read compiles against the catalog.
		no = &rlsRefusal{liveproto.CodeUnavailable, "the database is unavailable"}
	}
	if no == nil {
		var compiled *rest.Compiled
		if compiled, no = h.compileREST(ctx, f); no == nil {
			h.register(ctx, sess, f, compiled.SQL, compiled.Args, session, rlsFingerprint(f, session), expires, refuse)
			return
		}
	}
	refuse(no.code, no.msg, 0, 0)
}

// oversized reports a live.changes frame larger than a frame may be.
func oversized(frames []any) bool {
	if len(frames) != 1 {
		return false
	}
	c, ok := frames[0].(*liveproto.Changes)
	if !ok {
		return false
	}
	data, err := json.Marshal(c)
	return err != nil || len(data) > api.LiveQueryServerFrameMaxBytes
}

// Subscriptions reports how many subscriptions the engine holds, read on the
// hub's loop; -1 when the hub has stopped.
func (h *Hub) Subscriptions(ctx context.Context) int {
	n := make(chan int, 1)
	h.do(ctx, func(context.Context) { n <- h.engine.Subscriptions() })
	select {
	case v := <-n:
		return v
	case <-ctx.Done():
	case <-h.done:
	}
	return -1
}
