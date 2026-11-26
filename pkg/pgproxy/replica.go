package pgproxy

import (
	"context"
	"fmt"
	"io"
	"maps"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgproto3"

	"github.com/onebox-faas/faas/pkg/dbguest"
)

// Read-only endpoints (ADR-1027 amendment 2026-11-11). <id>-ro names a
// database's direct read-only endpoint and <id>-ro-pooler its pooled one.
// Their sessions go to the database's read replica, its production
// standby, while schedd reports the standby serving reads; otherwise, or
// when the replica cannot take the session, to the primary. Either way the
// session starts with default_transaction_read_only, so a write fails as
// it would on a standby. That is a guard against mistakes, not a privilege
// boundary: a client may turn it off, and on the primary its writes then
// land. Read-only credentials are the privilege boundary.

// readOnlySuffix marks a read-only endpoint's database label, before any
// -pooler.
const readOnlySuffix = "-ro"

// endpoint is what a client's label asked for.
type endpoint struct {
	databaseID  string
	pooled      bool
	readOnly    bool
	replicaOnly bool
}

// parseEndpoint reads <id>[-ro|-replica][-pooler].
func parseEndpoint(label string) (endpoint, bool) {
	label = strings.ToLower(label)
	id, pooled := strings.CutSuffix(label, pooledSuffix)
	id, replicaOnly := strings.CutSuffix(id, dbguest.ReplicaOnlySuffix)
	id, readOnly := strings.CutSuffix(id, readOnlySuffix)
	if replicaOnly && readOnly {
		return endpoint{}, false
	}
	if _, err := uuid.Parse(id); err != nil || len(id) != 36 {
		return endpoint{}, false
	}
	return endpoint{databaseID: id, pooled: pooled, readOnly: readOnly || replicaOnly, replicaOnly: replicaOnly}, true
}

// readOnlyParams are a read-only endpoint's startup parameters.
func readOnlyParams(params map[string]string) map[string]string {
	out := maps.Clone(params)
	out["default_transaction_read_only"] = "on"
	return out
}

// primary is the backend without its replica.
func (b Backend) primary() Backend {
	return Backend{Target: b.Target, Node: b.Node, PrimaryEpochMicros: b.PrimaryEpochMicros}
}

// replica is where the database's read sessions go, if schedd named one.
func (b Backend) replica() (Backend, bool) {
	if b.ReplicaTarget == "" || b.ReplicaNode == "" {
		return Backend{}, false
	}
	return Backend{Target: b.ReplicaTarget, Node: b.ReplicaNode}, true
}

// backendLogin is a session to a database logged in with a client's key.
// finish ends the setup deadline; the caller reads on to ReadyForQuery.
type backendLogin struct {
	backend Backend
	rw      io.ReadWriteCloser
	fe      *pgproto3.Frontend
	finish  func() error
}

// login dials b and logs in with the client's proven key. A failure closes
// the session.
func (p *Proxy) login(ctx context.Context, b Backend, databaseID string, params map[string]string, clientKey, serverKey []byte) (backendLogin, error) {
	rw, err := p.dialer.Dial(ctx, b, databaseID)
	if err != nil {
		return backendLogin{}, err
	}
	fe := pgproto3.NewFrontend(rw, rw)
	finish := interruptSetup(ctx, rw)
	if err := loginBackend(fe, params, clientKey, serverKey); err != nil {
		_ = finish()
		_ = rw.Close()
		return backendLogin{}, err
	}
	return backendLogin{backend: b, rw: rw, fe: fe, finish: finish}, nil
}

// loginEndpoint logs a session in where its endpoint sends it: the primary,
// or for a read-only endpoint the replica first.
func (p *Proxy) loginEndpoint(ctx context.Context, woken Backend, readOnly bool, databaseID string, params map[string]string, clientKey, serverKey []byte) (backendLogin, error) {
	if !readOnly {
		return p.login(ctx, woken.primary(), databaseID, params, clientKey, serverKey)
	}
	params = readOnlyParams(params)
	if replica, ok := woken.replica(); ok {
		l, err := p.loginReplica(ctx, replica, databaseID, params, clientKey, serverKey)
		if err == nil {
			p.cfg.Metrics.readSession("replica")
			return l, nil
		}
		p.log.Debug("pgproxy: replica refused a read session; using the primary", "database_id", databaseID, "err", err)
	}
	l, err := p.login(ctx, woken.primary(), databaseID, params, clientKey, serverKey)
	if err == nil {
		p.cfg.Metrics.readSession("primary")
	}
	return l, err
}

// A missing or stale sample cannot satisfy strict routing. The scheduler
// refreshes its control-plane observation on demand and shares it briefly.
const replicaMaxSampleAge = 5 * time.Second

var errReplicaUnavailable = &fatal{"57P03", "the read replica is unavailable or behind; retry shortly"}

type replicaRouteReader interface {
	ReplicaRoute(context.Context, string) (Backend, *int64, time.Time, error)
}

func (p *Proxy) strictReplica(ctx context.Context, id string) (Backend, error) {
	reader, ok := p.waker.(replicaRouteReader)
	if !ok {
		return Backend{}, errReplicaUnavailable
	}
	probe, cancel := context.WithTimeout(ctx, p.cfg.ReplicaLoginTimeout)
	defer cancel()
	replica, lag, observed, err := reader.ReplicaRoute(probe, id)
	age := time.Since(observed)
	if err != nil || replica.Target == "" || replica.Node == "" || lag == nil || *lag < 0 ||
		*lag > *p.cfg.ReplicaMaxReplayLagBytes || age < -time.Second || age > replicaMaxSampleAge {
		return Backend{}, errReplicaUnavailable
	}
	replica.replicaOnly = true
	return replica, nil
}

// loginRoute is shared by direct sessions and pooled server connections.
func (p *Proxy) loginRoute(ctx context.Context, databaseID string, readOnly bool, strict Backend, params map[string]string, clientKey, serverKey []byte) (backendLogin, error) {
	if strict.replicaOnly {
		login, err := p.loginReplica(ctx, strict, databaseID, readOnlyParams(params), clientKey, serverKey)
		if err != nil {
			p.log.Debug("pgproxy: standby-only login refused", "database_id", databaseID, "err", err)
			return backendLogin{}, errReplicaUnavailable
		}
		p.cfg.Metrics.readSession("replica")
		return login, nil
	}

	started := time.Now()
	woken, err := p.waker.Wake(ctx, databaseID)
	p.cfg.Metrics.woke(time.Since(started))
	if err != nil {
		if f := wakeRefused(err); f != nil {
			return backendLogin{}, f
		}
		return backendLogin{}, err
	}
	return p.loginEndpoint(ctx, woken, readOnly, databaseID, params, clientKey, serverKey)
}

// loginReplica tries the replica within ReplicaLoginTimeout, so a replica
// that does not answer leaves the wake time to the primary. Any failure,
// a wrong password included (a just-rotated one may not have replayed
// there yet), sends the session to the primary.
func (p *Proxy) loginReplica(ctx context.Context, replica Backend, databaseID string, params map[string]string, clientKey, serverKey []byte) (backendLogin, error) {
	tryCtx, cancel := context.WithTimeout(ctx, p.cfg.ReplicaLoginTimeout)
	defer cancel()
	l, err := p.login(tryCtx, replica, databaseID, params, clientKey, serverKey)
	if err != nil {
		return backendLogin{}, err
	}
	// The attempt's deadline ends here; the rest of the setup keeps ctx's.
	if err := l.finish(); err != nil {
		_ = l.rw.Close()
		return backendLogin{}, fmt.Errorf("pgproxy: replica login: %w", err)
	}
	l.finish = interruptSetup(ctx, l.rw)
	return l, nil
}
