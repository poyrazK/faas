package livequery

import (
	"context"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	pgq "github.com/pganalyze/pg_query_go/v6"
)

// AggKind is an aggregate the engine maintains incrementally.
type AggKind int

const (
	AggCountStar AggKind = iota + 1
	AggCount
	AggSum
	AggAvg
	AggMin
	AggMax
)

// AggSpec is one distinct aggregate call of an incremental group query.
type AggSpec struct {
	Kind       AggKind
	ArgType    string // argument type (format_type); empty for count(*)
	ResultType string
	// Numeric marks a numeric argument: sums track display scales.
	Numeric bool
	// CanonicalText marks argument types whose equal values always have equal
	// text, so removing a value other than the current min/max cannot
	// change it.
	CanonicalText bool
}

// AggPlan maintains a GROUP BY, DISTINCT or whole-table aggregate query
// incrementally: counts and exact sums in the engine, min and max compared by
// PostgreSQL, and the select list and HAVING evaluated by PostgreSQL from the
// group keys and aggregate values.
type AggPlan struct {
	Specs []AggSpec
	// FinalizeSQL takes $1 member indexes, $2 group ordinals, then text
	// arrays: one per query parameter, one per group key, and the aggregate
	// value columns (two for avg: sum and count; one otherwise).
	FinalizeSQL string

	args []*pgq.Node // each spec's argument; nil for count(*)
}

// valueColumns is how many finalize columns each spec takes.
func (s AggSpec) valueColumns() int {
	if s.Kind == AggAvg {
		return 2
	}
	return 1
}

var canonicalTextTypes = map[string]bool{
	"smallint": true, "integer": true, "bigint": true, "boolean": true, "text": true, "date": true,
	"timestamp without time zone": true, "timestamp with time zone": true, "uuid": true,
}

// plainDistinct reports SELECT DISTINCT (not DISTINCT ON).
func plainDistinct(sel *pgq.SelectStmt) bool {
	return len(sel.DistinctClause) == 1 && sel.DistinctClause[0].GetNode() == nil
}

// deparseExpr renders one expression as SQL text, or "" when the node is not
// an expression.
func deparseExpr(n *pgq.Node) string {
	sel := plainSelect(nil, nil)
	sel.TargetList = []*pgq.Node{resTarget("", cloneNode(n))}
	out, err := deparseSelect(sel)
	if err != nil {
		return ""
	}
	return strings.TrimPrefix(out, "SELECT ")
}

// compileAggregates decides whether a grouped query can be maintained
// incrementally and, if so, plans it. It returns a nil plan and a reason
// when the query must use group re-aggregation instead.
func (p *Plan) compileAggregates(ctx context.Context, conn *pgx.Conn, sel *pgq.SelectStmt, groups []*pgq.Node, names []string) (*AggPlan, string, error) {
	aggNames, err := aggregateNames(ctx, conn, sel)
	if err != nil {
		return nil, "", err
	}
	type call struct {
		node *pgq.Node
		kind AggKind
	}
	var calls []call
	index := map[string]int{}
	reason := ""
	collect := func(root *pgq.Node) {
		walkNodes(root, func(n *pgq.Node) {
			fc := n.GetFuncCall()
			if fc == nil || reason != "" {
				return
			}
			schema, name := funcName(fc)
			if !aggNames[name] {
				return
			}
			kind := map[string]AggKind{"count": AggCount, "sum": AggSum, "avg": AggAvg, "min": AggMin, "max": AggMax}[name]
			switch {
			case schema != "" && schema != "pg_catalog", kind == 0:
				reason = "aggregate " + name
			case fc.AggDistinct || fc.AggFilter != nil || len(fc.AggOrder) > 0 || fc.Over != nil || fc.AggWithinGroup:
				reason = "aggregate options on " + name
			case fc.AggStar && kind == AggCount:
				kind = AggCountStar
			case fc.AggStar || len(fc.Args) != 1:
				reason = "aggregate arguments of " + name
			}
			if reason != "" {
				return
			}
			text := deparseExpr(n)
			if _, ok := index[text]; !ok {
				index[text] = len(calls)
				calls = append(calls, call{node: cloneNode(n), kind: kind})
			}
		})
	}
	for _, t := range sel.TargetList {
		collect(t)
	}
	if sel.HavingClause != nil {
		collect(sel.HavingClause)
	}
	if reason != "" {
		return nil, reason, nil
	}
	plan := &AggPlan{Specs: make([]AggSpec, len(calls)), args: make([]*pgq.Node, len(calls))}
	for j, c := range calls {
		plan.Specs[j].Kind = c.kind
		if fc := c.node.GetFuncCall(); c.kind != AggCountStar {
			plan.args[j] = fc.Args[0]
			if c.kind != AggCount {
				continue
			}
			// count(t.*) counts rows where t is not NULL-extended, which is
			// exactly where t's primary key is not NULL.
			ref := fc.Args[0].GetColumnRef()
			if ref == nil || len(ref.Fields) != 2 || ref.Fields[1].GetAStar() == nil {
				continue
			}
			alias := ref.Fields[0].GetString_().GetSval()
			var found *Relation
			for _, rel := range p.Relations {
				if rel.Alias == alias {
					found = rel
				}
			}
			if found == nil {
				return nil, "count of " + alias + ".*", nil
			}
			plan.args[j] = columnRef(alias, found.Keys[0])
		}
	}
	if err := p.aggregateTypes(ctx, conn, sel, groups, plan, func(j int) *pgq.Node { return calls[j].node }, plan.args); err != nil {
		return nil, "", err
	}
	for j, s := range plan.Specs {
		if (s.Kind == AggSum || s.Kind == AggAvg) && !exactType(s.ArgType) {
			return nil, fmt.Sprintf("sum or avg of %s", s.ArgType), nil
		}
		plan.Specs[j].Numeric = strings.HasPrefix(s.ArgType, "numeric")
		plan.Specs[j].CanonicalText = canonicalTextTypes[s.ArgType]
	}
	sql, reason, err := p.finalizeSQL(sel, groups, names, plan, index)
	if err != nil || reason != "" {
		return nil, reason, err
	}
	plan.FinalizeSQL = sql
	return plan, "", nil
}

func exactType(t string) bool {
	return t == "smallint" || t == "integer" || t == "bigint" || strings.HasPrefix(t, "numeric")
}

func funcName(fc *pgq.FuncCall) (schema, name string) {
	var parts []string
	for _, f := range fc.Funcname {
		parts = append(parts, f.GetString_().GetSval())
	}
	name = parts[len(parts)-1]
	if len(parts) > 1 {
		schema = parts[len(parts)-2]
	}
	return schema, name
}

// aggregateNames returns the names of aggregate functions called anywhere in
// the query.
func aggregateNames(ctx context.Context, conn *pgx.Conn, sel *pgq.SelectStmt) (map[string]bool, error) {
	var names []string
	walkNodes(sel, func(n *pgq.Node) {
		if fc := n.GetFuncCall(); fc != nil {
			_, name := funcName(fc)
			names = append(names, name)
		}
	})
	out := map[string]bool{}
	if len(names) == 0 {
		return out, nil
	}
	rows, err := conn.Query(ctx, "SELECT DISTINCT proname::text FROM pg_proc WHERE prokind = 'a' AND proname = ANY($1)", names)
	if err != nil {
		return nil, fmt.Errorf("livequery: look up aggregates: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, fmt.Errorf("livequery: scan aggregate name: %w", err)
		}
		out[name] = true
	}
	return out, rows.Err()
}

// aggregateTypes fills argument and result types by preparing the aggregate
// calls and their arguments over the query's FROM and WHERE.
func (p *Plan) aggregateTypes(ctx context.Context, conn *pgx.Conn, sel *pgq.SelectStmt, groups []*pgq.Node, plan *AggPlan, node func(int) *pgq.Node, argNodes []*pgq.Node) error {
	if len(plan.Specs) == 0 {
		return nil
	}
	results := cloneSelect(plainSelect(sel.FromClause, sel.WhereClause))
	results.GroupClause = cloneSelect(&pgq.SelectStmt{GroupClause: groups}).GroupClause
	args := cloneSelect(plainSelect(sel.FromClause, sel.WhereClause))
	for j := range plan.Specs {
		results.TargetList = append(results.TargetList, resTarget(fmt.Sprintf("r%d", j), cloneNode(node(j))))
		if argNodes[j] != nil {
			args.TargetList = append(args.TargetList, resTarget(fmt.Sprintf("a%d", j), cloneNode(argNodes[j])))
		} else {
			args.TargetList = append(args.TargetList, resTarget(fmt.Sprintf("a%d", j), pgq.MakeAConstIntNode(1, -1)))
		}
	}
	typeOf := func(sql string) ([]string, error) {
		desc, err := conn.Prepare(ctx, "", sql)
		if err != nil {
			return nil, err
		}
		out := make([]string, len(desc.Fields))
		for i, f := range desc.Fields {
			if err := conn.QueryRow(ctx, "SELECT format_type($1, $2)", f.DataTypeOID, f.TypeModifier).Scan(&out[i]); err != nil {
				return nil, err
			}
		}
		return out, nil
	}
	for _, step := range []struct {
		sel *pgq.SelectStmt
		set func(j int, t string)
	}{
		{results, func(j int, t string) { plan.Specs[j].ResultType = t }},
		{args, func(j int, t string) {
			if plan.Specs[j].Kind != AggCountStar {
				plan.Specs[j].ArgType = t
			}
		}},
	} {
		sql, err := deparseSelect(step.sel)
		if err != nil {
			return err
		}
		types, err := typeOf(sql)
		if err != nil {
			return fmt.Errorf("livequery: aggregate types: %w", err)
		}
		for j, t := range types {
			step.set(j, t)
		}
	}
	return nil
}

// finalizeSQL builds the statement that evaluates the select list and HAVING
// from group keys and aggregate values. Every aggregate call becomes its value
// column, every group expression its key column and every parameter its
// member column; anything else reading a table makes the query ineligible.
func (p *Plan) finalizeSQL(sel *pgq.SelectStmt, groups []*pgq.Node, names []string, plan *AggPlan, aggIndex map[string]int) (string, string, error) {
	groupIndex := map[string]int{}
	for k, g := range groups {
		groupIndex[deparseExpr(g)] = k
	}
	valueExpr := map[int]*pgq.Node{}
	col := 0
	for j, s := range plan.Specs {
		var expr string
		if s.Kind == AggAvg {
			expr = fmt.Sprintf("(%[1]sv.v%[2]d / NULLIF(%[1]sv.v%[3]d, 0)::numeric)", hiddenPrefix, col, col+1)
		} else {
			expr = fmt.Sprintf("%sv.v%d", hiddenPrefix, col)
		}
		node, err := parseValue(expr)
		if err != nil {
			return "", "", err
		}
		valueExpr[j] = node
		col += s.valueColumns()
	}
	nparams := int32(p.NParams)
	replace := func(root *pgq.Node) string {
		reason := ""
		walkNodes(root, func(n *pgq.Node) {
			if ref := n.GetParamRef(); ref != nil && ref.Number <= nparams {
				n.Node = columnRef(hiddenPrefix+"v", fmt.Sprintf("p%d", ref.Number)).Node
				return
			}
			if n.GetFuncCall() != nil {
				if j, ok := aggIndex[deparseExpr(n)]; ok {
					n.Node = cloneNode(valueExpr[j]).Node
					return
				}
			}
			if n.GetColumnRef() != nil || n.GetAExpr() != nil || n.GetFuncCall() != nil || n.GetTypeCast() != nil ||
				n.GetCoalesceExpr() != nil || n.GetCaseExpr() != nil || n.GetNullTest() != nil || n.GetBoolExpr() != nil {
				if k, ok := groupIndex[deparseExpr(n)]; ok {
					n.Node = columnRef(hiddenPrefix+"v", fmt.Sprintf("g%d", k)).Node
				}
			}
		})
		walkNodes(root, func(n *pgq.Node) {
			if ref := n.GetColumnRef(); ref != nil && reason == "" {
				if len(ref.Fields) != 2 || ref.Fields[0].GetString_().GetSval() != hiddenPrefix+"v" {
					reason = "select list reads columns outside GROUP BY"
				}
			}
		})
		return reason
	}
	out := plainSelect(nil, nil)
	out.TargetList = []*pgq.Node{
		resTarget(hiddenPrefix+"sub", columnRef(hiddenPrefix+"v", "idx")),
		resTarget(hiddenPrefix+"gid", columnRef(hiddenPrefix+"v", "gid")),
	}
	for i, t := range sel.TargetList {
		rt := t.GetResTarget()
		if rt.Val.GetColumnRef() != nil && rt.Val.GetColumnRef().Fields[len(rt.Val.GetColumnRef().Fields)-1].GetAStar() != nil {
			return "", "SELECT * in a grouped query", nil
		}
		val := cloneNode(rt.Val)
		if reason := replace(val); reason != "" {
			return "", reason, nil
		}
		out.TargetList = append(out.TargetList, resTarget(names[i], val))
	}
	if sel.HavingClause != nil {
		having := cloneNode(sel.HavingClause)
		if reason := replace(having); reason != "" {
			return "", reason, nil
		}
		out.WhereClause = having
	}
	// The typed source: member index, group ordinal, parameters, group keys
	// and aggregate values, unnested from arrays and cast to their types.
	arrays := []string{"$1::int[]", "$2::int[]"}
	cols := []string{"idx", "gid"}
	typed := []string{"u.idx", "u.gid"}
	add := func(name, typ string) {
		arrays = append(arrays, fmt.Sprintf("$%d::text[]", len(arrays)+1))
		cols = append(cols, name)
		typed = append(typed, fmt.Sprintf("u.%s::%s AS %s", name, typ, name))
	}
	for k, typ := range p.ParamTypes {
		add(fmt.Sprintf("p%d", k+1), typ)
	}
	for k, typ := range p.GroupTypes {
		add(fmt.Sprintf("g%d", k), typ)
	}
	col = 0
	for _, s := range plan.Specs {
		switch s.Kind {
		case AggAvg:
			add(fmt.Sprintf("v%d", col), "numeric")
			add(fmt.Sprintf("v%d", col+1), "bigint")
		default:
			add(fmt.Sprintf("v%d", col), s.ResultType)
		}
		col += s.valueColumns()
	}
	source, err := parseSelect(fmt.Sprintf("SELECT * FROM (SELECT %s FROM unnest(%s) AS u(%s)) AS %sv",
		strings.Join(typed, ", "), strings.Join(arrays, ", "), strings.Join(cols, ", "), hiddenPrefix))
	if err != nil {
		return "", "", err
	}
	out.FromClause = source.FromClause
	sql, err := deparseSelect(out)
	return sql, "", err
}
