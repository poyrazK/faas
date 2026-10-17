package livequery

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	pgq "github.com/pganalyze/pg_query_go/v6"
)

// Keyed subqueries (ADR-1384). EXISTS / NOT EXISTS anywhere in WHERE, and IN
// / op ANY as top-level AND conditions, over one keyed table. The subquery's
// table becomes a relation whose rows the result never carries. A pair cache
// per subquery holds which FROM rows match which subquery rows (an inner
// join before WHERE); a change to the subquery table re-reads the outer rows
// it matched before or matches now. PostgreSQL still evaluates the subquery
// itself when the result rows are re-read. NOT IN recomputes: one NULL in the
// subquery changes its result for every row.

// SubqueryPlan is one keyed subquery's pair cache.
type SubqueryPlan struct {
	Rel int // relation index of the subquery's table
	// SQL selects the lineage of the FROM relations and of the subquery
	// table, joined on the subquery's condition; Restricted[i] restricts it
	// to relation i's keys ("" for relations it does not contain).
	SQL        string
	Restricted []string
}

// subquery is a planned subquery before its SQL is built.
type subquery struct {
	rel   int
	table *pgq.Node // the subquery table, renamed to its relation alias
	cond  *pgq.Node // the match condition, in outer terms
}

// compileSubqueries plans every subquery in WHERE or returns a recompute
// reason. It registers each subquery table as a relation.
func (p *Plan) compileSubqueries(ctx context.Context, conn *pgx.Conn, sel *pgq.SelectStmt) ([]subquery, error) {
	if sel.WhereClause == nil {
		return nil, nil
	}
	conjuncts := map[*pgq.SubLink]bool{}
	var flatten func(n *pgq.Node)
	flatten = func(n *pgq.Node) {
		if b := n.GetBoolExpr(); b != nil && b.Boolop == pgq.BoolExprType_AND_EXPR {
			for _, a := range b.Args {
				flatten(a)
			}
			return
		}
		if sl := n.GetSubLink(); sl != nil {
			conjuncts[sl] = true
		}
	}
	flatten(sel.WhereClause)
	var links []*pgq.SubLink
	reason := ""
	walkNodes(sel.WhereClause, func(n *pgq.Node) {
		sl := n.GetSubLink()
		if sl == nil || reason != "" {
			return
		}
		switch {
		case sl.SubLinkType == pgq.SubLinkType_EXISTS_SUBLINK:
		case sl.SubLinkType == pgq.SubLinkType_ANY_SUBLINK && conjuncts[sl]:
		default:
			reason = "subquery form"
		}
		links = append(links, sl)
	})
	if reason != "" {
		return nil, recomputeError(reason)
	}
	// Columns of the FROM relations, to qualify the outer references the
	// pair cache's join would otherwise find ambiguous.
	outerColumns := make([]map[string]bool, len(p.Relations))
	for i, rel := range p.Relations {
		cols, err := tableColumns(ctx, conn, rel.OID)
		if err != nil {
			return nil, err
		}
		outerColumns[i] = cols
	}
	var out []subquery
	for _, sl := range links {
		sq, err := p.compileSubquery(ctx, conn, sl, len(out), outerColumns)
		if err != nil {
			return nil, err
		}
		out = append(out, sq)
	}
	return out, nil
}

// qualifyOuter qualifies unqualified column references with the one FROM
// relation that has the column.
func (p *Plan) qualifyOuter(root *pgq.Node, outerColumns []map[string]bool) error {
	var err error
	walkNodes(root, func(n *pgq.Node) {
		ref := n.GetColumnRef()
		if ref == nil || len(ref.Fields) != 1 || ref.Fields[0].GetString_() == nil || err != nil {
			return
		}
		name := ref.Fields[0].GetString_().GetSval()
		owner := -1
		for i, cols := range outerColumns {
			if cols[name] {
				if owner >= 0 {
					err = recomputeError("ambiguous column " + name)
					return
				}
				owner = i
			}
		}
		if owner < 0 {
			err = recomputeError("unknown column " + name)
			return
		}
		ref.Fields = []*pgq.Node{pgq.MakeStrNode(p.Relations[owner].Alias), ref.Fields[0]}
	})
	return err
}

func (p *Plan) compileSubquery(ctx context.Context, conn *pgx.Conn, sl *pgq.SubLink, n int, outerColumns []map[string]bool) (subquery, error) {
	sub := sl.Subselect.GetSelectStmt()
	exists := sl.SubLinkType == pgq.SubLinkType_EXISTS_SUBLINK
	switch {
	case sub == nil || (sub.Op != pgq.SetOperation_SETOP_NONE && sub.Op != pgq.SetOperation_SET_OPERATION_UNDEFINED):
		return subquery{}, recomputeError("subquery set operation")
	case sub.WithClause != nil, len(sub.GroupClause) > 0, sub.HavingClause != nil, len(sub.WindowClause) > 0:
		return subquery{}, recomputeError("subquery shape")
	case len(sub.DistinctClause) > 0 && !plainDistinct(sub):
		return subquery{}, recomputeError("subquery DISTINCT ON")
	case !exists && (sub.LimitCount != nil || sub.LimitOffset != nil || len(sub.SortClause) > 0):
		return subquery{}, recomputeError("subquery LIMIT")
	case len(sub.FromClause) != 1 || sub.FromClause[0].GetRangeVar() == nil:
		return subquery{}, recomputeError("subquery FROM")
	case !exists && len(sub.TargetList) != 1:
		return subquery{}, recomputeError("subquery select list")
	case !exists && len(sl.OperName) > 1:
		return subquery{}, recomputeError("subquery operator")
	}
	nested := false
	walkNodes(sub, func(n *pgq.Node) {
		if n.GetSubLink() != nil {
			nested = true
		}
	})
	if nested {
		return subquery{}, recomputeError("nested subquery")
	}
	aggs, err := aggregateNames(ctx, conn, sub)
	if err != nil {
		return subquery{}, err
	}
	if len(aggs) > 0 {
		return subquery{}, recomputeError("subquery aggregate")
	}
	rv := sub.FromClause[0].GetRangeVar()
	rel, err := resolveRelation(ctx, conn, rv)
	if err != nil {
		return subquery{}, err
	}
	original := rv.Relname
	if rv.Alias != nil && rv.Alias.Aliasname != "" {
		original = rv.Alias.Aliasname
		if len(rv.Alias.Colnames) > 0 {
			return subquery{}, recomputeError("column aliases on a subquery table")
		}
	}
	rel.Alias = fmt.Sprintf("%sq%d", hiddenPrefix, n)
	rel.Subquery = true
	p.Relations = append(p.Relations, rel)
	idx := len(p.Relations) - 1
	p.Tables[rel.OID] = append(p.Tables[rel.OID], idx)
	p.TableNames[rel.OID] = rel.Name

	columns, err := tableColumns(ctx, conn, rel.OID)
	if err != nil {
		return subquery{}, err
	}
	// Point the subquery's references to its table at the new alias;
	// unqualified names resolve to the table when it has that column.
	bad := false
	rename := func(root *pgq.Node) {
		walkNodes(root, func(n *pgq.Node) {
			ref := n.GetColumnRef()
			if ref == nil {
				return
			}
			switch {
			case len(ref.Fields) == 2 && ref.Fields[0].GetString_().GetSval() == original:
				ref.Fields[0] = pgq.MakeStrNode(rel.Alias)
			case len(ref.Fields) == 1 && ref.Fields[0].GetString_() != nil && columns[ref.Fields[0].GetString_().GetSval()]:
				ref.Fields = []*pgq.Node{pgq.MakeStrNode(rel.Alias), ref.Fields[0]}
			case len(ref.Fields) > 2:
				bad = true
			}
		})
	}
	var cond *pgq.Node
	if sub.WhereClause != nil {
		cond = cloneNode(sub.WhereClause)
		rename(cond)
		if err := p.qualifyOuter(cond, outerColumns); err != nil {
			return subquery{}, err
		}
	}
	if !exists {
		target := cloneNode(sub.TargetList[0].GetResTarget().Val)
		rename(target)
		if err := p.qualifyOuter(target, outerColumns); err != nil {
			return subquery{}, err
		}
		test := cloneNode(sl.Testexpr)
		if err := p.qualifyOuter(test, outerColumns); err != nil {
			return subquery{}, err
		}
		op := "="
		if len(sl.OperName) == 1 {
			op = sl.OperName[0].GetString_().GetSval()
		}
		match := pgq.MakeAExprNode(pgq.A_Expr_Kind_AEXPR_OP, []*pgq.Node{pgq.MakeStrNode(op)}, test, target, -1)
		if cond == nil {
			cond = match
		} else {
			cond = pgq.MakeBoolExprNode(pgq.BoolExprType_AND_EXPR, []*pgq.Node{match, cond}, -1)
		}
	}
	if bad {
		return subquery{}, recomputeError("qualified subquery column")
	}
	if cond == nil {
		if cond, err = parseValue("true"); err != nil {
			return subquery{}, err
		}
	}
	table := cloneNode(sub.FromClause[0])
	table.GetRangeVar().Alias = &pgq.Alias{Aliasname: rel.Alias}
	return subquery{rel: idx, table: table, cond: cond}, nil
}

func tableColumns(ctx context.Context, conn *pgx.Conn, oid uint32) (map[string]bool, error) {
	rows, err := conn.Query(ctx, "SELECT attname::text FROM pg_attribute WHERE attrelid = $1 AND attnum > 0 AND NOT attisdropped", oid)
	if err != nil {
		return nil, fmt.Errorf("livequery: columns of %d: %w", oid, err)
	}
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, fmt.Errorf("livequery: scan column: %w", err)
		}
		out[name] = true
	}
	return out, rows.Err()
}

// compileSemiJoins builds each subquery's pair cache statements: the FROM
// clause (comma items cross-joined) inner-joined with the subquery table on
// its condition, projecting the lineage of both.
func (p *Plan) compileSemiJoins(sel *pgq.SelectStmt, subs []subquery) error {
	for _, sq := range subs {
		var from *pgq.Node
		for _, item := range sel.FromClause {
			if from == nil {
				from = cloneNode(item)
				continue
			}
			from = pgq.MakeJoinExprNode(pgq.JoinType_JOIN_INNER, from, cloneNode(item), nil)
		}
		joined := pgq.MakeJoinExprNode(pgq.JoinType_JOIN_INNER, from, cloneNode(sq.table), cloneNode(sq.cond))
		semi := cloneSelect(plainSelect([]*pgq.Node{joined}, nil))
		rels := append(append([]int{}, p.Outer...), sq.rel)
		semi.TargetList = p.withParams(p.lineageTargets(rels))
		sql, restricted, err := p.restricted(semi, rels)
		if err != nil {
			return err
		}
		p.Subqueries = append(p.Subqueries, SubqueryPlan{Rel: sq.rel, SQL: sql, Restricted: restricted})
	}
	return nil
}
