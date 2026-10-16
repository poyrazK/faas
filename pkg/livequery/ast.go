package livequery

import (
	"fmt"
	"strings"

	pgq "github.com/pganalyze/pg_query_go/v6"
	pgquery "github.com/wasilibs/go-pgquery"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
)

// Hidden column names. User queries may not produce columns with this prefix.
const hiddenPrefix = "__lq_"

func lineageColumn(rel, key int) string { return fmt.Sprintf("%sl%d_%d", hiddenPrefix, rel, key) }
func groupColumn(k int) string          { return fmt.Sprintf("%sg%d", hiddenPrefix, k) }
func paramColumn(k int) string          { return fmt.Sprintf("%sp%d", hiddenPrefix, k) }

// parseValue parses a scalar SQL expression.
func parseValue(expr string) (*pgq.Node, error) {
	sel, err := parseSelect("SELECT " + expr)
	if err != nil {
		return nil, err
	}
	return sel.TargetList[0].GetResTarget().Val, nil
}

// walkNodes visits every Node in the tree, depth first, including nodes inside
// subqueries. A visitor may replace n.Node in place; the walk then continues
// into the replacement.
func walkNodes(msg proto.Message, visit func(*pgq.Node)) {
	var rec func(m protoreflect.Message)
	rec = func(m protoreflect.Message) {
		if n, ok := m.Interface().(*pgq.Node); ok {
			visit(n)
		}
		m.Range(func(fd protoreflect.FieldDescriptor, v protoreflect.Value) bool {
			switch {
			case fd.IsMap():
			case fd.IsList() && fd.Message() != nil:
				list := v.List()
				for i := 0; i < list.Len(); i++ {
					rec(list.Get(i).Message())
				}
			case fd.Message() != nil:
				rec(v.Message())
			}
			return true
		})
	}
	rec(msg.ProtoReflect())
}

func cloneNode(n *pgq.Node) *pgq.Node { return proto.Clone(n).(*pgq.Node) }

func cloneSelect(s *pgq.SelectStmt) *pgq.SelectStmt { return proto.Clone(s).(*pgq.SelectStmt) }

// plainSelect builds a SELECT over a FROM clause and optional WHERE, with the
// statement options the deparser expects. Callers clone before mutating.
func plainSelect(from []*pgq.Node, where *pgq.Node) *pgq.SelectStmt {
	return &pgq.SelectStmt{
		FromClause:  from,
		WhereClause: where,
		Op:          pgq.SetOperation_SETOP_NONE,
		LimitOption: pgq.LimitOption_LIMIT_OPTION_DEFAULT,
	}
}

// parseSelect parses exactly one SELECT statement.
func parseSelect(sql string) (*pgq.SelectStmt, error) {
	tree, err := pgquery.Parse(sql)
	if err != nil {
		return nil, fmt.Errorf("livequery: parse: %w", err)
	}
	if len(tree.Stmts) != 1 {
		return nil, fmt.Errorf("livequery: want one statement, got %d", len(tree.Stmts))
	}
	sel := tree.Stmts[0].GetStmt().GetSelectStmt()
	if sel == nil {
		return nil, fmt.Errorf("livequery: only SELECT can be live")
	}
	return sel, nil
}

func deparseSelect(sel *pgq.SelectStmt) (string, error) {
	tree := &pgq.ParseResult{Stmts: []*pgq.RawStmt{{Stmt: &pgq.Node{Node: &pgq.Node_SelectStmt{SelectStmt: sel}}}}}
	out, err := pgquery.Deparse(tree)
	if err != nil {
		return "", fmt.Errorf("livequery: deparse: %w", err)
	}
	return out, nil
}

// parseExpr parses a boolean SQL expression by wrapping it in a SELECT.
func parseExpr(expr string) (*pgq.Node, error) {
	sel, err := parseSelect("SELECT 1 WHERE " + expr)
	if err != nil {
		return nil, err
	}
	return sel.WhereClause, nil
}

// andWhere returns sel with extra ANDed into its WHERE clause.
func andWhere(sel *pgq.SelectStmt, extra *pgq.Node) *pgq.SelectStmt {
	out := cloneSelect(sel)
	if out.WhereClause == nil {
		out.WhereClause = extra
		return out
	}
	out.WhereClause = pgq.MakeBoolExprNode(pgq.BoolExprType_AND_EXPR, []*pgq.Node{out.WhereClause, extra}, -1)
	return out
}

func columnRef(parts ...string) *pgq.Node {
	fields := make([]*pgq.Node, len(parts))
	for i, p := range parts {
		fields[i] = pgq.MakeStrNode(p)
	}
	return pgq.MakeColumnRefNode(fields, -1)
}

func resTarget(name string, val *pgq.Node) *pgq.Node {
	return pgq.MakeResTargetNodeWithNameAndVal(name, val, -1)
}

// quoteIdent quotes an identifier for SQL text.
func quoteIdent(s string) string { return `"` + strings.ReplaceAll(s, `"`, `""`) + `"` }

// replacePlaceholders swaps every ColumnRef "__lq_ph"."<name>" for a copy of
// repl[name].
func replacePlaceholders(n *pgq.Node, repl map[string]*pgq.Node) {
	walkNodes(n, func(node *pgq.Node) {
		ref := node.GetColumnRef()
		if ref == nil || len(ref.Fields) != 2 || ref.Fields[0].GetString_().GetSval() != hiddenPrefix+"ph" {
			return
		}
		if expr, ok := repl[ref.Fields[1].GetString_().GetSval()]; ok {
			node.Node = cloneNode(expr).Node
		}
	})
}
