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

// Delta is one round's change to one live query.
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

// Engine maintains live queries from a capture stream.
type Engine struct {
	pool    *pgxpool.Pool
	capture *Capture

	queries []*liveQuery
	pending []*Txn
	round   uint64

	// VerifyEachRound re-runs every query at the round snapshot and fails the
	// round if the maintained result differs. It is the differential oracle.
	VerifyEachRound bool
	// Trace, when set, receives a line per applied or held transaction.
	Trace func(string)

	Stats Stats
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

// Stats counts what the engine did, so tests can show which paths ran.
type Stats struct {
	Rounds       int
	Applied      int // transactions applied
	Held         int // transactions decoded before a marker but not yet visible
	Keyed        int // keyed maintenance passes (lineage or group)
	Rebuilds     int // full re-evaluations (recompute mode, truncation)
	Verified     int // oracle comparisons
	ExpandedKeys int // preserved-side keys added for outer joins
}

// NewEngine creates an engine over a pool and an open capture.
func NewEngine(pool *pgxpool.Pool, capture *Capture) *Engine {
	return &Engine{pool: pool, capture: capture}
}

type liveQuery struct {
	id      string
	plan    *Plan
	args    []any
	columns []string
	ready   bool

	result *keyedSet // lineage mode: result rows; recompute mode: rows by multiset ID
	input  *keyedSet // group mode: input rows (values = group keys)
	join   *keyedSet // outer joins: lineage of the FROM clause
	groups map[string]*keyedRow

	// Last round's changed and expanded keys, for divergence reports.
	lastKeys, lastExpanded []map[string]struct{}
}

// Register compiles a query. Its initial result is computed in the next round.
func (e *Engine) Register(ctx context.Context, id, sql string, args ...any) (*Plan, error) {
	conn, err := e.pool.Acquire(ctx)
	if err != nil {
		return nil, fmt.Errorf("livequery: acquire for compile: %w", err)
	}
	defer conn.Release()
	plan, err := Compile(ctx, conn.Conn(), sql)
	if err != nil {
		return nil, err
	}
	if plan.NParams != len(args) {
		return nil, fmt.Errorf("livequery: query takes %d parameters, got %d", plan.NParams, len(args))
	}
	e.queries = append(e.queries, &liveQuery{id: id, plan: plan, args: args})
	return plan, nil
}

// Result returns the engine's current rows for a query.
func (e *Engine) Result(id string) []Row {
	for _, q := range e.queries {
		if q.id == id {
			return q.rows()
		}
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
			var pgVisible bool
			if err := tx.QueryRow(ctx, "SELECT pg_visible_in_snapshot($1::text::xid8, pg_current_snapshot())",
				strconv.FormatUint(full, 10)).Scan(&pgVisible); err != nil {
				return nil, fmt.Errorf("livequery: check visibility: %w", err)
			}
			if pgVisible != snap.Visible(full) {
				return nil, fmt.Errorf("livequery: round %d: xid %d visibility %v, PostgreSQL says %v (snapshot %s, ref %d)",
					e.round, full, snap.Visible(full), pgVisible, snapText, ref)
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
	for _, q := range e.queries {
		var d Delta
		var err error
		if !q.ready {
			d, err = q.initialize(ctx, tx)
		} else {
			d, err = q.maintain(ctx, tx, changes, &e.Stats)
		}
		if err != nil {
			return nil, fmt.Errorf("livequery: query %s: %w", q.id, err)
		}
		d.Query, d.Snapshot, d.Xids = q.id, snapText, xids
		deltas = append(deltas, d)
	}
	if e.Trace != nil {
		e.Trace(fmt.Sprintf("round %d snapshot %s ref %d marker %s", e.round, snapText, ref, markerLSN))
		for _, txn := range visible {
			e.Trace(fmt.Sprintf("round %d applies xid %d end %s: %s", e.round, FullXid(txn.Xid, ref), txn.EndLSN, describeTxn(txn)))
		}
		for _, txn := range held {
			e.Trace(fmt.Sprintf("round %d holds xid %d end %s", e.round, FullXid(txn.Xid, ref), txn.EndLSN))
		}
	}
	if e.VerifyEachRound {
		for _, q := range e.queries {
			if err := q.verify(ctx, tx); err != nil {
				return nil, fmt.Errorf("livequery: round %d snapshot %s: query %s: %w", e.round, snapText, q.id, err)
			}
			e.Stats.Verified++
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("livequery: commit round: %w", err)
	}
	e.pending = held
	e.confirm(visible, held, markerLSN)
	return deltas, nil
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
	for _, q := range e.queries {
		for oid, name := range q.plan.TableNames {
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
	for _, q := range e.queries {
		for _, rel := range q.plan.Relations {
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
		set := cs.keys[table]
		if set == nil {
			set = map[string]struct{}{}
			cs.keys[table] = set
		}
		set[joinKey(parts)] = struct{}{}
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

func joinKey(parts []string) string {
	out := parts[0]
	for _, p := range parts[1:] {
		out += keySep + p
	}
	return out
}

// touched reports whether the change set affects any table the query reads,
// and whether only a full re-evaluation is safe.
func (q *liveQuery) touched(cs changeSet) (any, full bool) {
	for table := range q.plan.Tables {
		if cs.truncated[table] || cs.missing[table] {
			return true, true
		}
		if _, ok := cs.keys[table]; ok {
			any = true
		}
	}
	return any, false
}

// keysFor returns the changed keys per relation.
func (q *liveQuery) keysFor(cs changeSet) []map[string]struct{} {
	out := make([]map[string]struct{}, len(q.plan.Relations))
	for i, rel := range q.plan.Relations {
		out[i] = map[string]struct{}{}
		for key := range cs.keys[rel.OID] {
			out[i][key] = struct{}{}
		}
	}
	return out
}

func (q *liveQuery) initialize(ctx context.Context, tx pgx.Tx) (Delta, error) {
	if err := q.rebuild(ctx, tx); err != nil {
		return Delta{}, err
	}
	q.ready = true
	return Delta{Reset: true, Rows: q.rows()}, nil
}

func (q *liveQuery) maintain(ctx context.Context, tx pgx.Tx, cs changeSet, stats *Stats) (Delta, error) {
	any, full := q.touched(cs)
	if !any {
		return Delta{}, nil
	}
	before := q.snapshotRows()
	var err error
	switch {
	case full || q.plan.Mode == ModeRecompute:
		stats.Rebuilds++
		err = q.rebuild(ctx, tx)
	case q.plan.Mode == ModeLineage:
		stats.Keyed++
		err = q.maintainLineage(ctx, tx, q.keysFor(cs), stats)
	case q.plan.Mode == ModeGroup:
		stats.Keyed++
		err = q.maintainGroup(ctx, tx, q.keysFor(cs), stats)
	}
	if err != nil {
		return Delta{}, err
	}
	return diffRows(before, q.snapshotRows()), nil
}

// rebuild evaluates the query from scratch at the round snapshot.
func (q *liveQuery) rebuild(ctx context.Context, tx pgx.Tx) error {
	p := q.plan
	switch p.Mode {
	case ModeRecompute:
		names, rows, err := queryText(ctx, tx, p.SQL, q.args)
		if err != nil {
			return err
		}
		q.columns = names
		q.result = newKeyedSet(0)
		seen := map[string]int{}
		for _, values := range rows {
			enc := encodeValues(values)
			seen[enc]++
			q.result.put(rowID("r", enc, strconv.Itoa(seen[enc])), &keyedRow{values: values, enc: enc})
		}
		return nil
	case ModeLineage:
		fresh, shape, err := q.readKeyed(ctx, tx, p.RowSQL, nil, false)
		if err != nil {
			return err
		}
		q.columns = shape.names
		q.result = newKeyedSet(len(p.Relations))
		for id, r := range fresh {
			q.result.put(id, r)
		}
	case ModeGroup:
		fresh, _, err := q.readKeyed(ctx, tx, p.RowSQL, nil, true)
		if err != nil {
			return err
		}
		q.input = newKeyedSet(len(p.Relations))
		for id, r := range fresh {
			q.input.put(id, r)
		}
		q.groups = map[string]*keyedRow{}
		if err := q.readGroups(ctx, tx, p.OutputSQL, nil); err != nil {
			return err
		}
	}
	if p.HasOuterJoins() {
		fresh, _, err := q.readKeyed(ctx, tx, p.JoinSQL, nil, false)
		if err != nil {
			return err
		}
		q.join = newKeyedSet(len(p.Relations))
		for id, r := range fresh {
			q.join.put(id, r)
		}
	}
	return nil
}

// readKeyed runs a lineage-bearing query. With groupValues, row values are
// the hidden group keys instead of the visible columns.
func (q *liveQuery) readKeyed(ctx context.Context, tx pgx.Tx, sql string, extra []any, groupValues bool) (map[string]*keyedRow, resultShape, error) {
	names, rows, err := queryText(ctx, tx, sql, append(slices.Clone(q.args), extra...))
	if err != nil {
		return nil, resultShape{}, err
	}
	shape, err := shapeOf(names, q.plan.Relations, q.plan.NGroups)
	if err != nil {
		return nil, shape, err
	}
	out := make(map[string]*keyedRow, len(rows))
	for _, values := range rows {
		r := &keyedRow{lineage: shape.lineageOf(values)}
		if groupValues {
			r.values = pick(values, shape.groups)
		} else {
			r.values = pick(values, shape.visible)
		}
		r.enc = encodeValues(r.values)
		out[rowID(r.lineage...)] = r
	}
	return out, shape, nil
}

// readGroups runs the grouped output query and stores rows by group key.
func (q *liveQuery) readGroups(ctx context.Context, tx pgx.Tx, sql string, extra []any) error {
	names, rows, err := queryText(ctx, tx, sql, append(slices.Clone(q.args), extra...))
	if err != nil {
		return err
	}
	shape, err := shapeOf(names, nil, q.plan.NGroups)
	if err != nil {
		return err
	}
	q.columns = shape.names
	for _, values := range rows {
		visible := pick(values, shape.visible)
		q.groups[encodeValues(pick(values, shape.groups))] = &keyedRow{values: visible, enc: encodeValues(visible)}
	}
	return nil
}

// expand adds preserved-side keys for outer joins and refreshes the join
// lineage. Rows NULL-extended on a changed relation may appear or vanish, so
// the preserved rows that matched it before or match it now are re-read too.
func (q *liveQuery) expand(ctx context.Context, tx pgx.Tx, keys []map[string]struct{}, stats *Stats) ([]map[string]struct{}, error) {
	p := q.plan
	if !p.HasOuterJoins() {
		return keys, nil
	}
	out := make([]map[string]struct{}, len(keys))
	for i := range keys {
		out[i] = map[string]struct{}{}
		for k := range keys[i] {
			out[i][k] = struct{}{}
		}
	}
	addPreserved := func(i int, lineage []string) {
		for _, pr := range p.Relations[i].Preserved {
			if lineage[pr] == "" {
				continue
			}
			if _, ok := out[pr][lineage[pr]]; !ok {
				stats.ExpandedKeys++
				out[pr][lineage[pr]] = struct{}{}
			}
		}
	}
	for i, set := range keys {
		if len(set) == 0 || len(p.Relations[i].Preserved) == 0 {
			continue
		}
		only := make([]map[string]struct{}, len(keys))
		only[i] = set
		for id := range q.join.matching(only) {
			addPreserved(i, q.join.rows[id].lineage)
		}
		fresh, _, err := q.readKeyed(ctx, tx, p.JoinRestricted[i], keyArrays(set, len(p.Relations[i].Keys)), false)
		if err != nil {
			return nil, err
		}
		for _, r := range fresh {
			addPreserved(i, r.lineage)
		}
	}
	joinRows, err := q.readRestricted(ctx, tx, p.JoinRestricted, out, false)
	if err != nil {
		return nil, err
	}
	q.join.replace(out, joinRows)
	q.lastKeys, q.lastExpanded = keys, out
	return out, nil
}

func describeKeys(keys []map[string]struct{}) string {
	var parts []string
	for i, set := range keys {
		var ks []string
		for k := range set {
			ks = append(ks, strings.ReplaceAll(k, keySep, "|"))
		}
		sort.Strings(ks)
		parts = append(parts, fmt.Sprintf("r%d=%v", i, ks))
	}
	return strings.Join(parts, " ")
}

// readRestricted re-reads the rows matching keys, one restricted query per
// relation with changed keys.
func (q *liveQuery) readRestricted(ctx context.Context, tx pgx.Tx, restricted []string, keys []map[string]struct{}, groupValues bool) (map[string]*keyedRow, error) {
	out := map[string]*keyedRow{}
	for i, set := range keys {
		if len(set) == 0 {
			continue
		}
		fresh, _, err := q.readKeyed(ctx, tx, restricted[i], keyArrays(set, len(q.plan.Relations[i].Keys)), groupValues)
		if err != nil {
			return nil, err
		}
		for id, r := range fresh {
			out[id] = r
		}
	}
	return out, nil
}

func (q *liveQuery) maintainLineage(ctx context.Context, tx pgx.Tx, keys []map[string]struct{}, stats *Stats) error {
	keys, err := q.expand(ctx, tx, keys, stats)
	if err != nil {
		return err
	}
	fresh, err := q.readRestricted(ctx, tx, q.plan.RowRestricted, keys, false)
	if err != nil {
		return err
	}
	q.result.replace(keys, fresh)
	return nil
}

func (q *liveQuery) maintainGroup(ctx context.Context, tx pgx.Tx, keys []map[string]struct{}, stats *Stats) error {
	keys, err := q.expand(ctx, tx, keys, stats)
	if err != nil {
		return err
	}
	fresh, err := q.readRestricted(ctx, tx, q.plan.RowRestricted, keys, true)
	if err != nil {
		return err
	}
	affected := map[string][]*string{}
	for id := range q.input.matching(keys) {
		r := q.input.rows[id]
		affected[r.enc] = r.values
	}
	for _, r := range fresh {
		affected[r.enc] = r.values
	}
	q.input.replace(keys, fresh)
	if len(affected) == 0 {
		return nil
	}
	arrays := make([][]*string, q.plan.NGroups)
	for enc, values := range affected {
		delete(q.groups, enc)
		for k := range arrays {
			arrays[k] = append(arrays[k], values[k])
		}
	}
	extra := make([]any, len(arrays))
	for k := range arrays {
		extra[k] = arrays[k]
	}
	return q.readGroups(ctx, tx, q.plan.OutputRestricted, extra)
}

// snapshotRows returns the current result by row ID.
func (q *liveQuery) snapshotRows() map[string]*keyedRow {
	out := map[string]*keyedRow{}
	if q.plan.Mode == ModeGroup {
		for enc, r := range q.groups {
			out[rowID("g", enc)] = r
		}
		return out
	}
	for id, r := range q.result.rows {
		out[id] = r
	}
	return out
}

func (q *liveQuery) rows() []Row {
	snap := q.snapshotRows()
	out := make([]Row, 0, len(snap))
	for id, r := range snap {
		out = append(out, Row{ID: id, Values: r.values})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

func diffRows(before, after map[string]*keyedRow) Delta {
	var d Delta
	for id := range before {
		if _, ok := after[id]; !ok {
			d.Removes = append(d.Removes, id)
		}
	}
	for id, r := range after {
		if old, ok := before[id]; !ok || old.enc != r.enc {
			d.Upserts = append(d.Upserts, Row{ID: id, Values: r.values})
		}
	}
	sort.Strings(d.Removes)
	sort.Slice(d.Upserts, func(i, j int) bool { return d.Upserts[i].ID < d.Upserts[j].ID })
	return d
}

// verify compares the maintained result with the query run at the round
// snapshot, as multisets of rows.
func (q *liveQuery) verify(ctx context.Context, tx pgx.Tx) error {
	// Internal caches first, so a failure names the layer that diverged.
	if q.join != nil {
		if err := q.verifyKeyed(ctx, tx, "join lineage", q.plan.JoinSQL, q.join, false); err != nil {
			return err
		}
	}
	if q.input != nil {
		if err := q.verifyKeyed(ctx, tx, "group input", q.plan.RowSQL, q.input, true); err != nil {
			return err
		}
	}
	_, rows, err := queryText(ctx, tx, q.plan.SQL, q.args)
	if err != nil {
		return fmt.Errorf("oracle query: %w", err)
	}
	want := map[string]int{}
	for _, values := range rows {
		want[encodeValues(values)]++
	}
	got := map[string]int{}
	for _, r := range q.snapshotRows() {
		got[r.enc]++
	}
	for enc, n := range want {
		if got[enc] != n {
			return fmt.Errorf("divergence (mode %s): row %q expected %d times, maintained %d times", q.plan.Mode, printable(enc), n, got[enc])
		}
	}
	for enc, n := range got {
		if want[enc] != n {
			var lineage []string
			for _, r := range q.snapshotRows() {
				if r.enc == enc {
					lineage = r.lineage
				}
			}
			return fmt.Errorf("divergence (mode %s): row %q (lineage %q) maintained %d times, expected %d times",
				q.plan.Mode, printable(enc), lineage, n, want[enc])
		}
	}
	return nil
}

// verifyKeyed compares a lineage-keyed cache with its query at the snapshot.
func (q *liveQuery) verifyKeyed(ctx context.Context, tx pgx.Tx, layer, sql string, set *keyedSet, groupValues bool) error {
	fresh, _, err := q.readKeyed(ctx, tx, sql, nil, groupValues)
	if err != nil {
		return fmt.Errorf("oracle %s: %w", layer, err)
	}
	for id, r := range fresh {
		got, ok := set.rows[id]
		if !ok {
			return fmt.Errorf("divergence in %s: missing row lineage %q values %q", layer, r.lineage, printable(r.enc))
		}
		if got.enc != r.enc {
			return fmt.Errorf("divergence in %s: row lineage %q values %q, expected %q", layer, r.lineage, printable(got.enc), printable(r.enc))
		}
	}
	for id, r := range set.rows {
		if _, ok := fresh[id]; !ok {
			var now []string
			for i, key := range r.lineage {
				if key == "" {
					continue
				}
				rows, _, err := q.readKeyed(ctx, tx, q.plan.JoinRestricted[i], keyArrays(map[string]struct{}{key: {}}, len(q.plan.Relations[i].Keys)), false)
				if err != nil {
					now = append(now, fmt.Sprintf("r%d error: %v", i, err))
				}
				for _, nr := range rows {
					now = append(now, fmt.Sprintf("r%d:%q", i, nr.lineage))
				}
			}
			return fmt.Errorf("divergence in %s: stale row lineage %q values %q; at snapshot these keys join as %v; round keys %s; expanded %s",
				layer, r.lineage, printable(r.enc), now, describeKeys(q.lastKeys), describeKeys(q.lastExpanded))
		}
	}
	return nil
}

func printable(enc string) string {
	out := []byte{}
	for i := 0; i < len(enc); i++ {
		switch {
		case enc[i] == 0 && i+1 < len(enc) && enc[i+1] == 'N':
			out = append(out, "|NULL"...)
			i++
		case enc[i] == 0 && i+1 < len(enc) && enc[i+1] == 'V':
			out = append(out, '|')
			i++
		default:
			out = append(out, enc[i])
		}
	}
	return string(out)
}
