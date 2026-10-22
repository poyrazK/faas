package tracing

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"io"
	"sync"
	"testing"
)

// fakeConn implements the context interfaces a modern driver does; legacy
// drops them so database/sql prepares every statement.
type fakeConn struct {
	legacy  bool
	mu      *sync.Mutex
	checked *[]string
}

type fakeDriver struct {
	legacy  bool
	mu      sync.Mutex
	checked []string
}

func (d *fakeDriver) Open(string) (driver.Conn, error) {
	if d.legacy {
		return &legacyConn{}, nil
	}
	return &fakeConn{mu: &d.mu, checked: &d.checked}, nil
}

var errFakeQuery = errors.New("relation \"missing\" does not exist")

func (c *fakeConn) Prepare(query string) (driver.Stmt, error) { return &legacyStmt{query: query}, nil }
func (c *fakeConn) Close() error                              { return nil }
func (c *fakeConn) Begin() (driver.Tx, error)                 { return fakeTx{}, nil }

func (c *fakeConn) QueryContext(_ context.Context, query string, _ []driver.NamedValue) (driver.Rows, error) {
	if query == "SELECT * FROM missing" {
		return nil, errFakeQuery
	}
	return &fakeRows{}, nil
}

func (c *fakeConn) ExecContext(_ context.Context, query string, _ []driver.NamedValue) (driver.Result, error) {
	if query == "VACUUM" {
		// database/sql must fall back to a prepared statement.
		return nil, driver.ErrSkip
	}
	return driver.RowsAffected(1), nil
}

// CheckNamedValue accepts a type the default converter rejects, as pgx does
// for its own types.
func (c *fakeConn) CheckNamedValue(nv *driver.NamedValue) error {
	c.mu.Lock()
	*c.checked = append(*c.checked, "conn")
	c.mu.Unlock()
	if _, ok := nv.Value.(customArg); ok {
		return nil
	}
	return driver.ErrSkip
}

type customArg struct{ v int }

type legacyConn struct{}

func (legacyConn) Prepare(query string) (driver.Stmt, error) { return &legacyStmt{query: query}, nil }
func (legacyConn) Close() error                              { return nil }
func (legacyConn) Begin() (driver.Tx, error)                 { return fakeTx{}, nil }

type legacyStmt struct{ query string }

func (s *legacyStmt) Close() error  { return nil }
func (s *legacyStmt) NumInput() int { return -1 }
func (s *legacyStmt) Exec([]driver.Value) (driver.Result, error) {
	return driver.RowsAffected(1), nil
}
func (s *legacyStmt) Query([]driver.Value) (driver.Rows, error) { return &fakeRows{}, nil }

type fakeTx struct{}

func (fakeTx) Commit() error   { return nil }
func (fakeTx) Rollback() error { return nil }

type fakeRows struct{}

func (*fakeRows) Columns() []string                     { return []string{"id"} }
func (*fakeRows) Close() error                          { return nil }
func (*fakeRows) Next([]driver.Value) error             { return io.EOF }
func (*fakeRows) ColumnTypeDatabaseTypeName(int) string { return "INT8" }

func openFake(t *testing.T, legacy bool) (*sql.DB, *fakeDriver) {
	t.Helper()
	d := &fakeDriver{legacy: legacy}
	db := sql.OpenDB(WrapConnector(dsnConnector{driver: d}, "postgresql"))
	t.Cleanup(func() { _ = db.Close() })
	return db, d
}

func TestSQLRecordsQuerySpansUnderTheRequest(t *testing.T) {
	c, flush := startTracing(t, nil)
	db, d := openFake(t, false)
	ctx, request := StartSpan(context.Background(), "request")

	rows, err := db.QueryContext(ctx, "SELECT id FROM orders WHERE id = $1", customArg{42})
	if err != nil {
		t.Fatalf("query with a driver-specific argument type: %v", err)
	}
	_ = rows.Close()
	if _, err := db.QueryContext(ctx, "SELECT * FROM missing"); !errors.Is(err, errFakeQuery) {
		t.Fatalf("failing query err = %v", err)
	}
	if _, err := db.ExecContext(ctx, "VACUUM"); err != nil {
		t.Fatalf("exec through the prepared fallback: %v", err)
	}
	request.End()
	flush()

	selects := 0
	for _, s := range c.spans {
		if s.Name == "SELECT" {
			selects++
			if s.TraceID != hexOf(request.ctx.trace[:]) || s.ParentSpanID != hexOf(request.ctx.span[:]) || s.Kind != int(KindClient) {
				t.Fatalf("query span %+v is not a client child of the request", s)
			}
			if attr(s, "db.system.name") != "postgresql" || attr(s, "db.operation.name") != "SELECT" {
				t.Fatalf("query span attributes = %+v", s.Attributes)
			}
		}
	}
	if selects != 2 {
		t.Fatalf("SELECT spans = %d, want 2: %+v", selects, c.spans)
	}
	failed := c.byName(t, "SELECT")
	for _, s := range c.spans {
		if attr(s, "db.query.text") == "SELECT * FROM missing" {
			failed = s
		}
	}
	if failed.Status == nil || failed.Status.Message != errFakeQuery.Error() {
		t.Fatalf("failing query span status = %+v", failed.Status)
	}
	vacuums := 0
	for _, s := range c.spans {
		if s.Name == "VACUUM" {
			vacuums++
		}
	}
	if vacuums != 1 {
		t.Fatalf("VACUUM spans = %d, want 1: the skipped direct exec must not record one", vacuums)
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if len(d.checked) == 0 {
		t.Fatal("the driver's CheckNamedValue was bypassed")
	}
}

func TestSQLLegacyDriverRecordsPreparedSpans(t *testing.T) {
	c, flush := startTracing(t, nil)
	db, _ := openFake(t, true)
	if _, err := db.ExecContext(context.Background(), "INSERT INTO orders (id) VALUES ($1)", 7); err != nil {
		t.Fatal(err)
	}
	tx, err := db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	if _, err := db.BeginTx(context.Background(), &sql.TxOptions{ReadOnly: true}); err == nil {
		t.Fatal("read-only transaction accepted by a driver without BeginTx")
	}
	if err := db.PingContext(context.Background()); err != nil {
		t.Fatal(err)
	}
	flush()
	span := c.byName(t, "INSERT")
	if span.ParentSpanID != "" || attr(span, "db.query.text") != "INSERT INTO orders (id) VALUES ($1)" {
		t.Fatalf("legacy insert span = %+v", span)
	}
}

func TestOperationAndTruncate(t *testing.T) {
	for query, want := range map[string]string{
		"  select 1": "SELECT", "WITH x AS (SELECT 1) SELECT * FROM x": "WITH", "(SELECT 1)": "", "": "",
		"supercalifragilistic stuff": "",
	} {
		if got := operationOf(query); got != want {
			t.Errorf("operationOf(%q) = %q, want %q", query, got, want)
		}
	}
	if got := truncate("ééé", 3); got != "é" {
		t.Fatalf("truncate cut a rune: %q", got)
	}
}

func hexOf(b []byte) string {
	const digits = "0123456789abcdef"
	out := make([]byte, 0, len(b)*2)
	for _, c := range b {
		out = append(out, digits[c>>4], digits[c&15])
	}
	return string(out)
}
