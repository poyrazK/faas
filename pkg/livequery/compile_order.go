package livequery

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"
	pgq "github.com/pganalyze/pg_query_go/v6"
)

// Ordered windows (ADR-20261016151947): ORDER BY ... LIMIT [OFFSET] over a
// select-project-join query. The engine keeps each member's window, the first
// LIMIT + OFFSET rows, ordered by the query's sort items and then by every
// relation's primary key, which makes the order total; with ties the query
// may return any of the tied rows, and the tie-breakers pick one answer. A
// change re-reads only the rows derived from changed keys (at most a window's
// worth per relation); PostgreSQL ranks them against the rest of the window,
// and the window is refilled from the query only when it lost rows that no
// re-read row replaces.

// OrderPlan maintains an ordered window (ModeLineage).
type OrderPlan struct {
	// Ordered reports whether the query has ORDER BY. A LIMIT without it
	// still keeps a window, ordered by the tie-breakers alone, but clients
	// receive an unordered result.
	Ordered bool
	// Items are the sort items: the query's own (hidden columns __lq_o<k>),
	// then each relation's key columns.
	Items []OrderItem
	NUser int
	Limit Bound
	// Offset rows lead the window but are not part of the result.
	Offset Bound
	// RankSQL sorts candidate rows by Items: $1 member indexes, $2 candidate
	// indexes, one text array per item, then a marker array that sorts a
	// boundary after its equals. It returns the candidate indexes in order,
	// grouped by member.
	RankSQL string
	// OrderedSQL is the query with the tie-breakers, and WindowSQL the
	// window query unbatched, for the oracle.
	OrderedSQL string
	WindowSQL  string
}

// OrderItem is one sort item.
type OrderItem struct {
	Type       string // format_type of the item
	Collation  string // quoted collation name, or "" when not collatable
	Desc       bool
	NullsFirst bool
}

// Bound is a LIMIT or OFFSET: absent, a constant or a query parameter.
type Bound struct {
	Set   bool
	Value int64
	Param int // 1-based parameter number; 0 for a constant
}

// eval returns the bound for a member's arguments; ok is false when it does
// not limit anything (absent, ALL or NULL).
func (b Bound) eval(args []*string) (n int64, ok bool, err error) {
	if !b.Set {
		return 0, false, nil
	}
	if b.Param == 0 {
		return b.Value, true, nil
	}
	v := args[b.Param-1]
	if v == nil {
		return 0, false, nil
	}
	n, err = strconv.ParseInt(*v, 10, 64)
	if err != nil {
		return 0, false, fmt.Errorf("livequery: LIMIT or OFFSET %q: %w", *v, err)
	}
	if n < 0 {
		return 0, false, fmt.Errorf("livequery: LIMIT or OFFSET %d is negative", n)
	}
	return n, true, nil
}

func orderColumn(k int) string { return fmt.Sprintf("%so%d", hiddenPrefix, k) }

// hasOrder reports whether the query sorts or limits its result.
func hasOrder(sel *pgq.SelectStmt) bool {
	return len(sel.SortClause) > 0 || sel.LimitCount != nil || sel.LimitOffset != nil
}

// compileOrder turns rowSel, the query with lineage columns, into the window
// query: hidden sort columns, the tie-breakers, and LIMIT + OFFSET rows from
// the start.
func (p *Plan) compileOrder(ctx context.Context, conn *pgx.Conn, sel, rowSel *pgq.SelectStmt) error {
	if sel.LimitOption == pgq.LimitOption_LIMIT_OPTION_WITH_TIES {
		return recomputeError("FETCH WITH TIES")
	}
	o := &OrderPlan{Ordered: len(sel.SortClause) > 0, NUser: len(sel.SortClause)}
	var err error
	if o.Limit, err = bound(sel.LimitCount); err != nil {
		return err
	}
	if o.Offset, err = bound(sel.LimitOffset); err != nil {
		return err
	}
	var sorts []*pgq.Node
	var exprs []*pgq.Node
	for k, n := range sel.SortClause {
		sb := n.GetSortBy()
		if sb.SortbyDir == pgq.SortByDir_SORTBY_USING {
			return recomputeError("ORDER BY USING")
		}
		expr, err := sortExpr(sel, sb.Node)
		if err != nil {
			return err
		}
		exprs = append(exprs, expr)
		desc := sb.SortbyDir == pgq.SortByDir_SORTBY_DESC
		nullsFirst := sb.SortbyNulls == pgq.SortByNulls_SORTBY_NULLS_FIRST ||
			(sb.SortbyNulls != pgq.SortByNulls_SORTBY_NULLS_LAST && desc)
		o.Items = append(o.Items, OrderItem{Desc: desc, NullsFirst: nullsFirst})
		sorts = append(sorts, sortBy(expr, desc, nullsFirst))
		rowSel.TargetList = append(rowSel.TargetList, resTarget(orderColumn(k), cloneNode(expr)))
	}
	for _, i := range p.Outer {
		rel := p.Relations[i]
		for _, key := range rel.Keys {
			expr := columnRef(rel.Alias, key)
			exprs = append(exprs, expr)
			o.Items = append(o.Items, OrderItem{})
			sorts = append(sorts, sortBy(expr, false, false))
		}
	}
	if err := p.orderTypes(ctx, conn, sel, rowSel, exprs, o); err != nil {
		return err
	}
	ordered := cloneSelect(sel)
	ordered.SortClause = cloneNodes(sorts)
	if o.OrderedSQL, err = deparseSelect(ordered); err != nil {
		return err
	}
	rowSel.SortClause = cloneNodes(sorts)
	rowSel.LimitOffset = nil
	rowSel.LimitCount = windowSize(sel)
	rowSel.LimitOption = pgq.LimitOption_LIMIT_OPTION_DEFAULT
	if rowSel.LimitCount != nil {
		rowSel.LimitOption = pgq.LimitOption_LIMIT_OPTION_COUNT
	}
	if o.WindowSQL, err = deparseSelect(rowSel); err != nil {
		return err
	}
	o.RankSQL = rankSQL(o.Items)
	p.Order = o
	return nil
}

// bound reads a LIMIT or OFFSET clause: a constant, a parameter, ALL or NULL.
func bound(n *pgq.Node) (Bound, error) {
	if n == nil {
		return Bound{}, nil
	}
	if tc := n.GetTypeCast(); tc != nil {
		n = tc.Arg
	}
	switch {
	case n.GetParamRef() != nil:
		return Bound{Set: true, Param: int(n.GetParamRef().Number)}, nil
	case n.GetAConst() != nil && n.GetAConst().Isnull:
		return Bound{}, nil
	case n.GetAConst() != nil && n.GetAConst().GetIval() != nil:
		v := int64(n.GetAConst().GetIval().Ival)
		if v < 0 {
			return Bound{}, unsupported("LIMIT or OFFSET %d is negative", v)
		}
		return Bound{Set: true, Value: v}, nil
	}
	return Bound{}, recomputeError("LIMIT or OFFSET expression")
}

// windowSize is LIMIT + OFFSET as an expression, or nil when unlimited.
func windowSize(sel *pgq.SelectStmt) *pgq.Node {
	limit := sel.LimitCount
	if limit == nil {
		return nil
	}
	if c := limit.GetAConst(); c != nil && c.Isnull {
		return nil
	}
	if sel.LimitOffset == nil {
		return cloneNode(limit)
	}
	coalesce := &pgq.Node{Node: &pgq.Node_CoalesceExpr{CoalesceExpr: &pgq.CoalesceExpr{
		Args: []*pgq.Node{cloneNode(sel.LimitOffset), pgq.MakeAConstIntNode(0, -1)},
	}}}
	return pgq.MakeAExprNode(pgq.A_Expr_Kind_AEXPR_OP, []*pgq.Node{pgq.MakeStrNode("+")}, cloneNode(limit), coalesce, -1)
}

// sortExpr resolves an ORDER BY item as PostgreSQL does: an integer is an
// output column position, and a bare name matching an output column name
// refers to that column. Anything else is an expression over the inputs.
func sortExpr(sel *pgq.SelectStmt, n *pgq.Node) (*pgq.Node, error) {
	target := func(i int) (*pgq.Node, error) {
		rt := sel.TargetList[i].GetResTarget()
		if rt.Val.GetColumnRef() != nil {
			for _, f := range rt.Val.GetColumnRef().Fields {
				if f.GetAStar() != nil {
					return nil, recomputeError("ORDER BY a * output column")
				}
			}
		}
		return cloneNode(rt.Val), nil
	}
	if c := n.GetAConst(); c != nil && c.GetIval() != nil {
		pos := int(c.GetIval().Ival)
		if pos < 1 || pos > len(sel.TargetList) {
			return nil, unsupported("ORDER BY position %d is out of range", pos)
		}
		return target(pos - 1)
	}
	if ref := n.GetColumnRef(); ref != nil && len(ref.Fields) == 1 && ref.Fields[0].GetString_() != nil {
		name := ref.Fields[0].GetString_().GetSval()
		for i, t := range sel.TargetList {
			rt := t.GetResTarget()
			if rt.Name == name {
				return target(i)
			}
		}
	}
	return cloneNode(n), nil
}

func sortBy(expr *pgq.Node, desc, nullsFirst bool) *pgq.Node {
	dir, nulls := pgq.SortByDir_SORTBY_ASC, pgq.SortByNulls_SORTBY_NULLS_LAST
	if desc {
		dir = pgq.SortByDir_SORTBY_DESC
	}
	if nullsFirst {
		nulls = pgq.SortByNulls_SORTBY_NULLS_FIRST
	}
	return pgq.MakeSortByNode(cloneNode(expr), dir, nulls, -1)
}

func cloneNodes(ns []*pgq.Node) []*pgq.Node {
	out := make([]*pgq.Node, len(ns))
	for i, n := range ns {
		out[i] = cloneNode(n)
	}
	return out
}

// orderTypes fills each item's type and collation. Arrays cannot pass
// through the rank statement's unnest, and records have no text input, so
// both recompute.
func (p *Plan) orderTypes(ctx context.Context, conn *pgx.Conn, sel, rowSel *pgq.SelectStmt, exprs []*pgq.Node, o *OrderPlan) error {
	probe := cloneSelect(rowSel)
	probe.SortClause, probe.LimitCount, probe.LimitOffset = nil, nil, nil
	probe.LimitOption = pgq.LimitOption_LIMIT_OPTION_DEFAULT
	sql, err := deparseSelect(probe)
	if err != nil {
		return err
	}
	desc, err := conn.Prepare(ctx, "", sql)
	if err != nil {
		return fmt.Errorf("livequery: prepare window query: %w", err)
	}
	byName := map[string]uint32{}
	for _, f := range desc.Fields {
		byName[f.Name] = f.DataTypeOID
	}
	var oids []uint32
	for k := range o.NUser {
		oids = append(oids, byName[orderColumn(k)])
	}
	for _, rel := range p.Outer {
		for k := range p.Relations[rel].Keys {
			oids = append(oids, byName[lineageColumn(rel, k)])
		}
	}
	for k := range o.Items {
		var typ string
		var collatable, unsortable bool
		if err := conn.QueryRow(ctx, `
			SELECT format_type(oid, NULL), typcollation <> 0, typcategory = 'A' OR typtype = 'p'
			FROM pg_type WHERE oid = $1`, oids[k]).Scan(&typ, &collatable, &unsortable); err != nil {
			return fmt.Errorf("livequery: sort item type: %w", err)
		}
		if unsortable {
			return recomputeError("ORDER BY on an array or record")
		}
		o.Items[k].Type = typ
		if collatable {
			if o.Items[k].Collation, err = p.collationOf(ctx, conn, sel, exprs[k]); err != nil {
				return err
			}
		}
	}
	return nil
}

// collationOf asks PostgreSQL for the collation an expression sorts with in
// the query: the collation of a scalar subquery over the same FROM clause,
// with parameters replaced by typed NULLs.
func (p *Plan) collationOf(ctx context.Context, conn *pgx.Conn, sel *pgq.SelectStmt, expr *pgq.Node) (string, error) {
	expr = cloneNode(expr)
	var err error
	walkNodes(expr, func(n *pgq.Node) {
		ref := n.GetParamRef()
		if ref == nil || err != nil {
			return
		}
		var null *pgq.Node
		if null, err = parseValue("NULL::" + p.ParamTypes[ref.Number-1]); err == nil {
			n.Node = null.Node
		}
	})
	if err != nil {
		return "", err
	}
	never, err := parseValue("false")
	if err != nil {
		return "", err
	}
	probe := plainSelect(cloneNodes(sel.FromClause), never)
	probe.TargetList = []*pgq.Node{resTarget("c", expr)}
	sql, err := deparseSelect(probe)
	if err != nil {
		return "", err
	}
	var name *string
	if err := conn.QueryRow(ctx, "SELECT pg_collation_for(("+sql+"))").Scan(&name); err != nil {
		return "", fmt.Errorf("livequery: sort item collation: %w", err)
	}
	if name == nil {
		return "", nil
	}
	return *name, nil
}

// rankSQL builds the statement that orders candidates by the items.
func rankSQL(items []OrderItem) string {
	args := []string{"$1::int[]", "$2::int[]"}
	cols := []string{"m", "c"}
	order := []string{"r.m"}
	for k, it := range items {
		args = append(args, unnestArg(k+3, it.Type))
		cols = append(cols, fmt.Sprintf("v%d", k))
		item := fmt.Sprintf("r.v%d", k)
		if isArrayType(it.Type) {
			item = fmt.Sprintf("(r.v%d::%s)", k, it.Type)
		}
		if it.Collation != "" {
			item += " COLLATE " + it.Collation
		}
		if it.Desc {
			item += " DESC"
		}
		if it.NullsFirst {
			item += " NULLS FIRST"
		} else {
			item += " NULLS LAST"
		}
		order = append(order, item)
	}
	args = append(args, fmt.Sprintf("$%d::bool[]", len(items)+3))
	cols = append(cols, "marker")
	order = append(order, "r.marker")
	return fmt.Sprintf("SELECT r.c FROM unnest(%s) AS r(%s) ORDER BY %s",
		strings.Join(args, ", "), strings.Join(cols, ", "), strings.Join(order, ", "))
}
