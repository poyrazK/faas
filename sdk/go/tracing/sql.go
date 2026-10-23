package tracing

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"io"
	"strings"
	"unicode"
)

// queryTextMaxBytes bounds the db.query.text attribute.
const queryTextMaxBytes = 1024

// Open opens a database like sql.Open and records a client span for every
// query and statement run with a context. system names the database for the
// debugger, using the OpenTelemetry db.system.name values: "postgresql",
// "mysql", "sqlite", "mssql" and so on.
func Open(driverName, dataSourceName, system string) (*sql.DB, error) {
	db, err := sql.Open(driverName, dataSourceName)
	if err != nil {
		return nil, err
	}
	d := db.Driver()
	_ = db.Close()
	if dc, ok := d.(driver.DriverContext); ok {
		c, err := dc.OpenConnector(dataSourceName)
		if err != nil {
			return nil, err
		}
		return sql.OpenDB(WrapConnector(c, system)), nil
	}
	return sql.OpenDB(WrapConnector(dsnConnector{driver: d, dsn: dataSourceName}, system)), nil
}

// WrapConnector wraps a connector for sql.OpenDB; see Open.
func WrapConnector(c driver.Connector, system string) driver.Connector {
	return &connector{base: c, system: system}
}

// WrapDriver wraps a driver for sql.Register; see Open.
func WrapDriver(d driver.Driver, system string) driver.Driver {
	return &wrappedDriver{base: d, system: system}
}

type wrappedDriver struct {
	base   driver.Driver
	system string
}

func (d *wrappedDriver) Open(name string) (driver.Conn, error) {
	c, err := d.base.Open(name)
	if err != nil {
		return nil, err
	}
	return &conn{base: c, system: d.system}, nil
}

func (d *wrappedDriver) OpenConnector(name string) (driver.Connector, error) {
	if dc, ok := d.base.(driver.DriverContext); ok {
		c, err := dc.OpenConnector(name)
		if err != nil {
			return nil, err
		}
		return WrapConnector(c, d.system), nil
	}
	return WrapConnector(dsnConnector{driver: d.base, dsn: name}, d.system), nil
}

type connector struct {
	base   driver.Connector
	system string
}

func (c *connector) Connect(ctx context.Context) (driver.Conn, error) {
	conn0, err := c.base.Connect(ctx)
	if err != nil {
		return nil, err
	}
	return &conn{base: conn0, system: c.system}, nil
}

func (c *connector) Driver() driver.Driver {
	return &wrappedDriver{base: c.base.Driver(), system: c.system}
}

// Close closes the base connector when it holds resources (sql.DB.Close
// calls it).
func (c *connector) Close() error {
	if closer, ok := c.base.(io.Closer); ok {
		return closer.Close()
	}
	return nil
}

// dsnConnector adapts a driver without DriverContext, as database/sql does.
type dsnConnector struct {
	driver driver.Driver
	dsn    string
}

func (c dsnConnector) Connect(context.Context) (driver.Conn, error) { return c.driver.Open(c.dsn) }
func (c dsnConnector) Driver() driver.Driver                        { return c.driver }

// conn implements every optional connection interface and falls back the way
// database/sql does when the base connection lacks one.
type conn struct {
	base   driver.Conn
	system string
}

func (c *conn) Close() error { return c.base.Close() }

// Begin is the legacy entry point; database/sql calls BeginTx.
func (c *conn) Begin() (driver.Tx, error) { return c.base.Begin() } //nolint:staticcheck // driver.Conn requires it.

func (c *conn) Prepare(query string) (driver.Stmt, error) {
	s, err := c.base.Prepare(query)
	if err != nil {
		return nil, err
	}
	return &stmt{base: s, conn: c.base, query: query, system: c.system}, nil
}

func (c *conn) PrepareContext(ctx context.Context, query string) (driver.Stmt, error) {
	var s driver.Stmt
	var err error
	if p, ok := c.base.(driver.ConnPrepareContext); ok {
		s, err = p.PrepareContext(ctx, query)
	} else {
		if err = ctx.Err(); err == nil {
			s, err = c.base.Prepare(query)
		}
	}
	if err != nil {
		return nil, err
	}
	return &stmt{base: s, conn: c.base, query: query, system: c.system}, nil
}

func (c *conn) BeginTx(ctx context.Context, opts driver.TxOptions) (driver.Tx, error) {
	if b, ok := c.base.(driver.ConnBeginTx); ok {
		return b.BeginTx(ctx, opts)
	}
	if opts.Isolation != driver.IsolationLevel(sql.LevelDefault) {
		return nil, errors.New("sql: driver does not support non-default isolation level")
	}
	if opts.ReadOnly {
		return nil, errors.New("sql: driver does not support read-only transactions")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return c.base.Begin() //nolint:staticcheck // the fallback database/sql uses.
}

// ExecContext and QueryContext return driver.ErrSkip when the base lacks
// them, so database/sql prepares a statement and the stmt records the span.
func (c *conn) ExecContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Result, error) {
	execer, ok := c.base.(driver.ExecerContext)
	if !ok {
		return nil, driver.ErrSkip
	}
	span := startQuery(ctx, c.system, query)
	res, err := execer.ExecContext(ctx, query, args)
	span.endQuery(err)
	return res, err
}

func (c *conn) QueryContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	queryer, ok := c.base.(driver.QueryerContext)
	if !ok {
		return nil, driver.ErrSkip
	}
	span := startQuery(ctx, c.system, query)
	rows, err := queryer.QueryContext(ctx, query, args)
	span.endQuery(err)
	return rows, err
}

func (c *conn) Ping(ctx context.Context) error {
	if p, ok := c.base.(driver.Pinger); ok {
		return p.Ping(ctx)
	}
	return nil
}

func (c *conn) ResetSession(ctx context.Context) error {
	if r, ok := c.base.(driver.SessionResetter); ok {
		return r.ResetSession(ctx)
	}
	return nil
}

func (c *conn) IsValid() bool {
	if v, ok := c.base.(driver.Validator); ok {
		return v.IsValid()
	}
	return true
}

// CheckNamedValue keeps the base driver's argument conversion; ErrSkip makes
// database/sql use its default.
func (c *conn) CheckNamedValue(nv *driver.NamedValue) error {
	if checker, ok := c.base.(driver.NamedValueChecker); ok {
		return checker.CheckNamedValue(nv)
	}
	return driver.ErrSkip
}

type stmt struct {
	base   driver.Stmt
	conn   driver.Conn
	query  string
	system string
}

func (s *stmt) Close() error  { return s.base.Close() }
func (s *stmt) NumInput() int { return s.base.NumInput() }

func (s *stmt) Exec(args []driver.Value) (driver.Result, error) { //nolint:staticcheck // driver.Stmt requires it.
	return s.base.Exec(args) //nolint:staticcheck // forwarded legacy call.
}

func (s *stmt) Query(args []driver.Value) (driver.Rows, error) { //nolint:staticcheck // driver.Stmt requires it.
	return s.base.Query(args) //nolint:staticcheck // forwarded legacy call.
}

func (s *stmt) ExecContext(ctx context.Context, args []driver.NamedValue) (driver.Result, error) {
	span := startQuery(ctx, s.system, s.query)
	var res driver.Result
	var err error
	if e, ok := s.base.(driver.StmtExecContext); ok {
		res, err = e.ExecContext(ctx, args)
	} else {
		var values []driver.Value
		if values, err = namedValues(ctx, args); err == nil {
			res, err = s.base.Exec(values) //nolint:staticcheck // the fallback database/sql uses.
		}
	}
	span.endQuery(err)
	return res, err
}

func (s *stmt) QueryContext(ctx context.Context, args []driver.NamedValue) (driver.Rows, error) {
	span := startQuery(ctx, s.system, s.query)
	var rows driver.Rows
	var err error
	if q, ok := s.base.(driver.StmtQueryContext); ok {
		rows, err = q.QueryContext(ctx, args)
	} else {
		var values []driver.Value
		if values, err = namedValues(ctx, args); err == nil {
			rows, err = s.base.Query(values) //nolint:staticcheck // the fallback database/sql uses.
		}
	}
	span.endQuery(err)
	return rows, err
}

// CheckNamedValue prefers the statement's checker, then the connection's, as
// database/sql would without the wrapper.
func (s *stmt) CheckNamedValue(nv *driver.NamedValue) error {
	if checker, ok := s.base.(driver.NamedValueChecker); ok {
		return checker.CheckNamedValue(nv)
	}
	if checker, ok := s.conn.(driver.NamedValueChecker); ok {
		return checker.CheckNamedValue(nv)
	}
	return driver.ErrSkip
}

func (s *stmt) ColumnConverter(idx int) driver.ValueConverter {
	if c, ok := s.base.(driver.ColumnConverter); ok { //nolint:staticcheck // forwarded for drivers that still use it.
		return c.ColumnConverter(idx)
	}
	return driver.DefaultParameterConverter
}

func namedValues(ctx context.Context, args []driver.NamedValue) ([]driver.Value, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	values := make([]driver.Value, len(args))
	for i, arg := range args {
		if arg.Name != "" {
			return nil, errors.New("sql: driver does not support the use of Named Parameters")
		}
		values[i] = arg.Value
	}
	return values, nil
}

// querySpan is a client span for one query, nil while tracing is off.
type querySpan struct{ span *Span }

func startQuery(ctx context.Context, system, query string) querySpan {
	t := active.Load()
	if t == nil {
		return querySpan{}
	}
	op := operationOf(query)
	name := op
	if name == "" {
		name = system
	}
	_, span := t.start(ctx, name, KindClient, spanContext{})
	span.SetAttribute("db.system.name", system)
	if op != "" {
		span.SetAttribute("db.operation.name", op)
	}
	span.SetAttribute("db.query.text", truncate(strings.TrimSpace(query), queryTextMaxBytes))
	return querySpan{span: span}
}

// endQuery finishes the span; a driver.ErrSkip means database/sql retries the
// call another way, which records its own span.
func (q querySpan) endQuery(err error) {
	if q.span == nil || errors.Is(err, driver.ErrSkip) {
		return
	}
	if err != nil {
		q.span.RecordError(err)
	}
	q.span.End()
}

// operationOf returns the statement's leading keyword ("SELECT").
func operationOf(query string) string {
	word := strings.TrimSpace(query)
	if i := strings.IndexFunc(word, func(r rune) bool { return !unicode.IsLetter(r) }); i >= 0 {
		word = word[:i]
	}
	if len(word) > 16 {
		return ""
	}
	return strings.ToUpper(word)
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	cut := max
	for cut > 0 && !utf8Start(s[cut]) {
		cut--
	}
	return s[:cut]
}

func utf8Start(b byte) bool { return b&0xC0 != 0x80 }
