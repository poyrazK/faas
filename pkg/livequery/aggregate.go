package livequery

import (
	"context"
	"fmt"
	"math/big"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"
)

// Incremental aggregates (ADR-1384). Counts and exact sums are kept per group
// in the engine and updated from input row changes. PostgreSQL decides every
// comparison (min, max) and evaluates the select list and HAVING from the
// group keys and aggregate values, so output text is PostgreSQL's own.

// aggValue is the state of one aggregate in one group.
type aggValue struct {
	nonnull int64
	sum     *big.Int // unscaled, at scale `scale`
	scale   int
	scales  map[int]int64 // numeric arguments: display scale -> count
	special int64         // NaN or ±Infinity arguments

	ext   *string   // min or max
	stale bool      // the extreme may have been removed
	cands []*string // values added this round
	// override is PostgreSQL's sum when special values are present.
	override *string
}

// aggGroup is one group's state in one member.
type aggGroup struct {
	key  []*string
	n    int64
	vals []aggValue
	rows map[string]struct{} // input row IDs in the group
}

func newAggGroup(key []*string, specs int) *aggGroup {
	return &aggGroup{key: key, vals: make([]aggValue, specs), rows: map[string]struct{}{}}
}

// parseDecimal reads integer or numeric text. Special values (NaN,
// Infinity) are reported, not parsed.
func parseDecimal(s string) (unscaled *big.Int, scale int, special bool, err error) {
	switch s {
	case "NaN", "Infinity", "-Infinity":
		return nil, 0, true, nil
	}
	digits := s
	if i := strings.IndexByte(s, '.'); i >= 0 {
		scale = len(s) - i - 1
		digits = s[:i] + s[i+1:]
	}
	unscaled, ok := new(big.Int).SetString(digits, 10)
	if !ok {
		return nil, 0, false, fmt.Errorf("livequery: not a decimal: %q", s)
	}
	return unscaled, scale, false, nil
}

var bigTen = big.NewInt(10)

func pow10(n int) *big.Int { return new(big.Int).Exp(bigTen, big.NewInt(int64(n)), nil) }

// formatDecimal renders unscaled/10^scale with exactly display fractional
// digits (display <= scale, and the value has no digits beyond display), as
// PostgreSQL prints numeric.
func formatDecimal(unscaled *big.Int, scale, display int) string {
	v := new(big.Int).Quo(unscaled, pow10(scale-display))
	neg := v.Sign() < 0
	digits := new(big.Int).Abs(v).String()
	if display > 0 {
		if len(digits) <= display {
			digits = strings.Repeat("0", display-len(digits)+1) + digits
		}
		digits = digits[:len(digits)-display] + "." + digits[len(digits)-display:]
	}
	if neg {
		return "-" + digits
	}
	return digits
}

func (v *aggValue) addDecimal(text string, sign int) error {
	unscaled, scale, special, err := parseDecimal(text)
	if err != nil {
		return err
	}
	if special {
		v.special += int64(sign)
		return nil
	}
	if v.sum == nil {
		v.sum = new(big.Int)
	}
	if scale > v.scale {
		v.sum.Mul(v.sum, pow10(scale-v.scale))
		v.scale = scale
	}
	unscaled.Mul(unscaled, pow10(v.scale-scale))
	if sign > 0 {
		v.sum.Add(v.sum, unscaled)
	} else {
		v.sum.Sub(v.sum, unscaled)
	}
	if v.scales == nil {
		v.scales = map[int]int64{}
	}
	v.scales[scale] += int64(sign)
	if v.scales[scale] == 0 {
		delete(v.scales, scale)
	}
	return nil
}

// sumText is the aggregate's sum as PostgreSQL would print it, or nil.
func (v *aggValue) sumText() *string {
	if v.nonnull == 0 {
		return nil
	}
	if v.special > 0 {
		return v.override
	}
	display := 0
	for s := range v.scales {
		display = max(display, s)
	}
	sum := v.sum
	if sum == nil {
		sum = new(big.Int)
	}
	out := formatDecimal(sum, v.scale, display)
	return &out
}

// update applies one input row to a group: sign +1 adds it, -1 removes it.
func (g *planGroup) update(grp *aggGroup, id string, r *keyedRow, sign int) error {
	grp.n += int64(sign)
	if sign > 0 {
		grp.rows[id] = struct{}{}
	} else {
		delete(grp.rows, id)
	}
	for j, spec := range g.plan.Agg.Specs {
		v := &grp.vals[j]
		if spec.Kind == AggCountStar {
			continue
		}
		arg := r.aggs[j]
		if arg == nil {
			continue
		}
		v.nonnull += int64(sign)
		switch spec.Kind {
		case AggSum, AggAvg:
			if err := v.addDecimal(*arg, sign); err != nil {
				return err
			}
		case AggMin, AggMax:
			if sign > 0 {
				v.cands = append(v.cands, arg)
			} else if v.ext == nil || *v.ext == *arg || !spec.CanonicalText {
				v.stale = true
			}
		}
	}
	return nil
}

// rebuildAgg builds group states from the members' freshly read input.
func (g *planGroup) rebuildAgg(ctx context.Context, tx pgx.Tx, ms []*member) error {
	affected := map[*member]map[string]bool{}
	for _, m := range ms {
		m.agg = map[string]*aggGroup{}
		m.groups = map[string]*keyedRow{}
		affected[m] = map[string]bool{}
		if g.plan.NGroups == 0 {
			m.agg[""] = newAggGroup(nil, len(g.plan.Agg.Specs))
			affected[m][""] = true
		}
		for id, r := range m.input.rows {
			grp := m.agg[r.group]
			if grp == nil {
				grp = newAggGroup(r.values, len(g.plan.Agg.Specs))
				m.agg[r.group] = grp
			}
			if err := g.update(grp, id, r, 1); err != nil {
				return err
			}
			affected[m][r.group] = true
		}
		for _, grp := range m.agg {
			for j := range grp.vals {
				grp.vals[j].stale = true
			}
		}
	}
	return g.settle(ctx, tx, affected)
}

// maintainAgg applies a round's input changes to the members' group states.
func (g *planGroup) maintainAgg(ctx context.Context, tx pgx.Tx, ms []*member, keys []map[string]struct{}) error {
	keys, err := g.expand(ctx, tx, ms, keys)
	if err != nil {
		return err
	}
	fresh, err := g.readRestricted(ctx, tx, g.plan.RowRestricted, keys, ms, true)
	if err != nil {
		return err
	}
	affected := map[*member]map[string]bool{}
	for _, m := range ms {
		affected[m] = map[string]bool{}
		for id := range m.input.matching(keys) {
			r := m.input.rows[id]
			if err := g.update(m.agg[r.group], id, r, -1); err != nil {
				return err
			}
			affected[m][r.group] = true
		}
		m.input.replace(keys, fresh[m])
		for id, r := range fresh[m] {
			grp := m.agg[r.group]
			if grp == nil {
				grp = newAggGroup(r.values, len(g.plan.Agg.Specs))
				m.agg[r.group] = grp
			}
			if err := g.update(grp, id, r, 1); err != nil {
				return err
			}
			affected[m][r.group] = true
		}
	}
	return g.settle(ctx, tx, affected)
}

// groupRef names one (member, group) pair in a batched statement.
type groupRef struct {
	m   *member
	enc string
}

// settle resolves min/max and special sums for the affected groups, drops
// empty groups, and re-evaluates the affected output rows.
func (g *planGroup) settle(ctx context.Context, tx pgx.Tx, affected map[*member]map[string]bool) error {
	var refs []groupRef
	for _, m := range g.members {
		for enc := range affected[m] {
			grp := m.agg[enc]
			if grp == nil {
				continue
			}
			if grp.n == 0 && g.plan.NGroups > 0 {
				delete(m.agg, enc)
				delete(m.groups, enc)
				continue
			}
			refs = append(refs, groupRef{m: m, enc: enc})
		}
	}
	if len(refs) == 0 {
		return nil
	}
	for j, spec := range g.plan.Agg.Specs {
		switch spec.Kind {
		case AggMin, AggMax:
			if err := g.resolveExtremes(ctx, tx, refs, j); err != nil {
				return err
			}
		case AggSum, AggAvg:
			if err := g.resolveSpecialSums(ctx, tx, refs, j); err != nil {
				return err
			}
		}
	}
	return g.finalize(ctx, tx, refs)
}

// aggregateValues runs fn over (pair, value) lists in PostgreSQL and returns
// the result per pair.
func (g *planGroup) aggregateValues(ctx context.Context, tx pgx.Tx, fn, typ string, pairs []int32, values []*string) (map[int32]*string, error) {
	g.stats.Statements++
	_, rows, err := queryText(ctx, tx,
		fmt.Sprintf("SELECT u.pair, %s(u.v::%s)::text FROM unnest($1::int[], $2::text[]) AS u(pair, v) GROUP BY u.pair", fn, typ),
		[]any{pairs, values})
	if err != nil {
		return nil, fmt.Errorf("livequery: %s over group values: %w", fn, err)
	}
	out := map[int32]*string{}
	for _, row := range rows {
		n, err := strconv.ParseInt(*row[0], 10, 32)
		if err != nil {
			return nil, fmt.Errorf("livequery: %s returned pair %q: %w", fn, *row[0], err)
		}
		out[int32(n)] = row[1]
	}
	return out, nil
}

// resolveExtremes asks PostgreSQL for each affected group's min or max:
// from the current extreme and the added values, or, when the extreme may
// have been removed, from all of the group's cached values.
func (g *planGroup) resolveExtremes(ctx context.Context, tx pgx.Tx, refs []groupRef, j int) error {
	spec := g.plan.Agg.Specs[j]
	var pairs []int32
	var values []*string
	var touched []*aggValue
	for i, ref := range refs {
		grp := ref.m.agg[ref.enc]
		v := &grp.vals[j]
		if !v.stale && len(v.cands) == 0 {
			continue
		}
		touched = append(touched, v)
		if v.stale {
			for id := range grp.rows {
				if arg := ref.m.input.rows[id].aggs[j]; arg != nil {
					pairs = append(pairs, int32(i))
					values = append(values, arg)
				}
			}
		} else {
			if v.ext != nil {
				pairs = append(pairs, int32(i))
				values = append(values, v.ext)
			}
			for _, c := range v.cands {
				pairs = append(pairs, int32(i))
				values = append(values, c)
			}
		}
		v.ext = nil
	}
	if len(touched) == 0 {
		return nil
	}
	fn := "min"
	if spec.Kind == AggMax {
		fn = "max"
	}
	if len(pairs) > 0 {
		got, err := g.aggregateValues(ctx, tx, fn, spec.ArgType, pairs, values)
		if err != nil {
			return err
		}
		for i, ref := range refs {
			if ext, ok := got[int32(i)]; ok {
				ref.m.agg[ref.enc].vals[j].ext = ext
			}
		}
	}
	for _, v := range touched {
		v.stale, v.cands = false, nil
	}
	return nil
}

// resolveSpecialSums lets PostgreSQL sum groups holding NaN or Infinity.
func (g *planGroup) resolveSpecialSums(ctx context.Context, tx pgx.Tx, refs []groupRef, j int) error {
	var pairs []int32
	var values []*string
	for i, ref := range refs {
		grp := ref.m.agg[ref.enc]
		if grp.vals[j].special == 0 {
			continue
		}
		for id := range grp.rows {
			if arg := ref.m.input.rows[id].aggs[j]; arg != nil {
				pairs = append(pairs, int32(i))
				values = append(values, arg)
			}
		}
	}
	if len(pairs) == 0 {
		return nil
	}
	got, err := g.aggregateValues(ctx, tx, "sum", "numeric", pairs, values)
	if err != nil {
		return err
	}
	for i, ref := range refs {
		if s, ok := got[int32(i)]; ok {
			ref.m.agg[ref.enc].vals[j].override = s
		}
	}
	return nil
}

// finalize evaluates the select list and HAVING for the given groups and
// stores the output rows; a group HAVING rejects has no row.
func (g *planGroup) finalize(ctx context.Context, tx pgx.Tx, refs []groupRef) error {
	p := g.plan
	idx := make([]int32, len(refs))
	gid := make([]int32, len(refs))
	cols := make([][]*string, p.NParams+p.NGroups)
	var values [][]*string
	for _, s := range p.Agg.Specs {
		for range s.valueColumns() {
			values = append(values, nil)
		}
	}
	for i, ref := range refs {
		grp := ref.m.agg[ref.enc]
		idx[i], gid[i] = ref.m.idx, int32(i)
		for k := 0; k < p.NParams; k++ {
			cols[k] = append(cols[k], ref.m.argText[k])
		}
		for k := 0; k < p.NGroups; k++ {
			cols[p.NParams+k] = append(cols[p.NParams+k], grp.key[k])
		}
		c := 0
		for j, s := range p.Agg.Specs {
			v := &grp.vals[j]
			switch s.Kind {
			case AggCountStar:
				values[c] = append(values[c], strPtr(strconv.FormatInt(grp.n, 10)))
			case AggCount:
				values[c] = append(values[c], strPtr(strconv.FormatInt(v.nonnull, 10)))
			case AggSum:
				values[c] = append(values[c], v.sumText())
			case AggAvg:
				values[c] = append(values[c], v.sumText())
				values[c+1] = append(values[c+1], strPtr(strconv.FormatInt(v.nonnull, 10)))
			case AggMin, AggMax:
				values[c] = append(values[c], v.ext)
			}
			c += s.valueColumns()
		}
	}
	args := []any{idx, gid}
	for _, col := range cols {
		args = append(args, col)
	}
	for _, v := range values {
		args = append(args, v)
	}
	g.stats.Statements++
	names, rows, err := queryText(ctx, tx, p.Agg.FinalizeSQL, args)
	if err != nil {
		return fmt.Errorf("livequery: finalize groups: %w", err)
	}
	for _, ref := range refs {
		delete(ref.m.groups, ref.enc)
	}
	for _, row := range rows {
		n, err := strconv.Atoi(*row[1])
		if err != nil || n < 0 || n >= len(refs) {
			return fmt.Errorf("livequery: finalize returned group %v", row[1])
		}
		ref := refs[n]
		visible := row[2:]
		ref.m.columns = names[2:]
		ref.m.groups[ref.enc] = &keyedRow{values: visible, enc: encodeValues(visible)}
	}
	return nil
}

func strPtr(s string) *string { return &s }
