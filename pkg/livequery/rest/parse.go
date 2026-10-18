// Package rest compiles the PostgREST reads that @gregale/data emits into
// SQL for live queries in RLS mode (ADR-20261016151947). The client sends the request
// it would send to the Data API ("messages?select=id,author:users(name)&
// channel_id=eq.1&order=id.desc&limit=50"); livequeryd compiles it here,
// against the database's catalog, and maintains the SQL as the client's
// role and claims, so row-level security decides what it sees.
//
// The subset: select with aliases, casts, JSON paths and foreign-key
// embedding (to-one and to-many, nested, !inner, !hint); filters on the
// queried relation's columns with eq, neq, gt, gte, lt, lte, like, ilike,
// match, imatch, is, in, cs, cd, ov, fts, plfts, phfts, wfts, not., and
// or=/and= trees; order with nullsfirst and nullslast; limit and offset.
// Filter values become parameters, so requests differing only in values
// share one plan.
package rest

import (
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
)

// ErrInvalid marks a request outside the supported subset or malformed.
var ErrInvalid = errors.New("rest: invalid request")

func invalid(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalid, fmt.Sprintf(format, args...))
}

// Request is a parsed PostgREST read.
type Request struct {
	Relation string
	Select   []Item
	Filter   []*Cond // ANDed
	Order    []OrderTerm
	Limit    int // -1: none
	Offset   int
}

// Item is one select entry: a column or an embedded relation.
type Item struct {
	Alias  string
	Column string // "*" for every column
	Path   []PathStep
	Cast   string
	Embed  *Embed
}

// PathStep is one JSON path operator: -> (Text false) or ->> (Text true).
type PathStep struct {
	Key  string
	Text bool
}

// Embed is a related relation selected through a foreign key.
type Embed struct {
	Relation string
	Hint     string // a foreign key or column name disambiguating the relationship
	Inner    bool   // keep only parents with a related row
	Select   []Item
}

// Cond is a filter: a leaf comparing a column, or an and/or group.
type Cond struct {
	Not      bool
	Op       string // eq, neq, …, or "and"/"or" for a group
	Column   string
	Path     []PathStep
	Modifier string // any or all (eq(any).{1,2})
	Config   string // full-text search configuration
	Value    string
	List     []string // in
	Children []*Cond
}

// OrderTerm is one order entry.
type OrderTerm struct {
	Column     string
	Path       []PathStep
	Desc       bool
	NullsFirst *bool
}

// Bounds on what a request may contain.
const (
	maxDepth   = 4  // embed and condition nesting
	maxItems   = 64 // select items, filters and order terms each
	maxListLen = 256
)

// Parse parses "relation?query" as PostgREST reads it.
func Parse(target string) (*Request, error) {
	rel, query, _ := strings.Cut(target, "?")
	rel = strings.TrimPrefix(strings.TrimPrefix(rel, "/rest/v1"), "/")
	if !validName(rel) {
		return nil, invalid("relation %q", rel)
	}
	values, err := url.ParseQuery(query)
	if err != nil {
		return nil, invalid("query string: %v", err)
	}
	req := &Request{Relation: rel, Limit: -1, Select: []Item{{Column: "*"}}}
	for key, vals := range values {
		if len(vals) != 1 {
			return nil, invalid("parameter %s given %d times", key, len(vals))
		}
		v := vals[0]
		switch key {
		case "select":
			if req.Select, err = parseSelect(v, 0); err != nil {
				return nil, err
			}
		case "order":
			if req.Order, err = parseOrder(v); err != nil {
				return nil, err
			}
		case "limit", "offset":
			n, err := strconv.Atoi(v)
			if err != nil || n < 0 {
				return nil, invalid("%s must be a non-negative integer", key)
			}
			if key == "limit" {
				req.Limit = n
			} else {
				req.Offset = n
			}
		case "or", "and", "not.or", "not.and":
			c, err := parseGroup(key, v, 0)
			if err != nil {
				return nil, err
			}
			req.Filter = append(req.Filter, c)
		case "columns", "on_conflict":
			return nil, invalid("parameter %s is for writes", key)
		default:
			if strings.Contains(key, ".") {
				return nil, invalid("filter %s: live queries filter only the queried relation's columns", key)
			}
			c, err := parseLeaf(key, v)
			if err != nil {
				return nil, err
			}
			req.Filter = append(req.Filter, c)
		}
	}
	if len(req.Filter) > maxItems {
		return nil, invalid("more than %d filters", maxItems)
	}
	return req, nil
}

func validName(s string) bool {
	if s == "" || len(s) > 63 {
		return false
	}
	for i, r := range s {
		ok := r == '_' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || i > 0 && r >= '0' && r <= '9'
		if !ok {
			return false
		}
	}
	return true
}

// splitTop splits s on sep outside parentheses, braces and double quotes.
func splitTop(s string, sep byte) ([]string, error) {
	var (
		out   []string
		depth int
		quote bool
		start int
	)
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c == '\\' && quote && i+1 < len(s):
			i++
		case c == '"':
			quote = !quote
		case quote:
		case c == '(' || c == '{':
			depth++
		case c == ')' || c == '}':
			depth--
			if depth < 0 {
				return nil, invalid("unbalanced %q", s)
			}
		case c == sep && depth == 0:
			out = append(out, s[start:i])
			start = i + 1
		}
	}
	if depth != 0 || quote {
		return nil, invalid("unbalanced %q", s)
	}
	return append(out, s[start:]), nil
}

// parseSelect parses a select list.
func parseSelect(s string, depth int) ([]Item, error) {
	if depth > maxDepth {
		return nil, invalid("embedding nested deeper than %d", maxDepth)
	}
	parts, err := splitTop(s, ',')
	if err != nil {
		return nil, err
	}
	if len(parts) > maxItems {
		return nil, invalid("more than %d select items", maxItems)
	}
	var items []Item
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			return nil, invalid("empty select item")
		}
		item, err := parseItem(p, depth)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, nil
}

func parseItem(p string, depth int) (Item, error) {
	var item Item
	// An alias precedes a single colon (a cast is a double one).
	if i := strings.Index(p, ":"); i > 0 && !strings.HasPrefix(p[i:], "::") {
		item.Alias, p = strings.TrimSpace(p[:i]), strings.TrimSpace(p[i+1:])
		if !validName(item.Alias) {
			return item, invalid("alias %q", item.Alias)
		}
	}
	if open := strings.IndexByte(p, '('); open >= 0 {
		if !strings.HasSuffix(p, ")") {
			return item, invalid("embed %q", p)
		}
		head, body := p[:open], p[open+1:len(p)-1]
		embed := &Embed{}
		names := strings.Split(head, "!")
		embed.Relation = names[0]
		for _, h := range names[1:] {
			switch {
			case h == "inner":
				embed.Inner = true
			case h == "left":
			case validName(h) && embed.Hint == "":
				embed.Hint = h
			default:
				return item, invalid("embed hint %q", h)
			}
		}
		if !validName(embed.Relation) {
			return item, invalid("embedded relation %q", embed.Relation)
		}
		var err error
		if embed.Select, err = parseSelect(body, depth+1); err != nil {
			return item, err
		}
		item.Embed = embed
		return item, nil
	}
	if cast, rest, ok := cutCast(p); ok {
		item.Cast, p = cast, rest
	}
	if p == "*" {
		if item.Alias != "" || item.Cast != "" {
			return item, invalid("* takes no alias or cast")
		}
		item.Column = "*"
		return item, nil
	}
	col, path, err := parseField(p)
	if err != nil {
		return item, err
	}
	item.Column, item.Path = col, path
	return item, nil
}

// cutCast splits "expr::type".
func cutCast(p string) (string, string, bool) {
	i := strings.LastIndex(p, "::")
	if i < 0 {
		return "", p, false
	}
	return strings.TrimSpace(p[i+2:]), strings.TrimSpace(p[:i]), true
}

// parseField parses a column with an optional JSON path: data->a->>b.
func parseField(p string) (string, []PathStep, error) {
	i := strings.Index(p, "->")
	if i < 0 {
		if !validName(p) {
			return "", nil, invalid("column %q", p)
		}
		return p, nil, nil
	}
	col := p[:i]
	if !validName(col) {
		return "", nil, invalid("column %q", col)
	}
	var path []PathStep
	for rest := p[i:]; rest != ""; {
		step := PathStep{}
		switch {
		case strings.HasPrefix(rest, "->>"):
			step.Text, rest = true, rest[3:]
		case strings.HasPrefix(rest, "->"):
			rest = rest[2:]
		default:
			return "", nil, invalid("JSON path %q", p)
		}
		end := strings.Index(rest, "->")
		if end < 0 {
			end = len(rest)
		}
		step.Key, rest = rest[:end], rest[end:]
		if !validKey(step.Key) {
			return "", nil, invalid("JSON key %q", step.Key)
		}
		path = append(path, step)
	}
	if len(path) > maxItems {
		return "", nil, invalid("JSON path too long")
	}
	return col, path, nil
}

func validKey(k string) bool {
	if k == "" || len(k) > 128 {
		return false
	}
	for _, r := range k {
		if r != '_' && r != '-' && r != '$' && (r < 'a' || r > 'z') && (r < 'A' || r > 'Z') && (r < '0' || r > '9') {
			return false
		}
	}
	return true
}

func parseOrder(s string) ([]OrderTerm, error) {
	parts, err := splitTop(s, ',')
	if err != nil {
		return nil, err
	}
	if len(parts) > maxItems {
		return nil, invalid("more than %d order terms", maxItems)
	}
	var out []OrderTerm
	for _, p := range parts {
		fields := strings.Split(strings.TrimSpace(p), ".")
		col, path, err := parseField(fields[0])
		if err != nil {
			return nil, err
		}
		term := OrderTerm{Column: col, Path: path}
		for _, f := range fields[1:] {
			switch f {
			case "asc":
			case "desc":
				term.Desc = true
			case "nullsfirst", "nullslast":
				first := f == "nullsfirst"
				term.NullsFirst = &first
			default:
				return nil, invalid("order modifier %q", f)
			}
		}
		out = append(out, term)
	}
	return out, nil
}

// parseLeaf parses "column=[not.]op[(modifier)].value".
func parseLeaf(field, v string) (*Cond, error) {
	col, path, err := parseField(field)
	if err != nil {
		return nil, err
	}
	c := &Cond{Column: col, Path: path}
	if rest, ok := strings.CutPrefix(v, "not."); ok {
		c.Not, v = true, rest
	}
	op, value, ok := strings.Cut(v, ".")
	if !ok {
		return nil, invalid("filter %s=%s", field, v)
	}
	if open := strings.IndexByte(op, '('); open > 0 && strings.HasSuffix(op, ")") {
		arg := op[open+1 : len(op)-1]
		op = op[:open]
		switch {
		case arg == "any" || arg == "all":
			c.Modifier = arg
		case isFTS(op) && validName(arg):
			c.Config = arg
		default:
			return nil, invalid("filter modifier %q", arg)
		}
	}
	c.Op, c.Value = op, value
	switch op {
	case "eq", "neq", "gt", "gte", "lt", "lte", "like", "ilike", "match", "imatch":
		if c.Modifier != "" {
			list, err := parseList(value, '{', '}')
			if err != nil {
				return nil, err
			}
			c.List = list
		}
	case "is":
		switch value {
		case "null", "not_null", "true", "false", "unknown":
		default:
			return nil, invalid("is.%s", value)
		}
	case "in":
		list, err := parseList(value, '(', ')')
		if err != nil {
			return nil, err
		}
		c.List = list
	case "cs", "cd", "ov":
	case "fts", "plfts", "phfts", "wfts":
	default:
		return nil, invalid("filter operator %q", op)
	}
	return c, nil
}

func isFTS(op string) bool { return op == "fts" || op == "plfts" || op == "phfts" || op == "wfts" }

// parseList parses "(a,b,"c,d")" into its values.
func parseList(v string, open, close byte) ([]string, error) {
	if len(v) < 2 || v[0] != open || v[len(v)-1] != close {
		return nil, invalid("list %q", v)
	}
	parts, err := splitTop(v[1:len(v)-1], ',')
	if err != nil {
		return nil, err
	}
	if len(parts) > maxListLen {
		return nil, invalid("list longer than %d", maxListLen)
	}
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if len(p) >= 2 && p[0] == '"' && p[len(p)-1] == '"' {
			p = strings.ReplaceAll(strings.ReplaceAll(p[1:len(p)-1], `\"`, `"`), `\\`, `\`)
		}
		out = append(out, p)
	}
	return out, nil
}

// parseGroup parses or=(…)/and=(…) and their not. forms.
func parseGroup(key, v string, depth int) (*Cond, error) {
	if depth > maxDepth {
		return nil, invalid("conditions nested deeper than %d", maxDepth)
	}
	c := &Cond{}
	if rest, ok := strings.CutPrefix(key, "not."); ok {
		c.Not, key = true, rest
	}
	c.Op = key
	if len(v) < 2 || v[0] != '(' || v[len(v)-1] != ')' {
		return nil, invalid("%s=%s", key, v)
	}
	parts, err := splitTop(v[1:len(v)-1], ',')
	if err != nil {
		return nil, err
	}
	if len(parts) > maxItems {
		return nil, invalid("more than %d conditions in a group", maxItems)
	}
	for _, p := range parts {
		p = strings.TrimSpace(p)
		var child *Cond
		for _, g := range []string{"or", "and", "not.or", "not.and"} {
			if strings.HasPrefix(p, g+"(") {
				if child, err = parseGroup(g, p[len(g):], depth+1); err != nil {
					return nil, err
				}
				break
			}
		}
		if child == nil {
			// column.op.value, column.not.op.value
			field, rest, ok := cutField(p)
			if !ok {
				return nil, invalid("condition %q", p)
			}
			if child, err = parseLeaf(field, rest); err != nil {
				return nil, err
			}
		}
		c.Children = append(c.Children, child)
	}
	return c, nil
}

// cutField splits "data->>a.eq.x" into the field and "eq.x".
func cutField(p string) (string, string, bool) {
	for i := 0; i < len(p); i++ {
		if p[i] == '.' {
			return p[:i], p[i+1:], true
		}
	}
	return "", "", false
}
