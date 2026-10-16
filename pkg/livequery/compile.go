package livequery

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	pgq "github.com/pganalyze/pg_query_go/v6"
)

// Mode is how a query is maintained.
type Mode int

const (
	// ModeLineage maintains a select-project-join query by primary-key
	// lineage: a change re-reads only rows derived from the changed rows.
	ModeLineage Mode = iota + 1
	// ModeGroup maintains a GROUP BY query: input rows by lineage, output by
	// re-aggregating only the affected groups.
	ModeGroup
	// ModeRecompute re-runs the query when a table it reads changes. It is
	// the bounded fallback for constructs without a keyed strategy.
	ModeRecompute
)

func (m Mode) String() string {
	switch m {
	case ModeLineage:
		return "lineage"
	case ModeGroup:
		return "group"
	case ModeRecompute:
		return "recompute"
	}
	return "unknown"
}

// ErrUnsupported marks queries that cannot be live at all.
var ErrUnsupported = errors.New("livequery: query cannot be live")

func unsupported(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrUnsupported, fmt.Sprintf(format, args...))
}

// Relation is one base table in the FROM clause of a keyed query.
type Relation struct {
	Alias    string
	OID      uint32
	Name     string
	Keys     []string
	KeyTypes []string
	// Preserved lists relations on the preserved side of an outer join whose
	// NULL-extended rows can appear or disappear when this relation changes.
	Preserved []int
}

// Plan is a compiled live query.
type Plan struct {
	SQL        string
	NParams    int
	ParamTypes []string
	Mode       Mode
	Reason     string // why ModeRecompute was chosen

	// Every statement below is batched (see batchSelect): $1 is the member
	// index array and $2..$(NParams+1) the members' parameter arrays; any
	// further parameters follow.

	// BatchedSQL is the query itself, batched (ModeRecompute).
	BatchedSQL string

	// Tables maps every table the result depends on to the relation indexes
	// that read it (empty for ModeRecompute).
	Tables map[uint32][]int
	// TableNames gives each table's quoted, schema-qualified name.
	TableNames map[uint32]string

	Relations []*Relation

	// RowSQL is the query with hidden lineage columns (ModeLineage) or the
	// group input query: hidden group keys plus lineage (ModeGroup).
	// RowRestricted[i] adds a restriction to relation i's keys, passed as one
	// text[] parameter per key column after the query's own parameters.
	RowSQL        string
	RowRestricted []string

	// JoinSQL projects the lineage of the FROM clause alone, before WHERE.
	// It exists only when the query has outer joins.
	JoinSQL        string
	JoinRestricted []string

	// ModeGroup: OutputSQL is the query plus hidden group-key columns, and
	// OutputRestricted limits it to a set of groups (one text[] parameter per
	// group key).
	NGroups          int
	GroupTypes       []string
	OutputSQL        string
	OutputRestricted string

	// params holds one hidden, explicitly typed target per query parameter.
	// Every derived query carries them, so parameter numbers and types stay
	// fixed even where the clause that used a parameter was dropped.
	params []*pgq.Node
}

// withParams appends the hidden parameter targets to a target list.
func (p *Plan) withParams(targets []*pgq.Node) []*pgq.Node {
	for _, t := range p.params {
		targets = append(targets, cloneNode(t))
	}
	return targets
}

// HasOuterJoins reports whether join lineage is maintained.
func (p *Plan) HasOuterJoins() bool { return p.JoinSQL != "" }

// timeDependent lists built-in STABLE functions whose result depends on the
// current time rather than on pinned session settings.
var timeDependent = map[string]bool{
	"now": true, "transaction_timestamp": true, "statement_timestamp": true,
}

// Compile parses, validates and rewrites a query. conn is used for catalog
// lookups and to prepare (but never execute) the rewritten statements.
func Compile(ctx context.Context, conn *pgx.Conn, sql string) (*Plan, error) {
	sel, err := parseSelect(sql)
	if err != nil {
		return nil, err
	}
	desc, err := conn.Prepare(ctx, "", sql)
	if err != nil {
		return nil, fmt.Errorf("livequery: prepare query: %w", err)
	}
	for _, f := range desc.Fields {
		if strings.HasPrefix(f.Name, hiddenPrefix) {
			return nil, unsupported("column names starting with %s are reserved", hiddenPrefix)
		}
	}
	p := &Plan{SQL: sql, NParams: len(desc.ParamOIDs), Tables: map[uint32][]int{}, TableNames: map[uint32]string{}}
	if err := checkNodes(sel); err != nil {
		return nil, err
	}
	for k, oid := range desc.ParamOIDs {
		var typ string
		if err := conn.QueryRow(ctx, "SELECT format_type($1, NULL)", oid).Scan(&typ); err != nil {
			return nil, fmt.Errorf("livequery: parameter type: %w", err)
		}
		val, err := parseValue(fmt.Sprintf("$%d::%s", k+1, typ))
		if err != nil {
			return nil, err
		}
		p.params = append(p.params, resTarget(paramColumn(k+1), val))
		p.ParamTypes = append(p.ParamTypes, typ)
	}
	hasAgg, hasWindow, err := checkFunctions(ctx, conn, sel)
	if err != nil {
		return nil, err
	}
	reason := recomputeReason(sel, hasAgg, hasWindow)
	if reason == "" {
		err = p.compileKeyed(ctx, conn, sel)
		var fallback recomputeError
		if errors.As(err, &fallback) {
			reason, err = string(fallback), nil
		}
		if err != nil {
			return nil, err
		}
	}
	if reason != "" {
		p.Mode, p.Reason = ModeRecompute, reason
		p.Relations = nil
		p.Tables = map[uint32][]int{}
		p.TableNames = map[uint32]string{}
		if err := p.collectTables(ctx, conn, sel); err != nil {
			return nil, err
		}
		if p.BatchedSQL, err = batchSelect(sel, p.ParamTypes); err != nil {
			return nil, err
		}
	}
	return p, nil
}

type recomputeError string

func (e recomputeError) Error() string { return string(e) }

// checkNodes rejects constructs that can never be live.
func checkNodes(sel *pgq.SelectStmt) error {
	if sel.IntoClause != nil {
		return unsupported("SELECT INTO")
	}
	if len(sel.LockingClause) > 0 {
		return unsupported("FOR UPDATE/SHARE")
	}
	var err error
	walkNodes(sel, func(n *pgq.Node) {
		if err != nil {
			return
		}
		switch {
		case n.GetSqlvalueFunction() != nil:
			err = unsupported("CURRENT_TIMESTAMP, CURRENT_USER and similar values depend on when or by whom the query runs")
		case n.GetResTarget() != nil && strings.HasPrefix(n.GetResTarget().Name, hiddenPrefix):
			err = unsupported("column names starting with %s are reserved", hiddenPrefix)
		}
	})
	return err
}

// checkFunctions rejects non-deterministic functions and reports whether the
// query uses aggregates or window functions.
func checkFunctions(ctx context.Context, conn *pgx.Conn, sel *pgq.SelectStmt) (hasAgg, hasWindow bool, err error) {
	type call struct {
		schema, name string
		nargs        int
		over         bool
	}
	var calls []call
	walkNodes(sel, func(n *pgq.Node) {
		fc := n.GetFuncCall()
		if fc == nil {
			return
		}
		var parts []string
		for _, f := range fc.Funcname {
			parts = append(parts, f.GetString_().GetSval())
		}
		c := call{name: parts[len(parts)-1], nargs: len(fc.Args), over: fc.Over != nil}
		if len(parts) > 1 {
			c.schema = parts[len(parts)-2]
		}
		calls = append(calls, c)
	})
	for _, c := range calls {
		if c.over {
			hasWindow = true
		}
		if timeDependent[c.name] || (c.name == "age" && c.nargs == 1) {
			return false, false, unsupported("%s() depends on the current time", c.name)
		}
		rows, err := conn.Query(ctx, `
			SELECT p.provolatile::text, p.prokind::text, n.nspname
			FROM pg_proc p JOIN pg_namespace n ON n.oid = p.pronamespace
			WHERE p.proname = $1 AND ($2 = '' OR n.nspname = $2)
			  AND (p.pronargs = $3 OR p.provariadic <> 0 OR p.pronargs - p.pronargdefaults <= $3)`,
			c.name, c.schema, c.nargs)
		if err != nil {
			return false, false, fmt.Errorf("livequery: look up function %s: %w", c.name, err)
		}
		for rows.Next() {
			var volatility, kind, schema string
			if err := rows.Scan(&volatility, &kind, &schema); err != nil {
				rows.Close()
				return false, false, fmt.Errorf("livequery: scan function %s: %w", c.name, err)
			}
			switch {
			case volatility == "v":
				rows.Close()
				return false, false, unsupported("%s() is volatile", c.name)
			case volatility == "s" && schema != "pg_catalog":
				rows.Close()
				return false, false, unsupported("%s.%s() is a user-defined STABLE function and may read tables", schema, c.name)
			}
			switch kind {
			case "a":
				hasAgg = true
			case "w":
				hasWindow = true
			}
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return false, false, fmt.Errorf("livequery: look up function %s: %w", c.name, err)
		}
	}
	return hasAgg, hasWindow, nil
}

// recomputeReason names the first construct without a keyed strategy.
func recomputeReason(sel *pgq.SelectStmt, hasAgg, hasWindow bool) string {
	switch {
	case sel.Op != pgq.SetOperation_SET_OPERATION_UNDEFINED && sel.Op != pgq.SetOperation_SETOP_NONE:
		return "set operation"
	case sel.WithClause != nil:
		return "WITH clause"
	case len(sel.DistinctClause) > 0:
		return "DISTINCT"
	case len(sel.SortClause) > 0, sel.LimitCount != nil, sel.LimitOffset != nil:
		return "ORDER BY or LIMIT"
	case len(sel.WindowClause) > 0, hasWindow:
		return "window function"
	case len(sel.ValuesLists) > 0:
		return "VALUES"
	case len(sel.FromClause) == 0:
		return "no FROM clause"
	case sel.HavingClause != nil && len(sel.GroupClause) == 0:
		return "HAVING without GROUP BY"
	case hasAgg && len(sel.GroupClause) == 0:
		return "aggregate without GROUP BY"
	}
	reason := ""
	walkNodes(sel, func(n *pgq.Node) {
		if reason != "" {
			return
		}
		switch {
		case n.GetSubLink() != nil:
			reason = "subquery"
		case n.GetRangeSubselect() != nil:
			reason = "subquery in FROM"
		case n.GetRangeFunction() != nil:
			reason = "function in FROM"
		case n.GetRangeTableSample() != nil:
			reason = "TABLESAMPLE"
		case n.GetGroupingSet() != nil:
			reason = "grouping sets"
		}
	})
	return reason
}

// joinNode is the FROM tree of a keyed query.
type joinNode struct {
	rel         int // relation index, or -1 for a join
	kind        pgq.JoinType
	left, right *joinNode
}

func (j *joinNode) rels() []int {
	if j.rel >= 0 {
		return []int{j.rel}
	}
	return append(j.left.rels(), j.right.rels()...)
}

func (p *Plan) compileKeyed(ctx context.Context, conn *pgx.Conn, sel *pgq.SelectStmt) error {
	var roots []*joinNode
	for _, item := range sel.FromClause {
		root, err := p.fromItem(ctx, conn, item)
		if err != nil {
			return err
		}
		roots = append(roots, root)
	}
	hasOuter := false
	var mark func(j *joinNode)
	mark = func(j *joinNode) {
		if j.rel >= 0 {
			return
		}
		mark(j.left)
		mark(j.right)
		addPreserved := func(nullable, preserved *joinNode) {
			for _, r := range nullable.rels() {
				p.Relations[r].Preserved = append(p.Relations[r].Preserved, preserved.rels()...)
			}
		}
		switch j.kind {
		case pgq.JoinType_JOIN_LEFT:
			hasOuter = true
			addPreserved(j.right, j.left)
		case pgq.JoinType_JOIN_RIGHT:
			hasOuter = true
			addPreserved(j.left, j.right)
		case pgq.JoinType_JOIN_FULL:
			hasOuter = true
			addPreserved(j.right, j.left)
			addPreserved(j.left, j.right)
		}
	}
	for _, root := range roots {
		mark(root)
	}

	var lineage []*pgq.Node
	for i, rel := range p.Relations {
		for k, key := range rel.Keys {
			lineage = append(lineage, resTarget(lineageColumn(i, k), columnRef(rel.Alias, key)))
		}
	}
	if len(sel.GroupClause) == 0 {
		p.Mode = ModeLineage
		rowSel := cloneSelect(sel)
		rowSel.TargetList = p.withParams(append(rowSel.TargetList, lineage...))
		if err := p.restrictRows(rowSel); err != nil {
			return err
		}
	} else {
		p.Mode = ModeGroup
		groups, err := groupExprs(sel)
		if err != nil {
			return err
		}
		p.NGroups = len(groups)
		input := cloneSelect(plainSelect(sel.FromClause, sel.WhereClause))
		for k, g := range groups {
			input.TargetList = append(input.TargetList, resTarget(groupColumn(k), cloneNode(g)))
		}
		input.TargetList = p.withParams(append(input.TargetList, lineage...))
		if err := p.restrictRows(input); err != nil {
			return err
		}
		if err := p.compileOutput(ctx, conn, sel, groups); err != nil {
			return err
		}
	}
	if hasOuter {
		join := cloneSelect(plainSelect(sel.FromClause, nil))
		join.TargetList = p.withParams(cloneSelect(&pgq.SelectStmt{TargetList: lineage}).TargetList)
		sql, restricted, err := p.restricted(join)
		if err != nil {
			return err
		}
		p.JoinSQL, p.JoinRestricted = sql, restricted
	}
	return nil
}

func (p *Plan) restrictRows(sel *pgq.SelectStmt) error {
	sql, restricted, err := p.restricted(sel)
	if err != nil {
		return err
	}
	p.RowSQL, p.RowRestricted = sql, restricted
	return nil
}

// restricted deparses sel and one variant per relation restricted to a set of
// that relation's primary keys.
func (p *Plan) restricted(sel *pgq.SelectStmt) (string, []string, error) {
	full, err := batchSelect(sel, p.ParamTypes)
	if err != nil {
		return "", nil, err
	}
	out := make([]string, len(p.Relations))
	for i, rel := range p.Relations {
		var arrays, conds []string
		for k, key := range rel.Keys {
			param := p.NParams + 1 + k
			arrays = append(arrays, fmt.Sprintf("$%d::text[]", param))
			conds = append(conds, fmt.Sprintf("%s.%s = %sk.c%d::%s",
				quoteIdent(rel.Alias), quoteIdent(key), hiddenPrefix, k, rel.KeyTypes[k]))
		}
		cols := make([]string, len(rel.Keys))
		for k := range cols {
			cols[k] = fmt.Sprintf("c%d", k)
		}
		expr := fmt.Sprintf("EXISTS (SELECT 1 FROM unnest(%s) AS %sk(%s) WHERE %s)",
			strings.Join(arrays, ", "), hiddenPrefix, strings.Join(cols, ", "), strings.Join(conds, " AND "))
		restriction, err := parseExpr(expr)
		if err != nil {
			return "", nil, err
		}
		if out[i], err = batchSelect(andWhere(sel, restriction), p.ParamTypes); err != nil {
			return "", nil, err
		}
	}
	return full, out, nil
}

// fromItem resolves one FROM item into the join tree, registering relations.
func (p *Plan) fromItem(ctx context.Context, conn *pgx.Conn, item *pgq.Node) (*joinNode, error) {
	if rv := item.GetRangeVar(); rv != nil {
		rel, err := resolveRelation(ctx, conn, rv)
		if err != nil {
			return nil, err
		}
		alias := rv.Relname
		if rv.Alias != nil && rv.Alias.Aliasname != "" {
			alias = rv.Alias.Aliasname
			if len(rv.Alias.Colnames) > 0 {
				return nil, recomputeError("column aliases on a table")
			}
		}
		rel.Alias = alias
		p.Relations = append(p.Relations, rel)
		idx := len(p.Relations) - 1
		p.Tables[rel.OID] = append(p.Tables[rel.OID], idx)
		p.TableNames[rel.OID] = rel.Name
		return &joinNode{rel: idx}, nil
	}
	if je := item.GetJoinExpr(); je != nil {
		if je.Alias != nil {
			return nil, recomputeError("aliased join")
		}
		left, err := p.fromItem(ctx, conn, je.Larg)
		if err != nil {
			return nil, err
		}
		right, err := p.fromItem(ctx, conn, je.Rarg)
		if err != nil {
			return nil, err
		}
		switch je.Jointype {
		case pgq.JoinType_JOIN_INNER, pgq.JoinType_JOIN_LEFT, pgq.JoinType_JOIN_RIGHT, pgq.JoinType_JOIN_FULL:
		default:
			return nil, recomputeError("join type " + je.Jointype.String())
		}
		return &joinNode{rel: -1, kind: je.Jointype, left: left, right: right}, nil
	}
	return nil, recomputeError("FROM item")
}

// resolveRelation looks up a table, its primary key and its replica identity.
func resolveRelation(ctx context.Context, conn *pgx.Conn, rv *pgq.RangeVar) (*Relation, error) {
	name := pgx.Identifier{rv.Relname}
	if rv.Schemaname != "" {
		name = pgx.Identifier{rv.Schemaname, rv.Relname}
	}
	var (
		oid                      uint32
		kind, schema, rel, ident string
	)
	err := conn.QueryRow(ctx, `
		SELECT c.oid, c.relkind::text, n.nspname, c.relname, c.relreplident::text
		FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace
		WHERE c.oid = to_regclass($1)`, name.Sanitize()).Scan(&oid, &kind, &schema, &rel, &ident)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, unsupported("relation %s does not exist", name.Sanitize())
	}
	if err != nil {
		return nil, fmt.Errorf("livequery: resolve %s: %w", name.Sanitize(), err)
	}
	qualified := pgx.Identifier{schema, rel}.Sanitize()
	switch {
	case schema == "pg_catalog" || schema == "information_schema" || schema == "pg_toast":
		return nil, unsupported("system catalog %s", qualified)
	case kind != "r":
		return nil, unsupported("%s is not an ordinary table (relkind %s)", qualified, kind)
	case ident != "d" && ident != "f":
		return nil, unsupported("%s needs REPLICA IDENTITY DEFAULT or FULL", qualified)
	}
	r := &Relation{OID: oid, Name: qualified}
	rows, err := conn.Query(ctx, `
		SELECT a.attname, format_type(a.atttypid, a.atttypmod)
		FROM pg_index i
		CROSS JOIN LATERAL unnest(i.indkey) WITH ORDINALITY AS k(attnum, ord)
		JOIN pg_attribute a ON a.attrelid = i.indrelid AND a.attnum = k.attnum
		WHERE i.indrelid = $1 AND i.indisprimary
		ORDER BY k.ord`, oid)
	if err != nil {
		return nil, fmt.Errorf("livequery: primary key of %s: %w", qualified, err)
	}
	defer rows.Close()
	for rows.Next() {
		var col, typ string
		if err := rows.Scan(&col, &typ); err != nil {
			return nil, fmt.Errorf("livequery: scan primary key of %s: %w", qualified, err)
		}
		r.Keys = append(r.Keys, col)
		r.KeyTypes = append(r.KeyTypes, typ)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("livequery: primary key of %s: %w", qualified, err)
	}
	if len(r.Keys) == 0 {
		return nil, unsupported("%s has no primary key", qualified)
	}
	return r, nil
}

// groupExprs returns the GROUP BY expressions, resolving ordinal references.
func groupExprs(sel *pgq.SelectStmt) ([]*pgq.Node, error) {
	var out []*pgq.Node
	for _, g := range sel.GroupClause {
		if c := g.GetAConst(); c != nil && c.GetIval() != nil {
			pos := int(c.GetIval().Ival)
			if pos < 1 || pos > len(sel.TargetList) {
				return nil, unsupported("GROUP BY position %d is out of range", pos)
			}
			out = append(out, sel.TargetList[pos-1].GetResTarget().Val)
			continue
		}
		if ref := g.GetColumnRef(); ref != nil && len(ref.Fields) == 1 {
			name := ref.Fields[0].GetString_().GetSval()
			for _, t := range sel.TargetList {
				if t.GetResTarget().Name == name {
					return nil, recomputeError("GROUP BY an output column name")
				}
			}
		}
		out = append(out, g)
	}
	return out, nil
}

// compileOutput builds the grouped query with hidden group keys and its
// group-restricted variant. Group key types come from preparing the input.
func (p *Plan) compileOutput(ctx context.Context, conn *pgx.Conn, sel *pgq.SelectStmt, groups []*pgq.Node) error {
	desc, err := conn.Prepare(ctx, "", p.RowSQL)
	if err != nil {
		return fmt.Errorf("livequery: prepare group input: %w", err)
	}
	p.GroupTypes = make([]string, len(groups))
	for _, f := range desc.Fields {
		var k int
		if _, err := fmt.Sscanf(f.Name, hiddenPrefix+"g%d", &k); err != nil || k >= len(groups) {
			continue
		}
		var typ string
		if err := conn.QueryRow(ctx, "SELECT format_type($1, $2)", f.DataTypeOID, f.TypeModifier).Scan(&typ); err != nil {
			return fmt.Errorf("livequery: group key type: %w", err)
		}
		if typ == "unknown" {
			typ = "text"
		}
		p.GroupTypes[k] = typ
	}
	out := cloneSelect(sel)
	for k, g := range groups {
		out.TargetList = append(out.TargetList, resTarget(groupColumn(k), cloneNode(g)))
	}
	out.TargetList = p.withParams(out.TargetList)
	if p.OutputSQL, err = batchSelect(out, p.ParamTypes); err != nil {
		return err
	}
	// Groups are restricted per member: $(NParams+1) lists the member of each
	// group key and the arrays after it the key values.
	arrays := []string{fmt.Sprintf("$%d::int[]", p.NParams+1)}
	cols := []string{"s"}
	conds := []string{fmt.Sprintf("%sgk.s = %ss.idx", hiddenPrefix, hiddenPrefix)}
	repl := map[string]*pgq.Node{}
	for k, g := range groups {
		arrays = append(arrays, fmt.Sprintf("$%d::text[]", p.NParams+2+k))
		cols = append(cols, fmt.Sprintf("c%d", k))
		conds = append(conds, fmt.Sprintf("%sph.g%d IS NOT DISTINCT FROM %sgk.c%d::%s", hiddenPrefix, k, hiddenPrefix, k, p.GroupTypes[k]))
		repl[fmt.Sprintf("g%d", k)] = g
	}
	expr, err := parseExpr(fmt.Sprintf("EXISTS (SELECT 1 FROM unnest(%s) AS %sgk(%s) WHERE %s)",
		strings.Join(arrays, ", "), hiddenPrefix, strings.Join(cols, ", "), strings.Join(conds, " AND ")))
	if err != nil {
		return err
	}
	replacePlaceholders(expr, repl)
	p.OutputRestricted, err = batchSelect(andWhere(out, expr), p.ParamTypes)
	return err
}

// collectTables resolves every table a recompute query reads.
func (p *Plan) collectTables(ctx context.Context, conn *pgx.Conn, sel *pgq.SelectStmt) error {
	ctes := map[string]bool{}
	walkNodes(sel, func(n *pgq.Node) {
		if cte := n.GetCommonTableExpr(); cte != nil {
			ctes[cte.Ctename] = true
		}
	})
	var vars []*pgq.RangeVar
	walkNodes(sel, func(n *pgq.Node) {
		if rv := n.GetRangeVar(); rv != nil && (rv.Schemaname != "" || !ctes[rv.Relname]) {
			vars = append(vars, rv)
		}
	})
	for _, rv := range vars {
		rel, err := resolveRelation(ctx, conn, rv)
		if err != nil {
			return err
		}
		p.Tables[rel.OID] = nil
		p.TableNames[rel.OID] = rel.Name
	}
	return nil
}
