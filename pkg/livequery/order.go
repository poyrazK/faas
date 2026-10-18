package livequery

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
)

// window is a member's ordered window: the first size rows of the query by
// the plan's sort items. The rows live in member.result.
type window struct {
	ids     []string
	size    int64
	bounded bool // false: no LIMIT, so the window is the whole result
	offset  int64
}

func newWindow(o *OrderPlan, args []*string) (*window, error) {
	limit, bounded, err := o.Limit.eval(args)
	if err != nil {
		return nil, err
	}
	offset, _, err := o.Offset.eval(args)
	if err != nil {
		return nil, err
	}
	return &window{size: limit + offset, bounded: bounded, offset: offset}, nil
}

// full reports whether the window holds as many rows as it may: rows past it
// may then exist, so whatever leaves the window must be replaced from them.
func (w *window) full() bool {
	return w.bounded && w.size > 0 && int64(len(w.ids)) == w.size
}

// visible returns the IDs of the result rows in order: the window after the
// OFFSET rows.
func (w *window) visible() []string {
	if w.offset >= int64(len(w.ids)) {
		return nil
	}
	return w.ids[w.offset:]
}

// candidate is a row competing for a window place. A marker stands for the
// last row of a full window as it was before the round.
type candidate struct {
	id     string
	row    *keyedRow
	marker bool
}

// rebuildOrder reads every member's window from scratch.
func (g *planGroup) rebuildOrder(ctx context.Context, tx pgx.Tx, ms []*member) error {
	fresh, shape, err := g.readKeyed(ctx, tx, g.plan.RowSQL, ms, nil, false)
	if err != nil {
		return err
	}
	sets := map[*member][]candidate{}
	for _, m := range ms {
		m.columns = shape.names
		m.result = newKeyedSet(len(g.plan.Relations))
		m.win.ids = nil
		for id, r := range fresh[m] {
			sets[m] = append(sets[m], candidate{id: id, row: r})
		}
	}
	return g.settleWindows(ctx, tx, sets)
}

// maintainOrder updates the windows for changed keys. A member none of
// whose window rows changed and that gained no candidate keeps its window; a
// change that leaves every sort value as it was updates rows in place.
func (g *planGroup) maintainOrder(ctx context.Context, tx pgx.Tx, ms []*member, keys []map[string]struct{}) error {
	fresh, err := g.readRestricted(ctx, tx, g.plan.RowRestricted, keys, ms, false)
	if err != nil {
		return err
	}
	sets := map[*member][]candidate{}
	for _, m := range ms {
		if m.win.bounded && m.win.size == 0 {
			continue
		}
		gone := m.result.matching(keys)
		in := fresh[m]
		if len(gone) == 0 && len(in) == 0 {
			continue
		}
		if inPlace(m, gone, in) {
			for id, r := range in {
				m.result.put(id, r)
			}
			continue
		}
		g.stats.WindowRanks++
		var cands []candidate
		for _, id := range m.win.ids {
			if _, changed := gone[id]; !changed {
				cands = append(cands, candidate{id: id, row: m.result.rows[id]})
			}
		}
		for id, r := range in {
			cands = append(cands, candidate{id: id, row: r})
		}
		if m.win.full() {
			last := m.win.ids[len(m.win.ids)-1]
			if _, changed := gone[last]; changed {
				cands = append(cands, candidate{id: last, row: m.result.rows[last], marker: true})
			}
		}
		sets[m] = cands
	}
	return g.settleWindows(ctx, tx, sets)
}

// inPlace reports whether the re-read rows are exactly the changed window
// rows with unchanged sort values, so every row keeps its place.
func inPlace(m *member, gone map[string]struct{}, in map[string]*keyedRow) bool {
	if len(gone) != len(in) {
		return false
	}
	for id, r := range in {
		old, ok := m.result.rows[id]
		if _, changed := gone[id]; !ok || !changed || encodeValues(old.sort) != encodeValues(r.sort) {
			return false
		}
	}
	return true
}

// settleWindows ranks each member's candidates and takes its new window. In a full
// window only candidates up to the old last row may be kept: rows past it
// that the engine does not hold could outrank the rest. When too few remain,
// the window is read again from the query.
func (g *planGroup) settleWindows(ctx context.Context, tx pgx.Tx, sets map[*member][]candidate) error {
	ranked, err := g.rank(ctx, tx, sets)
	if err != nil {
		return err
	}
	var refill []*member
	for m := range sets {
		w := m.win
		boundary := ""
		if w.full() {
			boundary = w.ids[len(w.ids)-1]
		}
		var keep []candidate
		reached := boundary == ""
		for _, c := range ranked[m] {
			if c.marker {
				reached = true
				break
			}
			if w.bounded && int64(len(keep)) == w.size {
				break
			}
			keep = append(keep, c)
			if c.id == boundary {
				reached = true
				break
			}
		}
		if boundary != "" && reached && int64(len(keep)) < w.size {
			refill = append(refill, m)
			continue
		}
		m.setWindow(keep)
	}
	if len(refill) == 0 {
		return nil
	}
	g.stats.WindowRefills += len(refill)
	return g.rebuildOrder(ctx, tx, refill)
}

func (m *member) setWindow(keep []candidate) {
	m.result = newKeyedSet(len(m.group.plan.Relations))
	m.win.ids = make([]string, len(keep))
	for i, c := range keep {
		m.result.put(c.id, c.row)
		m.win.ids[i] = c.id
	}
}

// rank orders every member's candidates with one statement, so PostgreSQL
// decides the order with the query's own types, collations and NULL rules.
func (g *planGroup) rank(ctx context.Context, tx pgx.Tx, sets map[*member][]candidate) (map[*member][]candidate, error) {
	o := g.plan.Order
	var members, idx []int32
	values := make([][]*string, len(o.Items))
	var markers []bool
	var all []candidate
	var owner []*member
	for m, cands := range sets {
		for _, c := range cands {
			members = append(members, m.idx)
			idx = append(idx, int32(len(all)))
			for k := range values {
				values[k] = append(values[k], c.row.sort[k])
			}
			markers = append(markers, c.marker)
			all = append(all, c)
			owner = append(owner, m)
		}
	}
	out := make(map[*member][]candidate, len(sets))
	if len(all) == 0 {
		return out, nil
	}
	args := []any{members, idx}
	for _, v := range values {
		args = append(args, v)
	}
	args = append(args, markers)
	g.stats.Statements++
	start := time.Now()
	_, rows, err := queryText(ctx, tx, o.RankSQL, args)
	elapsed := time.Since(start)
	g.stats.StatementTime += elapsed
	if g.engine.StatementHook != nil {
		g.engine.StatementHook(o.RankSQL, len(sets), elapsed)
	}
	if err != nil {
		return nil, fmt.Errorf("livequery: rank window candidates: %w", err)
	}
	for _, values := range rows {
		i, err := strconv.Atoi(*values[0])
		if err != nil || i < 0 || i >= len(all) {
			return nil, fmt.Errorf("livequery: bad candidate index %v", values[0])
		}
		out[owner[i]] = append(out[owner[i]], all[i])
	}
	return out, nil
}

// orderDelta sets d.Order when the visible order is not the old order with
// the removed rows taken out: rows were added or moved.
func (m *member) orderDelta(d *Delta, before []string) {
	o := m.group.plan.Order
	if o == nil || !o.Ordered {
		return
	}
	after := m.win.visible()
	removed := make(map[string]bool, len(d.Removes))
	for _, id := range d.Removes {
		removed[id] = true
	}
	kept := slices.DeleteFunc(slices.Clone(before), func(id string) bool { return removed[id] })
	if !slices.Equal(kept, after) {
		d.Order = slices.Clone(after)
		if d.Order == nil {
			d.Order = []string{}
		}
	}
}
