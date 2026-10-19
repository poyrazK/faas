package clientgen

import (
	"fmt"
	"go/format"
	"go/token"
	"strconv"
	"strings"

	"github.com/onebox-faas/faas/pkg/dataapi"
)

// GoPackageValid reports whether name can be the generated file's package.
func GoPackageValid(name string) bool {
	return token.IsIdentifier(name) && !token.IsKeyword(name) && strings.ToLower(name) == name && name != "_"
}

type goRenderer struct {
	m model
	b strings.Builder
}

func (g *goRenderer) printf(format string, args ...any) { fmt.Fprintf(&g.b, format, args...) }

// goType maps a contract type to Go. Nullable values use pointers except for
// slices and raw JSON, whose zero values already decode JSON null.
func (g *goRenderer) goType(t Type) string {
	var base string
	switch t.Kind {
	case KindString:
		base = "string"
	case KindNumber:
		base = "float64"
	case KindBoolean:
		base = "bool"
	case KindEnum:
		if base = g.m.enumName(t); base == "" {
			base = "string"
		}
	case KindArray:
		return "[]" + g.goType(*t.Elem)
	case KindRow:
		if def, ok := g.m.tableByKey[t.Schema+"."+t.Name]; ok {
			base = def.Base + "Row"
		} else {
			return "json.RawMessage"
		}
	case KindComposite:
		if base = g.m.compositeNm[t.Schema+"."+t.Name]; base == "" {
			return "json.RawMessage"
		}
	default:
		return "json.RawMessage"
	}
	if t.Nullable {
		return "*" + base
	}
	return base
}

// optional wraps a type so an omitted value is distinguishable from its zero value.
func (g *goRenderer) optional(t Type) string {
	typ := g.goType(t)
	if strings.HasPrefix(typ, "*") || typ == "json.RawMessage" {
		return typ
	}
	return "*" + typ
}

func (g *goRenderer) fields(columns []dataapi.Column, include func(dataapi.Column) bool, typeOf func(dataapi.Column) (string, bool), reserved ...string) {
	fieldNames := newNames(reserved...)
	for _, c := range columns {
		if !include(c) {
			continue
		}
		typ, omit := typeOf(c)
		tag := c.Name
		if omit {
			tag += ",omitempty"
		}
		g.printf("\t%s %s `json:%s`\n", fieldNames.take(pascal(c.Name)), typ, strconv.Quote(tag))
	}
}

func (g *goRenderer) column(c dataapi.Column) Type {
	t := ParseType(c.Type)
	t.Nullable = t.Nullable || c.Nullable
	return t
}

// RenderGo renders a gofmt-formatted, standard-library-only Go client.
func RenderGo(contract dataapi.Contract, pkg string) ([]byte, error) {
	if !GoPackageValid(pkg) {
		return nil, fmt.Errorf("invalid Go package name %q", pkg)
	}
	g := &goRenderer{m: buildModel(contract)}
	g.printf("%s\npackage %s\n\n", header("//", g.m.Fingerprint), pkg)
	g.printf("import (\n\t\"bytes\"\n\t\"context\"\n\t\"encoding/json\"\n\t\"fmt\"\n\t\"io\"\n\t\"net/http\"\n\t\"net/url\"\n\t\"strconv\"\n\t\"strings\"\n)\n\n")
	g.printf("// SchemaFingerprint identifies the database contract this file was generated from.\nconst SchemaFingerprint = %q\n\n", g.m.Fingerprint)
	for _, e := range g.m.Enums {
		g.printf("// %s is the PostgreSQL enum %s.\ntype %s string\n\nconst (\n", e.Name, e.Key, e.Name)
		constNames := newNames()
		for _, label := range e.Labels {
			g.printf("\t%s %s = %s\n", constNames.take(e.Name+pascal(label)), e.Name, strconv.Quote(label))
		}
		g.printf(")\n\n")
	}
	for _, c := range g.m.Composites {
		name := g.m.compositeNm[c.Schema+"."+c.Name]
		g.printf("// %s is the composite type %s.%s.\ntype %s struct {\n", name, c.Schema, c.Name, name)
		fieldNames := newNames()
		for _, f := range c.Fields {
			t := ParseType(f.Type)
			t.Nullable = t.Nullable || f.Nullable
			g.printf("\t%s %s `json:%s`\n", fieldNames.take(pascal(f.Name)), g.goType(t), strconv.Quote(f.Name))
		}
		g.printf("}\n\n")
	}
	for _, t := range g.m.Tables {
		kind := "table"
		if t.View {
			kind = "view"
		}
		g.printf("// %sRow is a row of the %s %s.%s.\ntype %sRow struct {\n", t.Base, kind, t.Schema, t.Name, t.Base)
		g.fields(t.Columns, func(dataapi.Column) bool { return true }, func(c dataapi.Column) (string, bool) { return g.goType(g.column(c)), false })
		g.printf("}\n\n// %sInsert holds insertable columns; nil pointers use database defaults.\ntype %sInsert struct {\n", t.Base, t.Base)
		g.fields(t.Columns, func(c dataapi.Column) bool { return c.Insertable }, func(c dataapi.Column) (string, bool) {
			if c.Optional {
				return g.optional(g.column(c)), true
			}
			return g.goType(g.column(c)), false
		})
		var nullable []string
		for _, c := range t.Columns {
			if c.Updatable && g.column(c).Nullable {
				nullable = append(nullable, c.Name)
			}
		}
		if len(nullable) == 0 {
			g.printf("}\n\n// %sUpdate holds updatable columns; nil pointers are left unchanged.\ntype %sUpdate struct {\n", t.Base, t.Base)
			g.fields(t.Columns, func(c dataapi.Column) bool { return c.Updatable }, func(c dataapi.Column) (string, bool) { return g.optional(g.column(c)), true })
			g.printf("}\n\n")
			continue
		}
		g.printf("}\n\n// %sUpdate holds updatable columns; nil pointers are left unchanged and\n// SetNull names nullable columns to set to NULL.\ntype %sUpdate struct {\n", t.Base, t.Base)
		g.fields(t.Columns, func(c dataapi.Column) bool { return c.Updatable }, func(c dataapi.Column) (string, bool) { return g.optional(g.column(c)), true }, "SetNull")
		g.printf("\tSetNull []string `json:\"-\"`\n}\n\nfunc (u %sUpdate) setNull() ([]string, map[string]bool) {\n\treturn u.SetNull, map[string]bool{", t.Base)
		for i, name := range nullable {
			if i > 0 {
				g.printf(", ")
			}
			g.printf("%q: true", name)
		}
		g.printf("}\n}\n\n")
	}
	for _, f := range g.m.Functions {
		g.printf("// %s holds the arguments of %s.%s; nil pointers use argument defaults.\ntype %s struct {\n", f.Base, f.Schema, f.Name, f.Base)
		argNames := newNames()
		for _, a := range f.Args {
			t := ParseType(a.Type)
			if a.Optional {
				g.printf("\t%s %s `json:%s`\n", argNames.take(pascal(a.Name)), g.optional(t), strconv.Quote(a.Name+",omitempty"))
			} else {
				g.printf("\t%s %s `json:%s`\n", argNames.take(pascal(a.Name)), g.goType(t), strconv.Quote(a.Name))
			}
		}
		g.printf("}\n\n")
	}
	methods := newNames("BaseURL", "HTTPClient", "AccessToken", "ServerKey", "Subject", "Anonymous", "Relation")
	for _, t := range g.m.Tables {
		insert, update := t.Base+"Insert", t.Base+"Update"
		g.printf("// %s queries %s.%s.\nfunc (c *Client) %s() Table[%sRow, %s, %s] {\n\treturn newTable[%sRow, %s, %s](c, %q, %q)\n}\n\n",
			methods.take(t.Base), t.Schema, t.Name, t.Base, t.Base, insert, update, t.Base, insert, update, t.Schema, t.Name)
	}
	for _, f := range g.m.Functions {
		method := methods.take("RPC" + pascal(qualified(g.m.Schemas, f.Schema, f.Name)))
		if f.Returns.Kind == KindVoid {
			g.printf("// %s calls %s.%s; WithGET sends GET, as public RPCs require.\nfunc (c *Client) %s(ctx context.Context, args %s, opts ...CallOption) error {\n\treturn c.rpc(ctx, %q, %q, args, nil, opts...)\n}\n\n", method, f.Schema, f.Name, method, f.Base, f.Schema, f.Name)
			continue
		}
		result := g.goType(f.Returns)
		g.printf("// %s calls %s.%s; WithGET sends GET, as public RPCs require.\nfunc (c *Client) %s(ctx context.Context, args %s, opts ...CallOption) (%s, error) {\n\tvar out %s\n\terr := c.rpc(ctx, %q, %q, args, &out, opts...)\n\treturn out, err\n}\n\n", method, f.Schema, f.Name, method, f.Base, result, result, f.Schema, f.Name)
	}
	g.b.WriteString(goRuntime)
	return format.Source([]byte(g.b.String()))
}

// goRuntime is the shared client. It never retries writes: a lost response can
// follow a committed write, so callers reconcile before replaying.
const goRuntime = `// Client calls one Gregale Data API. Use AccessToken for application users or
// ServerKey for trusted backends; never ship a server key to a browser.
type Client struct {
	// BaseURL is the API origin, for example https://shop-data.example.com.
	BaseURL     string
	HTTPClient  *http.Client
	AccessToken func(context.Context) (string, error)
	ServerKey   string
	// Subject lets a server key act for one application user; RLS applies.
	Subject string
	// Anonymous sends requests without a token when AccessToken is nil or
	// returns "", for public reads and GET public RPCs. Without it a missing
	// token is an error, so a lost session never silently becomes anonymous.
	Anonymous bool
}

// CallOption changes how an RPC call is sent.
type CallOption func(*callOptions)

type callOptions struct{ get bool }

// WithGET sends a call as GET with its arguments in the query string, as
// public RPCs require for anonymous callers. PostgREST runs it read-only.
func WithGET() CallOption { return func(o *callOptions) { o.get = true } }

// Error is a Data API or database error. Code is the stable gateway or
// PostgREST code when the response carried one.
type Error struct {
	Status    int
	Code      string
	Message   string
	RequestID string
}

func (e *Error) Error() string {
	return fmt.Sprintf("data api: HTTP %d %s: %s (request %s)", e.Status, e.Code, e.Message, e.RequestID)
}

// Table builds a query on one relation. Methods return copies, so a Table
// value can be reused as a prefix for several queries.
type Table[R, I, U any] struct {
	client       *Client
	schema, name string
	params       url.Values
}

func newTable[R, I, U any](c *Client, schema, name string) Table[R, I, U] {
	return Table[R, I, U]{client: c, schema: schema, name: name, params: url.Values{}}
}

// Relation queries a relation without generated types.
func (c *Client) Relation(schema, name string) Table[map[string]any, map[string]any, map[string]any] {
	return newTable[map[string]any, map[string]any, map[string]any](c, schema, name)
}

func (t Table[R, I, U]) with(key, value string) Table[R, I, U] {
	params := url.Values{}
	for k, v := range t.params {
		params[k] = append([]string(nil), v...)
	}
	params.Add(key, value)
	t.params = params
	return t
}

func (t Table[R, I, U]) set(key, value string) Table[R, I, U] {
	t = t.with(key, value)
	t.params.Set(key, value)
	return t
}

func (t Table[R, I, U]) without(keys ...string) Table[R, I, U] {
	params := url.Values{}
	for k, v := range t.params {
		params[k] = append([]string(nil), v...)
	}
	for _, key := range keys {
		params.Del(key)
	}
	t.params = params
	return t
}

// Select chooses columns and embedded relations, e.g. "id,body,tags(name)".
func (t Table[R, I, U]) Select(columns string) Table[R, I, U] { return t.set("select", columns) }

func (t Table[R, I, U]) filter(column, operator string, value any) Table[R, I, U] {
	return t.with(column, operator+"."+formatValue(value))
}

func (t Table[R, I, U]) Eq(column string, value any) Table[R, I, U]  { return t.filter(column, "eq", value) }
func (t Table[R, I, U]) Neq(column string, value any) Table[R, I, U] { return t.filter(column, "neq", value) }
func (t Table[R, I, U]) Gt(column string, value any) Table[R, I, U]  { return t.filter(column, "gt", value) }
func (t Table[R, I, U]) Gte(column string, value any) Table[R, I, U] { return t.filter(column, "gte", value) }
func (t Table[R, I, U]) Lt(column string, value any) Table[R, I, U]  { return t.filter(column, "lt", value) }
func (t Table[R, I, U]) Lte(column string, value any) Table[R, I, U] { return t.filter(column, "lte", value) }
func (t Table[R, I, U]) Like(column, pattern string) Table[R, I, U] {
	return t.filter(column, "like", pattern)
}
func (t Table[R, I, U]) ILike(column, pattern string) Table[R, I, U] {
	return t.filter(column, "ilike", pattern)
}

// Is compares with null, true or false.
func (t Table[R, I, U]) Is(column string, value *bool) Table[R, I, U] {
	if value == nil {
		return t.with(column, "is.null")
	}
	return t.with(column, "is."+strconv.FormatBool(*value))
}

func (t Table[R, I, U]) In(column string, values ...any) Table[R, I, U] {
	quoted := make([]string, len(values))
	for i, v := range values {
		quoted[i] = strconv.Quote(formatValue(v))
	}
	return t.with(column, "in.("+strings.Join(quoted, ",")+")")
}

// Order appends an ordering column; call it again for tie-breakers.
func (t Table[R, I, U]) Order(column string, descending bool) Table[R, I, U] {
	return t.appendOrder(column + "." + direction(descending))
}

// OrderNulls appends an ordering column with explicit NULL placement.
func (t Table[R, I, U]) OrderNulls(column string, descending, nullsFirst bool) Table[R, I, U] {
	nulls := "nullslast"
	if nullsFirst {
		nulls = "nullsfirst"
	}
	return t.appendOrder(column + "." + direction(descending) + "." + nulls)
}

func direction(descending bool) string {
	if descending {
		return "desc"
	}
	return "asc"
}

func (t Table[R, I, U]) appendOrder(item string) Table[R, I, U] {
	if current := t.params.Get("order"); current != "" {
		return t.set("order", current+","+item)
	}
	return t.set("order", item)
}

// Or matches any of PostgREST filters, e.g. "status.eq.draft,id.gt.10".
func (t Table[R, I, U]) Or(filters string) Table[R, I, U] { return t.with("or", "("+filters+")") }

// CursorOrder is one keyset pagination column. The last must be Unique and
// non-null for the filtered rows; uniqueness is the caller's to ensure.
type CursorOrder struct {
	Column     string
	Descending bool
	NullsFirst bool
	Unique     bool
}

// Cursor holds one value per ordering column; build it with CursorFrom.
type Cursor []any

func simpleColumn(name string) bool {
	for i, r := range name {
		if !(r == '_' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || i > 0 && r >= '0' && r <= '9') {
			return false
		}
	}
	return name != ""
}

func checkCursorOrder(order []CursorOrder) error {
	if len(order) == 0 || !order[len(order)-1].Unique {
		return fmt.Errorf("data api: cursor ordering requires a final unique tie-breaker")
	}
	seen := map[string]bool{}
	for _, o := range order {
		if !simpleColumn(o.Column) || seen[o.Column] {
			return fmt.Errorf("data api: cursor columns must be distinct simple column names")
		}
		seen[o.Column] = true
	}
	return nil
}

func quoteLiteral(value string) string {
	return "\"" + strings.NewReplacer("\\", "\\\\", "\"", "\\\"").Replace(value) + "\""
}

func cursorLiteral(value any) (string, error) {
	switch v := value.(type) {
	case string:
		if strings.ContainsFunc(v, func(r rune) bool { return r < 0x20 || r == 0x7f }) {
			return "", fmt.Errorf("data api: cursor strings cannot contain control characters")
		}
		return quoteLiteral(v), nil
	case bool:
		return strconv.FormatBool(v), nil
	case json.Number:
		return v.String(), nil
	case int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64:
		return fmt.Sprint(v), nil
	case float64:
		if v-v == 0 {
			return strconv.FormatFloat(v, 'g', -1, 64), nil
		}
	case float32:
		if v-v == 0 {
			return strconv.FormatFloat(float64(v), 'g', -1, 32), nil
		}
	}
	return "", fmt.Errorf("data api: cursor values must be finite numbers, strings, booleans or nil")
}

// CursorFrom reads a cursor from a row that selected every ordering column.
func CursorFrom(row any, order []CursorOrder) (Cursor, error) {
	if err := checkCursorOrder(order); err != nil {
		return nil, err
	}
	encoded, err := json.Marshal(row)
	if err != nil {
		return nil, err
	}
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.UseNumber()
	var fields map[string]any
	if err := decoder.Decode(&fields); err != nil {
		return nil, fmt.Errorf("data api: cursor rows must be JSON objects: %w", err)
	}
	cursor := make(Cursor, len(order))
	for i, o := range order {
		value, ok := fields[o.Column]
		if !ok {
			return nil, fmt.Errorf("data api: cursor column %q was not selected", o.Column)
		}
		if value != nil {
			if _, err := cursorLiteral(value); err != nil {
				return nil, err
			}
		}
		cursor[i] = value
	}
	if cursor[len(cursor)-1] == nil {
		return nil, fmt.Errorf("data api: cursor tie-breaker must be non-null")
	}
	return cursor, nil
}

// Page applies keyset pagination, replacing any ordering. Keep the same
// filters and order across pages. With before, rows come back nearest-first
// in reverse order; reverse them for display.
func (t Table[R, I, U]) Page(order []CursorOrder, size int, after, before Cursor) (Table[R, I, U], error) {
	if after != nil && before != nil {
		return t, fmt.Errorf("data api: use either after or before, not both")
	}
	if err := checkCursorOrder(order); err != nil {
		return t, err
	}
	if size < 1 {
		return t, fmt.Errorf("data api: cursor page size must be positive")
	}
	cursor, traversal := after, order
	if before != nil {
		cursor, traversal = before, make([]CursorOrder, len(order))
		for i, o := range order {
			traversal[i] = CursorOrder{Column: o.Column, Descending: !o.Descending, NullsFirst: !o.NullsFirst, Unique: o.Unique}
		}
	}
	var branches, prefix []string
	if cursor != nil {
		if len(cursor) != len(order) || cursor[len(cursor)-1] == nil {
			return t, fmt.Errorf("data api: cursor must hold one value per ordering column and a non-null tie-breaker")
		}
		for i, o := range traversal {
			op := "gt"
			if o.Descending {
				op = "lt"
			}
			if cursor[i] == nil {
				if o.NullsFirst {
					branches = append(branches, branch(prefix, o.Column+".not.is.null"))
				}
				prefix = append(prefix, o.Column+".is.null")
				continue
			}
			literal, err := cursorLiteral(cursor[i])
			if err != nil {
				return t, err
			}
			next := "or(" + o.Column + "." + op + "." + literal + "," + o.Column + ".is.null)"
			if o.NullsFirst {
				next = o.Column + "." + op + "." + literal
			}
			branches = append(branches, branch(prefix, next))
			prefix = append(prefix, o.Column+".eq."+literal)
		}
	}
	t = t.without("order", "or")
	for _, o := range traversal {
		t = t.OrderNulls(o.Column, o.Descending, o.NullsFirst)
	}
	if cursor != nil {
		t = t.with("or", "("+strings.Join(branches, ",")+")")
	}
	return t.Limit(size), nil
}

func branch(prefix []string, next string) string {
	if len(prefix) == 0 {
		return next
	}
	return "and(" + strings.Join(append(append([]string(nil), prefix...), next), ",") + ")"
}

func (t Table[R, I, U]) Limit(n int) Table[R, I, U]  { return t.set("limit", strconv.Itoa(n)) }
func (t Table[R, I, U]) Offset(n int) Table[R, I, U] { return t.set("offset", strconv.Itoa(n)) }

// Get reads matching rows. The server caps one response at 1,000 rows.
func (t Table[R, I, U]) Get(ctx context.Context) ([]R, error) {
	var rows []R
	return rows, t.client.do(ctx, http.MethodGet, t.schema, "/rest/v1/"+t.name, t.params, nil, &rows)
}

// GetInto decodes matching rows into out, for example a slice of your own
// structs holding relations embedded with Select("id,body,author:users(name)").
func (t Table[R, I, U]) GetInto(ctx context.Context, out any) error {
	return t.client.do(ctx, http.MethodGet, t.schema, "/rest/v1/"+t.name, t.params, nil, out)
}

// Insert writes rows in one transaction and returns them as stored.
func (t Table[R, I, U]) Insert(ctx context.Context, rows ...I) ([]R, error) {
	var out []R
	return out, t.client.do(ctx, http.MethodPost, t.schema, "/rest/v1/"+t.name, t.params, rows, &out)
}

func (t Table[R, I, U]) filtered() bool {
	for key := range t.params {
		if key != "select" && key != "order" && key != "limit" && key != "offset" {
			return true
		}
	}
	return false
}

// Update changes matching rows; it refuses to run without a filter.
func (t Table[R, I, U]) Update(ctx context.Context, values U) ([]R, error) {
	if !t.filtered() {
		return nil, fmt.Errorf("data api: update of %s.%s requires a filter", t.schema, t.name)
	}
	body, err := updateBody(values)
	if err != nil {
		return nil, err
	}
	var out []R
	return out, t.client.do(ctx, http.MethodPatch, t.schema, "/rest/v1/"+t.name, t.params, body, &out)
}

// nullUpdate is implemented by generated update types with nullable columns.
type nullUpdate interface {
	setNull() ([]string, map[string]bool)
}

// updateBody adds explicit nulls for SetNull columns, which a nil pointer
// cannot express: nil means "leave unchanged".
func updateBody(values any) (any, error) {
	nulls, ok := values.(nullUpdate)
	if !ok {
		return values, nil
	}
	columns, allowed := nulls.setNull()
	if len(columns) == 0 {
		return values, nil
	}
	encoded, err := json.Marshal(values)
	if err != nil {
		return nil, err
	}
	fields := map[string]json.RawMessage{}
	if err := json.Unmarshal(encoded, &fields); err != nil {
		return nil, err
	}
	for _, column := range columns {
		if !allowed[column] {
			return nil, fmt.Errorf("data api: %q is not a nullable updatable column", column)
		}
		if _, set := fields[column]; set {
			return nil, fmt.Errorf("data api: %q is both set and in SetNull", column)
		}
		fields[column] = json.RawMessage("null")
	}
	return fields, nil
}

// Delete removes matching rows; it refuses to run without a filter.
func (t Table[R, I, U]) Delete(ctx context.Context) ([]R, error) {
	if !t.filtered() {
		return nil, fmt.Errorf("data api: delete from %s.%s requires a filter", t.schema, t.name)
	}
	var out []R
	return out, t.client.do(ctx, http.MethodDelete, t.schema, "/rest/v1/"+t.name, t.params, nil, &out)
}

func formatValue(value any) string {
	switch v := value.(type) {
	case nil:
		return "null"
	case string:
		return v
	case bool:
		return strconv.FormatBool(v)
	case fmt.Stringer:
		return v.String()
	}
	return fmt.Sprint(value)
}

func (c *Client) rpc(ctx context.Context, schema, name string, args, out any, opts ...CallOption) error {
	var o callOptions
	for _, opt := range opts {
		opt(&o)
	}
	if !o.get {
		return c.do(ctx, http.MethodPost, schema, "/rest/v1/rpc/"+name, nil, args, out)
	}
	params, err := queryArgs(args)
	if err != nil {
		return err
	}
	return c.do(ctx, http.MethodGet, schema, "/rest/v1/rpc/"+name, params, nil, out)
}

// queryArgs encodes RPC arguments for GET: scalars as text, arrays as
// PostgreSQL array literals and objects as JSON.
func queryArgs(args any) (url.Values, error) {
	encoded, err := json.Marshal(args)
	if err != nil {
		return nil, err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &fields); err != nil {
		return nil, fmt.Errorf("data api: RPC arguments must be an object: %w", err)
	}
	params := url.Values{}
	for name, raw := range fields {
		value, err := queryValue(raw)
		if err != nil {
			return nil, fmt.Errorf("data api: argument %q: %w", name, err)
		}
		params.Set(name, value)
	}
	return params, nil
}

func queryValue(raw json.RawMessage) (string, error) {
	raw = bytes.TrimSpace(raw)
	switch {
	case len(raw) == 0 || string(raw) == "null":
		return "", fmt.Errorf("GET arguments cannot be null; omit them to use defaults")
	case raw[0] == '"':
		var value string
		err := json.Unmarshal(raw, &value)
		return value, err
	case raw[0] == '[':
		decoder := json.NewDecoder(bytes.NewReader(raw))
		decoder.UseNumber()
		var items []any
		if err := decoder.Decode(&items); err != nil {
			return "", err
		}
		return arrayLiteral(items)
	}
	return string(raw), nil
}

func arrayLiteral(items []any) (string, error) {
	parts := make([]string, len(items))
	for i, item := range items {
		switch v := item.(type) {
		case nil:
			parts[i] = "NULL"
		case string:
			parts[i] = quoteLiteral(v)
		case json.Number:
			parts[i] = v.String()
		case bool:
			parts[i] = strconv.FormatBool(v)
		case []any:
			inner, err := arrayLiteral(v)
			if err != nil {
				return "", err
			}
			parts[i] = inner
		default:
			encoded, err := json.Marshal(v)
			if err != nil {
				return "", err
			}
			parts[i] = quoteLiteral(string(encoded))
		}
	}
	return "{" + strings.Join(parts, ",") + "}", nil
}

func (c *Client) do(ctx context.Context, method, schema, path string, params url.Values, body, out any) error {
	target := strings.TrimRight(c.BaseURL, "/") + path
	if len(params) > 0 {
		target += "?" + params.Encode()
	}
	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(encoded)
	}
	req, err := http.NewRequestWithContext(ctx, method, target, reader)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	if method == http.MethodGet {
		req.Header.Set("Accept-Profile", schema)
	} else {
		req.Header.Set("Content-Profile", schema)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Prefer", "return=representation")
	}
	if c.ServerKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.ServerKey)
		if c.Subject != "" {
			req.Header.Set("X-Gregale-Subject", c.Subject)
		}
	} else {
		token := ""
		if c.AccessToken != nil {
			if token, err = c.AccessToken(ctx); err != nil {
				return err
			}
		}
		switch {
		case strings.ContainsAny(token, " \t\r\n\v\f"):
			return fmt.Errorf("data api: the access token is malformed")
		case token != "":
			req.Header.Set("Authorization", "Bearer "+token)
		case !c.Anonymous:
			return fmt.Errorf("data api: an access token is required; set Anonymous for public reads and RPCs")
		}
	}
	httpClient := c.HTTPClient
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 16<<20+1))
	if err != nil {
		return err
	}
	if resp.StatusCode >= 400 {
		var problem struct {
			Code    string ` + "`json:\"code\"`" + `
			Message string ` + "`json:\"message\"`" + `
			Title   string ` + "`json:\"title\"`" + `
		}
		_ = json.Unmarshal(data, &problem)
		if problem.Message == "" {
			problem.Message = problem.Title
		}
		return &Error{Status: resp.StatusCode, Code: problem.Code, Message: problem.Message, RequestID: resp.Header.Get("X-Request-Id")}
	}
	if out == nil || len(bytes.TrimSpace(data)) == 0 {
		return nil
	}
	return json.Unmarshal(data, out)
}
`
