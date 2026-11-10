// adr: 570
package state

import (
	"context"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/onebox-faas/faas/pkg/api"
)

// A policy snapshot's callers bound it tightly (api.TrafficPublicHostReadTimeout,
// 250 ms). pgx answers an expired context by closing the connection, so in a
// control-plane slowdown every expired read cost gatewayd-internal a
// connection, and each replacement paid TLS, SCRAM, a new backend and its
// warm-up on the database that was already behind: on 2026-11-10 at 12:01 and
// 12:17 both gateways' pools fell from 7 to 4 connections while about 205
// acquires a minute were abandoned.
//
// policySnapshotCancel runs the snapshot's statements on a context detached
// from the caller's deadline. When the caller's context ends it sends
// PostgreSQL a cancel request, which ends the running statement and keeps the
// session; only when PostgreSQL has not answered within
// api.TrafficPolicySnapshotCancelGrace does the detached context end too and
// pgx close the connection, as before. The caller still gets its own context
// error, so its classification is unchanged.
type policySnapshotCancel struct {
	caller context.Context
	ctx    context.Context
	cancel context.CancelFunc
	stop   func() bool

	mu    sync.Mutex
	conn  *pgconn.PgConn
	fired bool
	// sent is closed once the watcher has finished: its cancel request
	// returned, or it had no connection to cancel.
	sent chan struct{}
}

// policySnapshotCancelSettle keeps a cancelled connection out of the pool a
// little longer. PostgreSQL acknowledges a cancel request before it signals
// the backend, so a connection reused at once can have its next statement
// cancelled (pgx's CancelRequestContextWatcherHandler waits 100 ms likewise).
var policySnapshotCancelSettle = 100 * time.Millisecond

// policySnapshotCancelRequestTimeout bounds the cancel request's own dial.
const policySnapshotCancelRequestTimeout = 2 * time.Second

func startPolicySnapshotCancel(caller context.Context) *policySnapshotCancel {
	ctx, cancel := context.WithCancel(context.WithoutCancel(caller))
	c := &policySnapshotCancel{caller: caller, ctx: ctx, cancel: cancel, sent: make(chan struct{})}
	c.stop = context.AfterFunc(caller, c.callerDone)
	return c
}

// callerDone runs once the caller's context ends while the snapshot runs.
func (c *policySnapshotCancel) callerDone() {
	defer close(c.sent)
	c.mu.Lock()
	c.fired = true
	conn := c.conn
	c.mu.Unlock()
	if conn == nil {
		// Still acquiring or beginning: nothing runs that a cancel request
		// could end, and the caller must not wait on the pool past its
		// deadline.
		c.cancel()
		return
	}
	time.AfterFunc(api.TrafficPolicySnapshotCancelGrace, c.cancel)
	requestCtx, cancel := context.WithTimeout(context.Background(), policySnapshotCancelRequestTimeout)
	defer cancel()
	_ = conn.CancelRequest(requestCtx)
}

// attach names the connection the snapshot's statements run on.
func (c *policySnapshotCancel) attach(tx pgx.Tx) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.fired {
		// The caller gave up while the transaction began; end it without
		// waiting for a statement to cancel.
		c.cancel()
		return
	}
	c.conn = tx.Conn().PgConn()
}

// finish ends tx (nil when it never began) and returns the snapshot's result.
// It commits only while the caller still waits; once the watcher has fired, a
// cancel request may be on its way to this session, so the transaction rolls
// back after that request has settled, off the caller's path, and only then
// does the connection return to the pool. Detaching the connection under the
// same lock the watcher fires under means a cancel request never reaches a
// connection the pool has handed to someone else.
func (c *policySnapshotCancel) finish(tx pgx.Tx, err error) error {
	c.mu.Lock()
	fired := c.fired
	c.conn = nil
	c.mu.Unlock()
	if !fired {
		if tx != nil {
			if err == nil {
				err = tx.Commit(c.ctx)
			}
			rollbackOrgAccess(c.ctx, tx)
		}
		c.stop()
		c.cancel()
		return c.callerErr(err)
	}
	go func() {
		<-c.sent
		time.Sleep(policySnapshotCancelSettle)
		if tx != nil {
			rollbackOrgAccess(c.ctx, tx)
		}
		c.cancel()
	}()
	return c.callerErr(err)
}

// callerErr reports a failure the caller's own deadline caused as that
// context's error, which is what pgx returned when it closed the connection.
func (c *policySnapshotCancel) callerErr(err error) error {
	if err != nil {
		if callerErr := c.caller.Err(); callerErr != nil {
			return callerErr
		}
	}
	return err
}

// policySnapshotTx runs every statement on the snapshot's detached context;
// the context a reader passes is the caller's, whose deadline would close the
// connection.
type policySnapshotTx struct {
	pgx.Tx
	ctx context.Context
}

func (t policySnapshotTx) Exec(_ context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	return t.Tx.Exec(t.ctx, sql, args...)
}

func (t policySnapshotTx) Query(_ context.Context, sql string, args ...any) (pgx.Rows, error) {
	return t.Tx.Query(t.ctx, sql, args...)
}

func (t policySnapshotTx) QueryRow(_ context.Context, sql string, args ...any) pgx.Row {
	return t.Tx.QueryRow(t.ctx, sql, args...)
}

func (t policySnapshotTx) SendBatch(_ context.Context, b *pgx.Batch) pgx.BatchResults {
	return t.Tx.SendBatch(t.ctx, b)
}

func (t policySnapshotTx) Prepare(_ context.Context, name, sql string) (*pgconn.StatementDescription, error) {
	return t.Tx.Prepare(t.ctx, name, sql)
}

func (t policySnapshotTx) CopyFrom(_ context.Context, table pgx.Identifier, columns []string, src pgx.CopyFromSource) (int64, error) {
	return t.Tx.CopyFrom(t.ctx, table, columns, src)
}
