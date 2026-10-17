package livequery

import (
	"context"
	"fmt"
	"slices"
	"sort"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
)

// planGroup is every subscription to one SQL text: one compiled plan, one
// set of batched statements per round, however many members it has.
type planGroup struct {
	plan    *Plan
	members []*member // indexed by member.idx
	byArgs  map[string]*member
	stats   *Stats
	engine  *Engine

	// pendingPlan replaces plan at the next step after a schema change that
	// kept the result's shape: every member is re-read and diffed.
	pendingPlan *Plan
	// skip leaves the group out of the current round.
	skip bool

	// Last round's changed and expanded keys, for divergence reports.
	lastKeys, lastExpanded []map[string]struct{}

	// session, when set, is who the group reads as (RLS mode); deps is what
	// its tables' policies read, re-read after a schema change (depsStale).
	session   *Session
	deps      policyDeps
	depsStale bool
}

func (g *planGroup) key() string { return g.plan.SQL + g.session.key() }

// stepAs steps the group as its session.
func (g *planGroup) stepAs(ctx context.Context, tx pgx.Tx, cs changeSet) (map[*member]Delta, error) {
	if err := g.session.enter(ctx, tx); err != nil {
		return nil, err
	}
	out, err := g.step(ctx, tx, cs)
	if err != nil {
		return nil, err
	}
	return out, leave(ctx, tx)
}

// verify compares every member with PostgreSQL, as the group's session.
func (g *planGroup) verify(ctx context.Context, tx pgx.Tx) error {
	if g.session != nil {
		if err := g.session.enter(ctx, tx); err != nil {
			return err
		}
	}
	for _, m := range g.members {
		if err := m.verify(ctx, tx); err != nil {
			return fmt.Errorf("subscription %v: %w", m.ids, err)
		}
		g.stats.Verified++
	}
	if g.session != nil {
		return leave(ctx, tx)
	}
	return nil
}

// member is one distinct argument list of a plan group. Subscriptions with
// identical arguments share it.
type member struct {
	group   *planGroup
	idx     int32
	ids     []string
	args    []any
	argText []*string
	ready   bool
	columns []string
	win     *window         // ordered windows
	joined  map[string]bool // subscribers that joined a maintained result

	result *keyedSet   // lineage mode: result rows; recompute mode: rows by multiset ID
	input  *keyedSet   // group mode: input rows (values = group keys)
	join   *keyedSet   // outer joins: lineage of the FROM clause
	semi   []*keyedSet // keyed subqueries: pairs of FROM rows and subquery rows
	groups map[string]*keyedRow
	agg    map[string]*aggGroup // incremental aggregates: state by group
}

// aggCount is the number of aggregate argument columns the plan reads.
func (g *planGroup) aggCount() int {
	if g.plan.Agg == nil {
		return 0
	}
	return len(g.plan.Agg.Specs)
}

// orderCount is the number of the query's own sort items.
func (g *planGroup) orderCount() int {
	if g.plan.Order == nil {
		return 0
	}
	return g.plan.Order.NUser
}

// batchArgs returns the leading parameters of a batched statement for the
// given members: their indexes, then one text array per query parameter.
func (g *planGroup) batchArgs(ms []*member) []any {
	idx := make([]int32, len(ms))
	params := make([][]*string, g.plan.NParams)
	for i, m := range ms {
		idx[i] = m.idx
		for k := range params {
			params[k] = append(params[k], m.argText[k])
		}
	}
	out := []any{idx}
	for _, p := range params {
		out = append(out, p)
	}
	return out
}

// read runs a batched statement for the members and splits the rows by
// member.
func (g *planGroup) read(ctx context.Context, tx pgx.Tx, sql string, ms []*member, extra []any) (map[*member][][]*string, resultShape, error) {
	g.stats.Statements++
	start := time.Now()
	names, rows, err := queryText(ctx, tx, sql, append(g.batchArgs(ms), extra...))
	elapsed := time.Since(start)
	g.stats.StatementTime += elapsed
	if g.engine.StatementHook != nil {
		g.engine.StatementHook(sql, len(ms), elapsed)
	}
	if err != nil {
		return nil, resultShape{}, err
	}
	shape, err := shapeOf(names, g.plan.Relations, g.plan.NGroups, g.aggCount(), g.orderCount())
	if err != nil {
		return nil, shape, err
	}
	if shape.sub < 0 {
		return nil, shape, fmt.Errorf("livequery: batched statement has no member column")
	}
	out := make(map[*member][][]*string, len(ms))
	for _, m := range ms {
		out[m] = nil
	}
	for _, values := range rows {
		n, err := strconv.Atoi(*values[shape.sub])
		if err != nil || n < 0 || n >= len(g.members) {
			return nil, shape, fmt.Errorf("livequery: bad member index %v", values[shape.sub])
		}
		m := g.members[n]
		out[m] = append(out[m], values)
	}
	return out, shape, nil
}

// readKeyed runs a lineage-bearing batched statement. With groupValues, row
// values are the hidden group keys instead of the visible columns.
func (g *planGroup) readKeyed(ctx context.Context, tx pgx.Tx, sql string, ms []*member, extra []any, groupValues bool) (map[*member]map[string]*keyedRow, resultShape, error) {
	byMember, shape, err := g.read(ctx, tx, sql, ms, extra)
	if err != nil {
		return nil, shape, err
	}
	out := make(map[*member]map[string]*keyedRow, len(byMember))
	for m, rows := range byMember {
		set := make(map[string]*keyedRow, len(rows))
		for _, values := range rows {
			r := &keyedRow{lineage: shape.lineageOf(values)}
			if groupValues {
				r.values = pick(values, shape.groups)
				r.aggs = pick(values, shape.aggs)
				r.group = encodeValues(r.values)
			} else {
				r.values = pick(values, shape.visible)
			}
			if g.plan.Order != nil {
				r.sort = pick(values, shape.order)
				for _, rel := range g.plan.Outer {
					r.sort = append(r.sort, pick(values, shape.lineage[rel])...)
				}
			}
			r.enc = encodeValues(append(slices.Clone(r.values), r.aggs...))
			set[rowID(r.lineage...)] = r
		}
		out[m] = set
	}
	return out, shape, nil
}

// readRestricted re-reads the rows matching keys for every member, one
// batched statement per relation with changed keys.
func (g *planGroup) readRestricted(ctx context.Context, tx pgx.Tx, restricted []string, keys []map[string]struct{}, ms []*member, groupValues bool) (map[*member]map[string]*keyedRow, error) {
	out := make(map[*member]map[string]*keyedRow, len(ms))
	for _, m := range ms {
		out[m] = map[string]*keyedRow{}
	}
	for i, set := range keys {
		if len(set) == 0 || restricted[i] == "" {
			continue
		}
		fresh, _, err := g.readKeyed(ctx, tx, restricted[i], ms, keyArrays(set, len(g.plan.Relations[i].Keys)), groupValues)
		if err != nil {
			return nil, err
		}
		for m, rows := range fresh {
			for id, r := range rows {
				out[m][id] = r
			}
		}
	}
	return out, nil
}

// readGroups runs the grouped output statement and stores rows by group key.
func (g *planGroup) readGroups(ctx context.Context, tx pgx.Tx, sql string, ms []*member, extra []any) error {
	byMember, shape, err := g.read(ctx, tx, sql, ms, extra)
	if err != nil {
		return err
	}
	for m, rows := range byMember {
		m.columns = shape.names
		for _, values := range rows {
			visible := pick(values, shape.visible)
			m.groups[encodeValues(pick(values, shape.groups))] = &keyedRow{values: visible, enc: encodeValues(visible)}
		}
	}
	return nil
}

// step runs one round for the group: initial results for new members, then
// maintenance for the rest. It returns each changed member's delta.
func (g *planGroup) step(ctx context.Context, tx pgx.Tx, cs changeSet) (map[*member]Delta, error) {
	out := map[*member]Delta{}
	var fresh, ready []*member
	for _, m := range g.members {
		if m.ready {
			ready = append(ready, m)
		} else {
			fresh = append(fresh, m)
		}
	}
	// A recompiled plan: capture the results under the old plan, then
	// re-read everything under the new one.
	force := g.pendingPlan != nil
	before := make(map[*member]map[string]*keyedRow, len(ready))
	beforeOrder := make(map[*member][]string, len(ready))
	if force {
		for _, m := range ready {
			before[m] = m.snapshotRows()
			if m.win != nil {
				beforeOrder[m] = slices.Clone(m.win.visible())
			}
		}
		g.plan, g.pendingPlan = g.pendingPlan, nil
		for _, m := range g.members {
			if err := m.reset(); err != nil {
				return nil, err
			}
		}
		for _, m := range ready {
			m.ready = true // re-read below and diffed against before
		}
	}
	if len(fresh) > 0 {
		if err := g.rebuild(ctx, tx, fresh); err != nil {
			return nil, err
		}
		for _, m := range fresh {
			m.ready = true
			out[m] = Delta{Reset: true, Rows: m.rows()}
		}
	}
	if len(ready) == 0 {
		return out, nil
	}
	touched, full := g.touched(cs)
	if force {
		touched, full = true, true
	}
	if !touched {
		return out, nil
	}
	if !force {
		for _, m := range ready {
			before[m] = m.snapshotRows()
			if m.win != nil {
				beforeOrder[m] = slices.Clone(m.win.visible())
			}
		}
	}
	var err error
	switch {
	case full || g.plan.Mode == ModeRecompute:
		g.stats.Rebuilds++
		err = g.rebuild(ctx, tx, ready)
	case g.plan.Mode == ModeLineage && g.plan.Order != nil:
		g.stats.Keyed++
		err = g.maintainOrder(ctx, tx, ready, g.keysFor(cs))
	case g.plan.Mode == ModeLineage:
		g.stats.Keyed++
		err = g.maintainLineage(ctx, tx, ready, g.keysFor(cs))
	case g.plan.Mode == ModeGroup && g.plan.Agg != nil:
		g.stats.Keyed++
		g.stats.Incremental++
		err = g.maintainAgg(ctx, tx, ready, g.keysFor(cs))
	case g.plan.Mode == ModeGroup:
		g.stats.Keyed++
		err = g.maintainGroup(ctx, tx, ready, g.keysFor(cs))
	}
	if err != nil {
		return nil, err
	}
	for _, m := range ready {
		d := diffRows(before[m], m.snapshotRows())
		if m.win != nil {
			m.orderDelta(&d, beforeOrder[m])
		}
		if !d.Empty() {
			out[m] = d
		}
	}
	return out, nil
}

// touched reports whether the change set affects any table the plan reads,
// and whether only a full re-evaluation is safe.
func (g *planGroup) touched(cs changeSet) (any, full bool) {
	// A session group's rows also depend on what its policies read: a change
	// there re-reads it, as does any change when a policy is opaque.
	if g.session != nil {
		for table := range g.deps.tables {
			if _, ok := cs.keys[table]; ok || cs.truncated[table] {
				return true, true
			}
		}
		if g.deps.opaque && (len(cs.keys) > 0 || len(cs.truncated) > 0) {
			return true, true
		}
	}
	for table := range g.plan.Tables {
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
func (g *planGroup) keysFor(cs changeSet) []map[string]struct{} {
	out := make([]map[string]struct{}, len(g.plan.Relations))
	for i, rel := range g.plan.Relations {
		out[i] = map[string]struct{}{}
		for key := range cs.keys[rel.OID] {
			out[i][key] = struct{}{}
		}
	}
	return out
}

// rebuild evaluates the query from scratch for the members at the round
// snapshot.
func (g *planGroup) rebuild(ctx context.Context, tx pgx.Tx, ms []*member) error {
	p := g.plan
	switch p.Mode {
	case ModeRecompute:
		byMember, shape, err := g.read(ctx, tx, p.BatchedSQL, ms, nil)
		if err != nil {
			return err
		}
		for m, rows := range byMember {
			m.columns = shape.names
			m.result = newKeyedSet(0)
			seen := map[string]int{}
			for _, values := range rows {
				visible := pick(values, shape.visible)
				enc := encodeValues(visible)
				seen[enc]++
				m.result.put(rowID("r", enc, strconv.Itoa(seen[enc])), &keyedRow{values: visible, enc: enc})
			}
		}
		return nil
	case ModeLineage:
		if p.Order != nil {
			return g.rebuildOrder(ctx, tx, ms)
		}
		fresh, shape, err := g.readKeyed(ctx, tx, p.RowSQL, ms, nil, false)
		if err != nil {
			return err
		}
		for m, rows := range fresh {
			m.columns = shape.names
			m.result = newKeyedSet(len(p.Relations))
			for id, r := range rows {
				m.result.put(id, r)
			}
		}
	case ModeGroup:
		fresh, _, err := g.readKeyed(ctx, tx, p.RowSQL, ms, nil, true)
		if err != nil {
			return err
		}
		for m, rows := range fresh {
			m.input = newKeyedSet(len(p.Relations))
			for id, r := range rows {
				m.input.put(id, r)
			}
			m.groups = map[string]*keyedRow{}
		}
		if p.Agg != nil {
			if err := g.rebuildAgg(ctx, tx, ms); err != nil {
				return err
			}
		} else if err := g.readGroups(ctx, tx, p.OutputSQL, ms, nil); err != nil {
			return err
		}
	}
	if p.HasOuterJoins() {
		fresh, _, err := g.readKeyed(ctx, tx, p.JoinSQL, ms, nil, false)
		if err != nil {
			return err
		}
		for m, rows := range fresh {
			m.join = newKeyedSet(len(p.Relations))
			for id, r := range rows {
				m.join.put(id, r)
			}
		}
	}
	for _, m := range ms {
		m.semi = make([]*keyedSet, len(p.Subqueries))
	}
	for s, sq := range p.Subqueries {
		fresh, _, err := g.readKeyed(ctx, tx, sq.SQL, ms, nil, false)
		if err != nil {
			return err
		}
		for m, rows := range fresh {
			m.semi[s] = newKeyedSet(len(p.Relations))
			for id, r := range rows {
				m.semi[s].put(id, r)
			}
		}
	}
	return nil
}

// expand adds preserved-side keys for outer joins and refreshes the join
// lineage. Rows NULL-extended on a changed relation may appear or vanish, so
// the preserved rows that matched it before (in any member's join lineage)
// or match it now are re-read too. Every member uses the same expanded keys:
// re-reading and replacing extra rows is harmless.
func (g *planGroup) expand(ctx context.Context, tx pgx.Tx, ms []*member, keys []map[string]struct{}) ([]map[string]struct{}, error) {
	p := g.plan
	g.lastKeys, g.lastExpanded = keys, keys
	if !p.expands() {
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
				g.stats.ExpandedKeys++
				out[pr][lineage[pr]] = struct{}{}
			}
		}
	}
	for i, set := range keys {
		if len(set) == 0 || len(p.Relations[i].Preserved) == 0 || !p.HasOuterJoins() {
			continue
		}
		only := make([]map[string]struct{}, len(keys))
		only[i] = set
		for _, m := range ms {
			for id := range m.join.matching(only) {
				addPreserved(i, m.join.rows[id].lineage)
			}
		}
		fresh, _, err := g.readKeyed(ctx, tx, p.JoinRestricted[i], ms, keyArrays(set, len(p.Relations[i].Keys)), false)
		if err != nil {
			return nil, err
		}
		for _, rows := range fresh {
			for _, r := range rows {
				addPreserved(i, r.lineage)
			}
		}
	}
	// A changed subquery row can change the condition for the outer rows it
	// matched before (in the pair cache) or matches now.
	for _, sq := range p.Subqueries {
		set := keys[sq.Rel]
		if len(set) == 0 {
			continue
		}
		addOuter := func(lineage []string) {
			for _, o := range p.Outer {
				if lineage[o] == "" {
					continue
				}
				if _, ok := out[o][lineage[o]]; !ok {
					g.stats.ExpandedKeys++
					out[o][lineage[o]] = struct{}{}
				}
			}
		}
		only := make([]map[string]struct{}, len(keys))
		only[sq.Rel] = set
		for _, m := range ms {
			for id := range m.semi[g.subIndex(sq.Rel)].matching(only) {
				addOuter(m.semi[g.subIndex(sq.Rel)].rows[id].lineage)
			}
		}
		fresh, _, err := g.readKeyed(ctx, tx, sq.Restricted[sq.Rel], ms, keyArrays(set, len(p.Relations[sq.Rel].Keys)), false)
		if err != nil {
			return nil, err
		}
		for _, rows := range fresh {
			for _, r := range rows {
				addOuter(r.lineage)
			}
		}
	}
	if p.HasOuterJoins() {
		joinRows, err := g.readRestricted(ctx, tx, p.JoinRestricted, out, ms, false)
		if err != nil {
			return nil, err
		}
		for _, m := range ms {
			m.join.replace(out, joinRows[m])
		}
	}
	for s, sq := range p.Subqueries {
		pairs, err := g.readRestricted(ctx, tx, sq.Restricted, out, ms, false)
		if err != nil {
			return nil, err
		}
		for _, m := range ms {
			m.semi[s].replace(g.semiKeys(sq, out), pairs[m])
		}
	}
	g.lastExpanded = out
	return out, nil
}

// subIndex returns the subquery plan index for a subquery relation.
func (g *planGroup) subIndex(rel int) int {
	for s, sq := range g.plan.Subqueries {
		if sq.Rel == rel {
			return s
		}
	}
	return -1
}

// semiKeys limits keys to the relations a subquery's pair cache contains.
func (g *planGroup) semiKeys(sq SubqueryPlan, keys []map[string]struct{}) []map[string]struct{} {
	out := make([]map[string]struct{}, len(keys))
	for i := range keys {
		if sq.Restricted[i] != "" {
			out[i] = keys[i]
		}
	}
	return out
}

func (g *planGroup) maintainLineage(ctx context.Context, tx pgx.Tx, ms []*member, keys []map[string]struct{}) error {
	keys, err := g.expand(ctx, tx, ms, keys)
	if err != nil {
		return err
	}
	fresh, err := g.readRestricted(ctx, tx, g.plan.RowRestricted, keys, ms, false)
	if err != nil {
		return err
	}
	for _, m := range ms {
		m.result.replace(keys, fresh[m])
	}
	return nil
}

func (g *planGroup) maintainGroup(ctx context.Context, tx pgx.Tx, ms []*member, keys []map[string]struct{}) error {
	keys, err := g.expand(ctx, tx, ms, keys)
	if err != nil {
		return err
	}
	fresh, err := g.readRestricted(ctx, tx, g.plan.RowRestricted, keys, ms, true)
	if err != nil {
		return err
	}
	// Re-aggregate exactly the groups whose input changed, per member.
	var subs []int32
	values := make([][]*string, g.plan.NGroups)
	var touched []*member
	for _, m := range ms {
		affected := map[string][]*string{}
		for id := range m.input.matching(keys) {
			r := m.input.rows[id]
			affected[r.group] = r.values
		}
		for _, r := range fresh[m] {
			affected[r.group] = r.values
		}
		m.input.replace(keys, fresh[m])
		if len(affected) == 0 {
			continue
		}
		touched = append(touched, m)
		for enc, vals := range affected {
			delete(m.groups, enc)
			subs = append(subs, m.idx)
			for k := range values {
				values[k] = append(values[k], vals[k])
			}
		}
	}
	if len(touched) == 0 {
		return nil
	}
	extra := []any{subs}
	for _, v := range values {
		extra = append(extra, v)
	}
	return g.readGroups(ctx, tx, g.plan.OutputRestricted, touched, extra)
}

// size is the number of rows the member holds for subscribers: its result,
// or its whole window for an ordered query.
func (m *member) size() int {
	switch {
	case m.win != nil:
		return len(m.win.ids)
	case m.group.plan.Mode == ModeGroup:
		return len(m.groups)
	case m.result != nil:
		return len(m.result.rows)
	}
	return 0
}

// snapshotRows returns the member's current result by row ID.
func (m *member) snapshotRows() map[string]*keyedRow {
	out := map[string]*keyedRow{}
	if m.group.plan.Mode == ModeGroup {
		for enc, r := range m.groups {
			out[rowID("g", enc)] = r
		}
		return out
	}
	if m.win != nil {
		for _, id := range m.win.visible() {
			out[id] = m.result.rows[id]
		}
		return out
	}
	for id, r := range m.result.rows {
		out[id] = r
	}
	return out
}

func (m *member) rows() []Row {
	if m.win != nil && m.group.plan.Order.Ordered {
		var out []Row
		for _, id := range m.win.visible() {
			out = append(out, Row{ID: id, Values: m.result.rows[id].values})
		}
		return out
	}
	snap := m.snapshotRows()
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
