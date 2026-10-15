package pgproxy

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jackc/pgx/v5/pgproto3"

	"github.com/onebox-faas/faas/pkg/pgscram"
)

// Transaction pooling (ADR-1027). A pooled client connects to
// <database-id>-pooler.<suffix>. It authenticates with the proxy as on the
// direct endpoint, but never owns a PostgreSQL session: each of its
// transactions borrows a server connection from its role's pool and returns
// it when PostgreSQL reports the session idle again. Many clients then
// share a few of the database's connections (native databases allow 25),
// as with Neon's pooled endpoint. Session state (SET, LISTEN, advisory
// locks, SQL PREPARE) does not survive between transactions; named
// protocol-level prepared statements do, prepared again on whichever server
// runs them.

// pooledSuffix marks a pooled endpoint's database label.
const pooledSuffix = "-pooler"

// poolKey names a pool: one role's logins to one database with one
// verifier, so a rotated password starts a new pool.
type poolKey struct {
	databaseID, role, dbname, verifier string
}

// pool keeps a role's server connections to a database and the key a
// client proved, with which it logs further connections in.
type pool struct {
	set                  *poolSet
	key                  poolKey
	clientKey, serverKey []byte
	params               map[string]string

	mu      sync.Mutex
	idle    []*serverConn
	clients int
	// status is what a server reported at login, replayed to every client.
	status [][]byte
}

// serverConn is one logged-in PostgreSQL session.
type serverConn struct {
	rw       io.ReadWriteCloser
	fe       *pgproto3.Frontend
	prepared map[string]bool
	idleAt   time.Time
	// key reaches the server's backend for cancel requests.
	key backendKey
}

func (s *serverConn) close() { _ = s.rw.Close() }

// maxServerStatements bounds the prepared statements one server keeps; a
// server past it is closed when released.
const maxServerStatements = 1000

// poolSet holds the proxy's pools and enforces each database's server cap.
type poolSet struct {
	p     *Proxy
	mu    sync.Mutex
	pools map[poolKey]*pool
	// servers counts each database's open server connections, opening ones
	// included; freed wakes waiters for a database.
	servers map[string]int
	freed   map[string]chan struct{}
	// egress is each database's pooled bytes sent to clients since the
	// last touch.
	egress map[string]*atomic.Int64
}

func newPoolSet(p *Proxy) *poolSet {
	return &poolSet{p: p, pools: map[poolKey]*pool{}, servers: map[string]int{}, freed: map[string]chan struct{}{}, egress: map[string]*atomic.Int64{}}
}

// join returns the pool for key, made with the client's proven key if it
// is new, and counts the client in.
func (s *poolSet) join(key poolKey, clientKey, serverKey []byte, params map[string]string) *pool {
	s.mu.Lock()
	defer s.mu.Unlock()
	pl, ok := s.pools[key]
	if !ok {
		pl = &pool{set: s, key: key, clientKey: clientKey, serverKey: serverKey, params: params}
		s.pools[key] = pl
	}
	if _, ok := s.egress[key.databaseID]; !ok {
		s.egress[key.databaseID] = &atomic.Int64{}
	}
	pl.mu.Lock()
	pl.clients++
	pl.mu.Unlock()
	return pl
}

func (pl *pool) leave() {
	pl.mu.Lock()
	pl.clients--
	pl.mu.Unlock()
}

// errPoolFull: the database's server connections stayed busy for the
// whole wait.
var errPoolFull = errors.New("pgproxy: every pooled connection to the database is busy")

// acquire returns an idle server of the pool or opens one, waiting up to
// PoolWait while the database is at its cap. It takes an idle server of
// another of the database's pools first, so one role cannot starve
// another.
func (pl *pool) acquire(ctx context.Context) (*serverConn, error) {
	s := pl.set
	ctx, cancel := context.WithTimeout(ctx, s.p.cfg.PoolWait)
	defer cancel()
	for {
		if srv := pl.popIdle(); srv != nil {
			return srv, nil
		}
		s.mu.Lock()
		wait := s.freedLocked(pl.key.databaseID)
		open := s.servers[pl.key.databaseID] < s.p.cfg.PoolMaxServers
		if !open {
			open = s.reclaimLocked(pl)
		}
		if open {
			s.servers[pl.key.databaseID]++
		}
		s.mu.Unlock()
		if open {
			srv, err := pl.open(ctx)
			if err != nil {
				s.release(pl.key.databaseID)
				return nil, err
			}
			return srv, nil
		}
		select {
		case <-ctx.Done():
			return nil, errPoolFull
		case <-wait:
		}
	}
}

func (pl *pool) popIdle() *serverConn {
	pl.mu.Lock()
	defer pl.mu.Unlock()
	if n := len(pl.idle); n > 0 {
		srv := pl.idle[n-1]
		pl.idle = pl.idle[:n-1]
		return srv
	}
	return nil
}

// reclaimLocked closes an idle server another pool of the database holds,
// making room under the cap. s.mu is held.
func (s *poolSet) reclaimLocked(except *pool) bool {
	for key, other := range s.pools {
		if other == except || key.databaseID != except.key.databaseID {
			continue
		}
		if srv := other.popIdle(); srv != nil {
			srv.close()
			s.servers[key.databaseID]--
			return true
		}
	}
	return false
}

func (s *poolSet) freedLocked(databaseID string) <-chan struct{} {
	ch, ok := s.freed[databaseID]
	if !ok {
		ch = make(chan struct{})
		s.freed[databaseID] = ch
	}
	return ch
}

// release frees one of a database's server slots and wakes its waiters.
func (s *poolSet) release(databaseID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.servers[databaseID]--
	s.signalLocked(databaseID)
}

func (s *poolSet) signalLocked(databaseID string) {
	if ch, ok := s.freed[databaseID]; ok {
		close(ch)
		delete(s.freed, databaseID)
	}
}

// open wakes the database and logs a new server in with the pool's key.
func (pl *pool) open(ctx context.Context) (*serverConn, error) {
	p := pl.set.p
	wakeCtx, cancel := context.WithTimeout(ctx, p.cfg.WakeTimeout)
	defer cancel()
	started := time.Now()
	target, err := p.waker.Wake(wakeCtx, pl.key.databaseID)
	p.cfg.Metrics.woke(time.Since(started))
	if err != nil {
		return nil, err
	}
	rw, err := p.dialer.Dial(wakeCtx, target, pl.key.databaseID)
	if err != nil {
		return nil, err
	}
	srv := &serverConn{rw: rw, fe: pgproto3.NewFrontend(rw, rw), prepared: map[string]bool{}, key: backendKey{target: target, databaseID: pl.key.databaseID}}
	if err := loginBackend(srv.fe, pl.params, pl.clientKey, pl.serverKey); err != nil {
		srv.close()
		return nil, err
	}
	var status [][]byte
	for {
		msg, err := srv.fe.Receive()
		if err != nil {
			srv.close()
			return nil, err
		}
		switch m := msg.(type) {
		case *pgproto3.ParameterStatus:
			buf, err := m.Encode(nil)
			if err != nil {
				srv.close()
				return nil, err
			}
			status = append(status, buf)
		case *pgproto3.BackendKeyData:
			srv.key.pid, srv.key.secret = m.ProcessID, append([]byte(nil), m.SecretKey...)
		case *pgproto3.ErrorResponse:
			srv.close()
			return nil, &fatal{m.Code, m.Message}
		case *pgproto3.ReadyForQuery:
			pl.mu.Lock()
			if pl.status == nil {
				pl.status = status
			}
			pl.mu.Unlock()
			return srv, nil
		}
	}
}

// put returns a server after a transaction. A server in an unknown state,
// or holding too many statements, is closed instead.
func (pl *pool) put(srv *serverConn, clean bool) {
	if !clean || len(srv.prepared) > maxServerStatements {
		srv.close()
		pl.set.release(pl.key.databaseID)
		return
	}
	srv.idleAt = time.Now()
	pl.mu.Lock()
	pl.idle = append(pl.idle, srv)
	pl.mu.Unlock()
	pl.set.mu.Lock()
	pl.set.signalLocked(pl.key.databaseID)
	pl.set.mu.Unlock()
}

// maintain closes servers idle past PoolServerIdle, forgets pools nobody
// uses (and with them the proven key), and touches every database with
// open servers or pooled egress, until ctx ends.
func (s *poolSet) maintain(ctx context.Context) {
	ticker := time.NewTicker(s.p.cfg.TouchInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			s.closeAll()
			return
		case <-ticker.C:
		}
		for databaseID, egress := range s.sweep() {
			if err := s.p.waker.Touch(ctx, databaseID, egress); err != nil {
				s.p.log.Warn("pgproxy: pooled touch failed", "database_id", databaseID, "err", err)
			}
		}
	}
}

// sweep closes idle servers past their time and returns the databases to
// touch with their egress.
func (s *poolSet) sweep() map[string]int64 {
	cutoff := time.Now().Add(-s.p.cfg.PoolServerIdle)
	s.mu.Lock()
	defer s.mu.Unlock()
	for key, pl := range s.pools {
		pl.mu.Lock()
		kept := pl.idle[:0]
		for _, srv := range pl.idle {
			if srv.idleAt.Before(cutoff) {
				srv.close()
				s.servers[key.databaseID]--
				s.signalLocked(key.databaseID)
				continue
			}
			kept = append(kept, srv)
		}
		pl.idle = kept
		unused := pl.clients == 0 && len(pl.idle) == 0
		pl.mu.Unlock()
		if unused && s.servers[key.databaseID] == 0 {
			delete(s.pools, key)
		}
	}
	touch := map[string]int64{}
	for databaseID, egress := range s.egress {
		if bytes := egress.Swap(0); s.servers[databaseID] > 0 || bytes > 0 {
			touch[databaseID] = bytes
		}
	}
	for databaseID, n := range s.servers {
		if n == 0 {
			delete(s.servers, databaseID)
		}
	}
	return touch
}

func (s *poolSet) closeAll() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, pl := range s.pools {
		pl.mu.Lock()
		for _, srv := range pl.idle {
			srv.close()
		}
		pl.idle = nil
		pl.mu.Unlock()
	}
}

// egressOf is the counter of a database's pooled egress.
func (s *poolSet) egressOf(databaseID string) *atomic.Int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.egress[databaseID]
	if !ok {
		e = &atomic.Int64{}
		s.egress[databaseID] = e
	}
	return e
}

// verifierKey names a verifier without exposing it.
func verifierKey(v pgscram.Verifier) string {
	sum := sha256.Sum256(append(append([]byte{}, v.StoredKey...), v.ServerKey...))
	return hex.EncodeToString(sum[:16])
}
