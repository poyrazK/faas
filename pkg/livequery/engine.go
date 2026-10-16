package livequery

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pglogrepl"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// pinnedSettings fixes the session settings that output functions and STABLE
// built-ins depend on, so maintenance and decoding render values identically.
const pinnedSettings = `SET LOCAL TimeZone = 'UTC'; SET LOCAL DateStyle = 'ISO, MDY'; ` +
	`SET LOCAL IntervalStyle = 'postgres'; SET LOCAL extra_float_digits = 3`

// CaptureRuntimeParams are the same settings for the replication connection.
var CaptureRuntimeParams = map[string]string{
	"TimeZone": "UTC", "DateStyle": "ISO, MDY", "IntervalStyle": "postgres", "extra_float_digits": "3",
}

// Row is one result row as a subscriber sees it.
type Row struct {
	ID     string
	Values []*string
}

// Delta is one round's change to one subscription.
type Delta struct {
	Query    string
	Snapshot string
	Xids     []uint64 // transactions this round applied, as xid8
	Reset    bool     // Rows replaces the whole result
	Rows     []Row
	Upserts  []Row
	Removes  []string
}

// Empty reports whether the delta changes nothing.
func (d Delta) Empty() bool { return !d.Reset && len(d.Upserts) == 0 && len(d.Removes) == 0 }

// Stats counts what the engine did, so tests can show which paths ran.
type Stats struct {
	Rounds       int
	Applied      int // transactions applied
	Held         int // transactions decoded before a marker but not yet visible
	Keyed        int // keyed maintenance passes (lineage or group), per plan group
	Incremental  int // of those, incremental aggregate passes
	Rebuilds     int // full re-evaluations (recompute mode, truncation), per plan group
	Verified     int // oracle comparisons
	ExpandedKeys int // preserved-side keys added for outer joins
	Statements   int // maintenance statements executed (excluding the oracle)
	// StatementTime is the time spent in maintenance statements, including
	// reading their rows.
	StatementTime time.Duration
}

// Engine maintains live queries from a capture stream. Subscriptions with the
// same SQL form one plan group, compiled once and maintained by batched
// statements; subscriptions with the same SQL and arguments share a member.
type Engine struct {
	pool    *pgxpool.Pool
	capture *Capture

	groups  []*planGroup
	bySQL   map[string]*planGroup
	byID    map[string]*member
	pending []*Txn
	round   uint64

	// VerifyEachRound re-runs every subscription at the round snapshot and
	// fails the round if the maintained result differs: the differential
	// oracle.
	VerifyEachRound bool
	// Trace, when set, receives a line per applied or held transaction.
	Trace func(string)
	// LastApplied holds the transactions the last round applied.
	LastApplied []*Txn
	// StatementHook, when set, receives each maintenance statement and its
	// duration (benchmarks use it to find the expensive ones).
	StatementHook func(sql string, members int, d time.Duration)

	Stats Stats
}

// NewEngine creates an engine over a pool and an open capture.
func NewEngine(pool *pgxpool.Pool, capture *Capture) *Engine {
	return &Engine{pool: pool, capture: capture, bySQL: map[string]*planGroup{}, byID: map[string]*member{}}
}

// Register subscribes id to a query. Its initial result arrives in the next
// round.
func (e *Engine) Register(ctx context.Context, id, sql string, args ...any) (*Plan, error) {
	if _, dup := e.byID[id]; dup {
		return nil, fmt.Errorf("livequery: subscription %s already exists", id)
	}
	conn, err := e.pool.Acquire(ctx)
	if err != nil {
		return nil, fmt.Errorf("livequery: acquire for compile: %w", err)
	}
	defer conn.Release()
	g := e.bySQL[sql]
	if g == nil {
		plan, err := Compile(ctx, conn.Conn(), sql)
		if err != nil {
			return nil, err
		}
		g = &planGroup{plan: plan, byArgs: map[string]*member{}, stats: &e.Stats, engine: e}
		e.bySQL[sql] = g
		e.groups = append(e.groups, g)
	}
	if g.plan.NParams != len(args) {
		return nil, fmt.Errorf("livequery: query takes %d parameters, got %d", g.plan.NParams, len(args))
	}
	text, err := argText(ctx, conn.Conn(), g.plan.ParamTypes, args)
	if err != nil {
		return nil, err
	}
	key := encodeValues(text)
	m := g.byArgs[key]
	if m == nil {
		m = &member{group: g, idx: int32(len(g.members)), args: args, argText: text}
		g.members = append(g.members, m)
		g.byArgs[key] = m
	}
	m.ids = append(m.ids, id)
	e.byID[id] = m
	return g.plan, nil
}

// argText renders arguments as PostgreSQL text, the form batched statements
// pass them in, so identical arguments are recognised however the caller
// typed them.
func argText(ctx context.Context, conn *pgx.Conn, types []string, args []any) ([]*string, error) {
	if len(args) == 0 {
		return nil, nil
	}
	cols := make([]string, len(types))
	for k, typ := range types {
		cols[k] = fmt.Sprintf("$%d::%s::text", k+1, typ)
	}
	rows, err := conn.Query(ctx, "SELECT "+strings.Join(cols, ", "), args...)
	if err != nil {
		return nil, fmt.Errorf("livequery: render arguments: %w", err)
	}
	defer rows.Close()
	out := make([]*string, len(args))
	if rows.Next() {
		for k, raw := range rows.RawValues() {
			if raw != nil {
				v := string(raw)
				out[k] = &v
			}
		}
	}
	return out, rows.Err()
}

// Result returns the engine's current rows for a subscription.
func (e *Engine) Result(id string) []Row {
	if m := e.byID[id]; m != nil {
		return m.rows()
	}
	return nil
}

// changeSet is what a round's transactions changed, by table.
type changeSet struct {
	keys      map[uint32]map[string]struct{}
	truncated map[uint32]bool
	missing   map[uint32]bool // changes whose key could not be read
}

// Round runs one snapshot-aligned maintenance round and returns the deltas.
func (e *Engine) Round(ctx context.Context) ([]Delta, error) {
	e.round++
	marker := strconv.FormatUint(e.round, 10)
	tx, snapText, err := e.beginRound(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	snap, err := ParseSnapshot(snapText)
	if err != nil {
		return nil, err
	}
	// The marker follows the snapshot, so every transaction visible in it is
	// decoded before the marker arrives. The xmax read with it bounds every
	// xid decoded this round.
	var refText string
	if err := e.pool.QueryRow(ctx,
		"SELECT pg_snapshot_xmax(pg_current_snapshot())::text FROM pg_logical_emit_message(false, $1, $2)",
		MarkerPrefix, marker).Scan(&refText); err != nil {
		return nil, fmt.Errorf("livequery: emit round marker: %w", err)
	}
	ref, err := strconv.ParseUint(refText, 10, 64)
	if err != nil {
		return nil, fmt.Errorf("livequery: parse reference xid %q: %w", refText, err)
	}
	markerLSN, err := e.drain(ctx, marker)
	if err != nil {
		return nil, err
	}

	var visible, held []*Txn
	var xids []uint64
	for _, txn := range e.pending {
		full := FullXid(txn.Xid, ref)
		if e.VerifyEachRound {
			if err := checkVisibility(ctx, tx, snap, full); err != nil {
				return nil, fmt.Errorf("livequery: round %d (ref %d): %w", e.round, ref, err)
			}
		}
		if snap.Visible(full) {
			visible = append(visible, txn)
			xids = append(xids, full)
		} else {
			held = append(held, txn)
		}
	}
	slices.Sort(xids)
	changes := e.collect(visible)
	e.Stats.Rounds++
	e.Stats.Applied += len(visible)
	e.Stats.Held += len(held)

	var deltas []Delta
	for _, g := range e.groups {
		perMember, err := g.step(ctx, tx, changes)
		if err != nil {
			return nil, fmt.Errorf("livequery: query %q: %w", g.plan.SQL, err)
		}
		for _, m := range g.members {
			d, ok := perMember[m]
			if !ok {
				d = Delta{}
			}
			for _, id := range m.ids {
				d.Query, d.Snapshot, d.Xids = id, snapText, xids
				deltas = append(deltas, d)
			}
		}
	}
	e.trace(snapText, ref, markerLSN, visible, held)
	if e.VerifyEachRound {
		for _, g := range e.groups {
			for _, m := range g.members {
				if err := m.verify(ctx, tx); err != nil {
					return nil, fmt.Errorf("livequery: round %d snapshot %s: subscription %v: %w", e.round, snapText, m.ids, err)
				}
				e.Stats.Verified++
			}
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("livequery: commit round: %w", err)
	}
	e.pending = held
	e.LastApplied = visible
	e.confirm(visible, held, markerLSN)
	return deltas, nil
}

func (e *Engine) trace(snapText string, ref uint64, marker pglogrepl.LSN, visible, held []*Txn) {
	if e.Trace == nil {
		return
	}
	e.Trace(fmt.Sprintf("round %d snapshot %s ref %d marker %s", e.round, snapText, ref, marker))
	for _, txn := range visible {
		e.Trace(fmt.Sprintf("round %d applies xid %d end %s: %s", e.round, FullXid(txn.Xid, ref), txn.EndLSN, describeTxn(txn)))
	}
	for _, txn := range held {
		e.Trace(fmt.Sprintf("round %d holds xid %d end %s", e.round, FullXid(txn.Xid, ref), txn.EndLSN))
	}
}

func describeTxn(txn *Txn) string {
	var parts []string
	for _, ch := range txn.Changes {
		key := func(t Tuple) string {
			if t == nil {
				return "-"
			}
			var kv []string
			for k, v := range t {
				if v != nil && (k == "id" || k == "k1" || k == "k2") {
					kv = append(kv, k+"="+*v)
				}
			}
			sort.Strings(kv)
			return strings.Join(kv, ",")
		}
		parts = append(parts, fmt.Sprintf("%c rel=%d old[%s] new[%s]", ch.Op, ch.Relation, key(ch.Old), key(ch.New)))
	}
	if len(txn.Truncated) > 0 {
		parts = append(parts, fmt.Sprintf("truncate %v", txn.Truncated))
	}
	return strings.Join(parts, "; ")
}

// roundLockAttempts bounds retries when a round's table locks time out or
// lose a deadlock to a concurrent TRUNCATE or table rewrite.
const roundLockAttempts = 10

// beginRound opens the round transaction and takes its snapshot. TRUNCATE and
// table rewrites are not MVCC-safe: one that commits while the round reads
// would make a table look empty to a snapshot that predates it. Holding
// ACCESS SHARE on every live table from before the snapshot until the round
// commits keeps them out; ordinary writes take weaker locks and proceed.
func (e *Engine) beginRound(ctx context.Context) (pgx.Tx, string, error) {
	tables := e.lockList()
	for attempt := 1; ; attempt++ {
		tx, snap, err := e.tryBeginRound(ctx, tables)
		if err == nil {
			return tx, snap, nil
		}
		var pgErr *pgconn.PgError
		retryable := errors.As(err, &pgErr) && (pgErr.Code == "40P01" || pgErr.Code == "55P03")
		if !retryable || attempt == roundLockAttempts {
			return nil, "", err
		}
	}
}

func (e *Engine) tryBeginRound(ctx context.Context, tables string) (pgx.Tx, string, error) {
	tx, err := e.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return nil, "", fmt.Errorf("livequery: begin round: %w", err)
	}
	fail := func(op string, err error) (pgx.Tx, string, error) {
		_ = tx.Rollback(ctx)
		return nil, "", fmt.Errorf("livequery: %s: %w", op, err)
	}
	if _, err := tx.Exec(ctx, pinnedSettings+"; SET LOCAL lock_timeout = '2s'"); err != nil {
		return fail("pin settings", err)
	}
	// LOCK does not take the REPEATABLE READ snapshot; the next SELECT does.
	if tables != "" {
		if _, err := tx.Exec(ctx, "LOCK TABLE "+tables+" IN ACCESS SHARE MODE"); err != nil {
			return fail("lock live tables", err)
		}
	}
	var snap string
	if err := tx.QueryRow(ctx, "SELECT pg_current_snapshot()::text").Scan(&snap); err != nil {
		return fail("take snapshot", err)
	}
	return tx, snap, nil
}

// lockList names every table any live query reads, in OID order.
func (e *Engine) lockList() string {
	names := map[uint32]string{}
	for _, g := range e.groups {
		for oid, name := range g.plan.TableNames {
			names[oid] = name
		}
	}
	oids := make([]uint32, 0, len(names))
	for oid := range names {
		oids = append(oids, oid)
	}
	slices.Sort(oids)
	parts := make([]string, len(oids))
	for i, oid := range oids {
		parts[i] = names[oid]
	}
	return strings.Join(parts, ", ")
}

// drain consumes capture events up to this round's marker.
func (e *Engine) drain(ctx context.Context, marker string) (pglogrepl.LSN, error) {
	for {
		select {
		case ev, ok := <-e.capture.Events():
			if !ok {
				return 0, fmt.Errorf("livequery: capture stopped: %w", e.capture.Err())
			}
			if ev.Txn != nil {
				e.pending = append(e.pending, ev.Txn)
			} else if ev.Marker == marker {
				return ev.LSN, nil
			}
		case <-ctx.Done():
			return 0, ctx.Err()
		}
	}
}

// DebugDrain reads whatever the capture delivers for the duration, for
// divergence reports.
func (e *Engine) DebugDrain(d time.Duration) []string {
	var out []string
	timeout := time.After(d)
	for {
		select {
		case ev, ok := <-e.capture.Events():
			if !ok {
				return out
			}
			if ev.Txn != nil {
				out = append(out, fmt.Sprintf("late xid %d end %s: %s", ev.Txn.Xid, ev.Txn.EndLSN, describeTxn(ev.Txn)))
			} else {
				out = append(out, fmt.Sprintf("late marker %q at %s", ev.Marker, ev.LSN))
			}
		case <-timeout:
			return out
		}
	}
}

// confirm lets the slot release WAL that every applied transaction covers,
// stopping before the first transaction still held for a later round.
func (e *Engine) confirm(visible, held []*Txn, marker pglogrepl.LSN) {
	if len(held) == 0 {
		e.capture.Confirm(marker)
		return
	}
	limit := held[0].EndLSN
	for _, txn := range held {
		limit = min(limit, txn.EndLSN)
	}
	var best pglogrepl.LSN
	for _, txn := range visible {
		if txn.EndLSN < limit {
			best = max(best, txn.EndLSN)
		}
	}
	if best > 0 {
		e.capture.Confirm(best)
	}
}

// collect gathers changed primary keys per table from visible transactions.
func (e *Engine) collect(txns []*Txn) changeSet {
	cs := changeSet{keys: map[uint32]map[string]struct{}{}, truncated: map[uint32]bool{}, missing: map[uint32]bool{}}
	keyCols := map[uint32][]string{}
	for _, g := range e.groups {
		for _, rel := range g.plan.Relations {
			keyCols[rel.OID] = rel.Keys
		}
	}
	add := func(table uint32, t Tuple) {
		cols, ok := keyCols[table]
		if !ok || t == nil {
			return
		}
		parts := make([]string, len(cols))
		for k, col := range cols {
			v, present := t[col]
			if !present || v == nil {
				cs.missing[table] = true
				return
			}
			parts[k] = *v
		}
		cs.keys[table][strings.Join(parts, keySep)] = struct{}{}
	}
	for _, txn := range txns {
		for _, table := range txn.Truncated {
			cs.truncated[table] = true
		}
		for _, ch := range txn.Changes {
			if _, ok := cs.keys[ch.Relation]; !ok {
				cs.keys[ch.Relation] = map[string]struct{}{}
			}
			add(ch.Relation, ch.Old)
			add(ch.Relation, ch.New)
		}
	}
	return cs
}
