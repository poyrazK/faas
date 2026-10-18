package rest

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"
)

// Relation is a table or view the request reads.
type Relation struct {
	OID     uint32
	Schema  string
	Name    string
	Columns []string // in order
}

// ForeignKey relates two relations.
type ForeignKey struct {
	Name             string
	From, To         uint32 // the referencing and the referenced relation
	FromCols, ToCols []string
}

// Catalog answers the compiler's questions about the database.
type Catalog interface {
	Relation(ctx context.Context, schema, name string) (*Relation, error)
	// ForeignKeys lists the foreign keys between a and b, either way.
	ForeignKeys(ctx context.Context, a, b uint32) ([]ForeignKey, error)
	// Type canonicalises a type name for a cast.
	Type(ctx context.Context, name string) (string, error)
}

// Compiled is a request as SQL, its parameters PostgreSQL text values.
type Compiled struct {
	SQL  string
	Args []any
}

// Compile turns a parsed request on schema into SQL.
func Compile(ctx context.Context, cat Catalog, schema string, req *Request) (*Compiled, error) {
	if !validName(schema) || strings.HasPrefix(schema, "pg_") || schema == "information_schema" || schema == "gregale_live" {
		return nil, invalid("schema %q", schema)
	}
	if (req.Limit >= 0 || req.Offset > 0) && len(req.Order) == 0 {
		return nil, invalid("limit and offset need an order: a live result must be deterministic")
	}
	c := &compiler{ctx: ctx, cat: cat, schema: schema}
	root, err := c.relation(req.Relation)
	if err != nil {
		return nil, err
	}
	alias := c.alias()
	sel, err := c.selectList(req.Select, root, alias, 0)
	if err != nil {
		return nil, err
	}
	var b strings.Builder
	fmt.Fprintf(&b, "SELECT %s FROM %s %s", sel, qualified(root), quote(alias))
	var where []string
	for _, f := range req.Filter {
		cond, err := c.cond(f, root, alias, 0)
		if err != nil {
			return nil, err
		}
		where = append(where, cond)
	}
	where = append(where, c.inner...)
	if len(where) > 0 {
		b.WriteString(" WHERE " + strings.Join(where, " AND "))
	}
	if len(req.Order) > 0 {
		var terms []string
		for _, o := range req.Order {
			expr, err := c.field(root, alias, o.Column, o.Path)
			if err != nil {
				return nil, err
			}
			term := expr
			if o.Desc {
				term += " DESC"
			}
			if o.NullsFirst != nil {
				if *o.NullsFirst {
					term += " NULLS FIRST"
				} else {
					term += " NULLS LAST"
				}
			}
			terms = append(terms, term)
		}
		b.WriteString(" ORDER BY " + strings.Join(terms, ", "))
	}
	if req.Limit >= 0 {
		b.WriteString(" LIMIT " + strconv.Itoa(req.Limit))
	}
	if req.Offset > 0 {
		b.WriteString(" OFFSET " + strconv.Itoa(req.Offset))
	}
	return &Compiled{SQL: b.String(), Args: c.args}, nil
}

type compiler struct {
	ctx     context.Context
	cat     Catalog
	schema  string
	args    []any
	aliases int
	inner   []string // EXISTS conditions from top-level !inner embeds
}

func (c *compiler) alias() string {
	c.aliases++
	return "t" + strconv.Itoa(c.aliases-1)
}

func (c *compiler) param(v any) string {
	c.args = append(c.args, v)
	return "$" + strconv.Itoa(len(c.args))
}

func (c *compiler) relation(name string) (*Relation, error) {
	rel, err := c.cat.Relation(c.ctx, c.schema, name)
	if err != nil {
		return nil, err
	}
	if rel == nil {
		return nil, invalid("no relation %s.%s", c.schema, name)
	}
	return rel, nil
}

func quote(id string) string { return `"` + strings.ReplaceAll(id, `"`, `""`) + `"` }

func qualified(r *Relation) string { return quote(r.Schema) + "." + quote(r.Name) }

func hasColumn(r *Relation, col string) bool {
	for _, c := range r.Columns {
		if c == col {
			return true
		}
	}
	return false
}

// field renders a column of rel with its JSON path.
func (c *compiler) field(rel *Relation, alias, col string, path []PathStep) (string, error) {
	if !hasColumn(rel, col) {
		return "", invalid("no column %s on %s", col, rel.Name)
	}
	expr := quote(alias) + "." + quote(col)
	for _, step := range path {
		op := "->"
		if step.Text {
			op = "->>"
		}
		if n, err := strconv.Atoi(step.Key); err == nil {
			expr += op + strconv.Itoa(n)
		} else {
			expr += op + "'" + step.Key + "'" // validKey admits no quote
		}
	}
	return expr, nil
}

func (c *compiler) selectList(items []Item, rel *Relation, alias string, depth int) (string, error) {
	var cols []string
	for _, it := range items {
		switch {
		case it.Embed != nil:
			expr, err := c.embed(it, rel, alias, depth)
			if err != nil {
				return "", err
			}
			cols = append(cols, expr)
		case it.Column == "*":
			for _, col := range rel.Columns {
				cols = append(cols, quote(alias)+"."+quote(col)+" AS "+quote(col))
			}
		default:
			expr, err := c.field(rel, alias, it.Column, it.Path)
			if err != nil {
				return "", err
			}
			if it.Cast != "" {
				typ, err := c.cat.Type(c.ctx, it.Cast)
				if err != nil {
					return "", err
				}
				expr = "CAST(" + expr + " AS " + typ + ")"
			}
			name := it.Alias
			if name == "" {
				name = it.Column
				if len(it.Path) > 0 {
					name = it.Path[len(it.Path)-1].Key // PostgREST names a path by its last key
				}
			}
			cols = append(cols, expr+" AS "+quote(name))
		}
	}
	if len(cols) == 0 {
		return "", invalid("empty select")
	}
	return strings.Join(cols, ", "), nil
}

// embed renders a related relation as a JSON column: an object (or null)
// through a foreign key the parent holds, an array through one the related
// relation holds.
func (c *compiler) embed(it Item, parent *Relation, parentAlias string, depth int) (string, error) {
	e := it.Embed
	rel, err := c.relation(e.Relation)
	if err != nil {
		return "", err
	}
	fk, toOne, err := c.relationship(parent, rel, e.Hint)
	if err != nil {
		return "", err
	}
	alias := c.alias()
	join := make([]string, len(fk.FromCols))
	for i := range fk.FromCols {
		if toOne { // parent.fromCol = rel.toCol
			join[i] = quote(parentAlias) + "." + quote(fk.FromCols[i]) + " = " + quote(alias) + "." + quote(fk.ToCols[i])
		} else { // rel.fromCol = parent.toCol
			join[i] = quote(alias) + "." + quote(fk.FromCols[i]) + " = " + quote(parentAlias) + "." + quote(fk.ToCols[i])
		}
	}
	on := strings.Join(join, " AND ")
	sel, err := c.selectList(e.Select, rel, alias, depth+1)
	if err != nil {
		return "", err
	}
	inner := fmt.Sprintf("SELECT %s FROM %s %s WHERE %s", sel, qualified(rel), quote(alias), on)
	if e.Inner {
		exists := fmt.Sprintf("EXISTS (SELECT 1 FROM %s %s WHERE %s)", qualified(rel), quote(alias), on)
		if depth > 0 {
			return "", invalid("!inner is supported on top-level embeds only")
		}
		c.inner = append(c.inner, exists)
	}
	name := it.Alias
	if name == "" {
		name = e.Relation
	}
	e2 := quote("e" + alias)
	if toOne {
		return fmt.Sprintf("(SELECT row_to_json(%s) FROM (%s) %s) AS %s", e2, inner, e2, quote(name)), nil
	}
	return fmt.Sprintf("(SELECT coalesce(json_agg(%s), '[]'::json) FROM (%s) %s) AS %s", e2, inner, e2, quote(name)), nil
}

// relationship picks the foreign key between parent and rel; toOne is true
// when the parent holds it. hint names a foreign key or a column of it.
func (c *compiler) relationship(parent, rel *Relation, hint string) (ForeignKey, bool, error) {
	fks, err := c.cat.ForeignKeys(c.ctx, parent.OID, rel.OID)
	if err != nil {
		return ForeignKey{}, false, err
	}
	var match []ForeignKey
	for _, fk := range fks {
		if hint == "" || fk.Name == hint || hintsColumn(fk, hint) {
			match = append(match, fk)
		}
	}
	switch {
	case len(match) == 0:
		return ForeignKey{}, false, invalid("no foreign key relates %s and %s", parent.Name, rel.Name)
	case len(match) > 1:
		return ForeignKey{}, false, invalid("several foreign keys relate %s and %s; name one with %s!<foreign key>", parent.Name, rel.Name, rel.Name)
	}
	fk := match[0]
	if fk.From == fk.To {
		// A self-reference: the hint decides the direction; by default the
		// parent holds the key (to-one), as PostgREST does.
		return fk, true, nil
	}
	return fk, fk.From == parent.OID, nil
}

func hintsColumn(fk ForeignKey, hint string) bool {
	for _, col := range append(append([]string{}, fk.FromCols...), fk.ToCols...) {
		if col == hint {
			return true
		}
	}
	return false
}

var sqlOps = map[string]string{"eq": "=", "neq": "<>", "gt": ">", "gte": ">=", "lt": "<", "lte": "<=",
	"like": "LIKE", "ilike": "ILIKE", "match": "~", "imatch": "~*", "cs": "@>", "cd": "<@", "ov": "&&"}

func (c *compiler) cond(f *Cond, rel *Relation, alias string, depth int) (string, error) {
	var out string
	switch f.Op {
	case "and", "or":
		var parts []string
		for _, child := range f.Children {
			p, err := c.cond(child, rel, alias, depth+1)
			if err != nil {
				return "", err
			}
			parts = append(parts, p)
		}
		if len(parts) == 0 {
			return "", invalid("empty %s", f.Op)
		}
		out = "(" + strings.Join(parts, " "+strings.ToUpper(f.Op)+" ") + ")"
	default:
		expr, err := c.field(rel, alias, f.Column, f.Path)
		if err != nil {
			return "", err
		}
		if out, err = c.leaf(f, expr); err != nil {
			return "", err
		}
	}
	if f.Not {
		out = "NOT " + out
	}
	return out, nil
}

func (c *compiler) leaf(f *Cond, expr string) (string, error) {
	switch op := f.Op; {
	case op == "is":
		return "(" + expr + " IS " + map[string]string{"null": "NULL", "not_null": "NOT NULL", "true": "TRUE", "false": "FALSE", "unknown": "UNKNOWN"}[f.Value] + ")", nil
	case op == "in":
		return "(" + expr + " = ANY(" + c.param(arrayLiteral(f.List)) + "))", nil
	case isFTS(op):
		fn := map[string]string{"fts": "to_tsquery", "plfts": "plainto_tsquery", "phfts": "phraseto_tsquery", "wfts": "websearch_to_tsquery"}[op]
		if f.Config != "" {
			return "(" + expr + " @@ " + fn + "(" + c.param(f.Config) + "::regconfig, " + c.param(f.Value) + "))", nil
		}
		return "(" + expr + " @@ " + fn + "(" + c.param(f.Value) + "))", nil
	case f.Modifier != "":
		return "(" + expr + " " + sqlOps[op] + " " + strings.ToUpper(f.Modifier) + "(" + c.param(arrayLiteral(f.List)) + "))", nil
	case op == "like" || op == "ilike":
		return "(" + expr + " " + sqlOps[op] + " " + c.param(strings.ReplaceAll(f.Value, "*", "%")) + ")", nil
	default:
		sqlOp, ok := sqlOps[op]
		if !ok {
			return "", invalid("filter operator %q", op)
		}
		return "(" + expr + " " + sqlOp + " " + c.param(f.Value) + ")", nil
	}
}

// arrayLiteral renders values as a PostgreSQL array literal.
func arrayLiteral(vals []string) string {
	parts := make([]string, len(vals))
	for i, v := range vals {
		parts[i] = `"` + strings.ReplaceAll(strings.ReplaceAll(v, `\`, `\\`), `"`, `\"`) + `"`
	}
	return "{" + strings.Join(parts, ",") + "}"
}

// PG answers Catalog from a connection.
type PG struct{ Conn *pgx.Conn }

// Relation reads a relation's columns; nil when it does not exist.
func (p PG) Relation(ctx context.Context, schema, name string) (*Relation, error) {
	rows, err := p.Conn.Query(ctx, `SELECT c.oid, a.attname FROM pg_class c
		JOIN pg_namespace n ON n.oid = c.relnamespace
		JOIN pg_attribute a ON a.attrelid = c.oid AND a.attnum > 0 AND NOT a.attisdropped
		WHERE n.nspname = $1 AND c.relname = $2 AND c.relkind IN ('r', 'p', 'v', 'm', 'f')
		ORDER BY a.attnum`, schema, name)
	if err != nil {
		return nil, fmt.Errorf("rest: read relation: %w", err)
	}
	defer rows.Close()
	var rel *Relation
	for rows.Next() {
		var oid uint32
		var col string
		if err := rows.Scan(&oid, &col); err != nil {
			return nil, fmt.Errorf("rest: read relation: %w", err)
		}
		if rel == nil {
			rel = &Relation{OID: oid, Schema: schema, Name: name}
		}
		rel.Columns = append(rel.Columns, col)
	}
	return rel, rows.Err()
}

// ForeignKeys lists the foreign keys between a and b.
func (p PG) ForeignKeys(ctx context.Context, a, b uint32) ([]ForeignKey, error) {
	rows, err := p.Conn.Query(ctx, `SELECT con.conname, con.conrelid, con.confrelid,
		ARRAY(SELECT att.attname FROM unnest(con.conkey) WITH ORDINALITY k(n, i) JOIN pg_attribute att ON att.attrelid = con.conrelid AND att.attnum = k.n ORDER BY k.i)::text[],
		ARRAY(SELECT att.attname FROM unnest(con.confkey) WITH ORDINALITY k(n, i) JOIN pg_attribute att ON att.attrelid = con.confrelid AND att.attnum = k.n ORDER BY k.i)::text[]
		FROM pg_constraint con WHERE con.contype = 'f'
		AND ((con.conrelid = $1 AND con.confrelid = $2) OR (con.conrelid = $2 AND con.confrelid = $1))
		ORDER BY con.conname`, a, b)
	if err != nil {
		return nil, fmt.Errorf("rest: read foreign keys: %w", err)
	}
	defer rows.Close()
	var out []ForeignKey
	for rows.Next() {
		var fk ForeignKey
		if err := rows.Scan(&fk.Name, &fk.From, &fk.To, &fk.FromCols, &fk.ToCols); err != nil {
			return nil, fmt.Errorf("rest: read foreign keys: %w", err)
		}
		out = append(out, fk)
	}
	return out, rows.Err()
}

// Type canonicalises a cast's type name.
func (p PG) Type(ctx context.Context, name string) (string, error) {
	if len(name) > 63 || strings.ContainsAny(name, ";'\"\\") {
		return "", invalid("cast type %q", name)
	}
	var typ *string
	if err := p.Conn.QueryRow(ctx, `SELECT format_type(to_regtype($1), NULL)`, name).Scan(&typ); err != nil {
		var pgErr interface{ SQLState() string }
		if errors.As(err, &pgErr) {
			return "", invalid("cast type %q", name)
		}
		return "", fmt.Errorf("rest: read type: %w", err)
	}
	if typ == nil {
		return "", invalid("no type %q", name)
	}
	return *typ, nil
}
