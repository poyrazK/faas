package main

// `gregale postgres sql`: run one query against a managed database and print
// the result as a table or, with --json, as typed rows an agent can read.
//
// It borrows the `postgres connect` tunnel (a bound app's task VM reads the
// binding and relays a local port; credentials never print) but speaks the
// wire protocol itself, so no local psql is needed.
//
// Read-only is the default. The statement runs as the only statement of a
// READ ONLY transaction over the extended protocol, which refuses a second
// statement, so it cannot COMMIT its way out; the transaction is always
// rolled back, and a read_only binding is preferred when the database has
// one. --write runs the text as a script over the simple protocol
// (autocommit, several statements allowed) on a writable binding.

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"os/signal"
	"slices"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgconn/ctxwatch"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
)

const postgresSQLUsage = "usage: gregale postgres query <database> (<query> | --file PATH | --file -) [--write] [--max-rows N] [--timeout D] [--app SLUG] [--env KEY] [--access MODE] [--wait-timeout D] [--json]"

const (
	defaultPostgresSQLMaxRows = 500
	maxPostgresSQLMaxRows     = 10000
	defaultPostgresSQLTimeout = 30 * time.Second
	// postgresSQLQueryLimit bounds the query text read from a file or stdin.
	postgresSQLQueryLimit = 1 << 20
	// postgresSQLCellLimit bounds one cell in the human table; --json keeps
	// whole values.
	postgresSQLCellLimit = 120
)

// postgresSQLReadOnlyOrder prefers the least privileged binding for reads.
var postgresSQLReadOnlyOrder = []string{"read_only", "session", "read_write", "migration"}

// postgresSQLWriteOrder never picks a read_only binding: writes would fail.
var postgresSQLWriteOrder = []string{"read_write", "migration", "session"}

type postgresSQLColumn struct {
	Name string `json:"name"`
	Type string `json:"type"`
}

type postgresSQLResult struct {
	Command   string              `json:"command"`
	Columns   []postgresSQLColumn `json:"columns"`
	Rows      []map[string]any    `json:"rows"`
	RowCount  int                 `json:"row_count"`
	Truncated bool                `json:"truncated"`

	// cells keeps column order and NULLs for the human table.
	cells [][]*string
}

type postgresSQLOutput struct {
	Database   string              `json:"database"`
	App        string              `json:"app"`
	Binding    string              `json:"binding"`
	Access     string              `json:"access"`
	ReadOnly   bool                `json:"read_only"`
	MaxRows    int                 `json:"max_rows"`
	DurationMS int64               `json:"duration_ms"`
	Results    []postgresSQLResult `json:"results"`
}

type postgresSQLOptions struct {
	write   bool
	maxRows int
	timeout time.Duration
}

func cmdPostgresSQL(args []string) int {
	fs := newFlagSet("postgres-sql", flag.ContinueOnError)
	appSlug := fs.String("app", "", "bound app whose credentials to use")
	envKey := fs.String("env", "", "binding environment key (default: the chosen binding's)")
	access := fs.String("access", "", "binding access mode to use")
	file := fs.String("file", "", "read the query from PATH, or - for stdin")
	write := fs.Bool("write", false, "allow writes: run the text as a script in autocommit mode")
	readOnly := fs.Bool("read-only", true, "refuse writes (the default)")
	maxRows := fs.Int("max-rows", defaultPostgresSQLMaxRows, "rows kept per result (1..10000)")
	timeout := fs.Duration("timeout", defaultPostgresSQLTimeout, "statement timeout")
	waitTimeout := fs.Duration("wait-timeout", appExecInteractiveStartupWait, "maximum time to wait for each task VM to start")
	flags, positionals := splitArgsForFlags(args, "write", "read-only")
	if err := fs.Parse(flags); err != nil || len(positionals) != 1 && len(positionals) != 2 {
		PrintUsage(osStderr, postgresSQLUsage, "postgres")
		return 1
	}
	readOnlySet := false
	fs.Visit(func(f *flag.Flag) { readOnlySet = readOnlySet || f.Name == "read-only" })
	switch {
	case *write && readOnlySet && *readOnly:
		return printErr("Invalid flags", errors.New("--write and --read-only conflict"))
	case readOnlySet && !*readOnly:
		*write = true
	}
	if *maxRows < 1 || *maxRows > maxPostgresSQLMaxRows {
		return printErr("Invalid flags", fmt.Errorf("--max-rows must be between 1 and %d", maxPostgresSQLMaxRows))
	}
	if *timeout <= 0 {
		return printErr("Invalid flags", errors.New("--timeout must be positive"))
	}
	order := postgresSQLReadOnlyOrder
	if *write {
		order = postgresSQLWriteOrder
	}
	if *access != "" && !slices.Contains(order, *access) {
		return printErr("Invalid flags", fmt.Errorf("--access must be one of %s", strings.Join(order, ", ")))
	}
	query, err := postgresSQLQueryText(positionals[1:], *file, os.Stdin)
	if err != nil {
		return printErr("No query", err)
	}

	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	databaseID, err := postgresDatabaseID(ctx, client, positionals[0])
	if err != nil {
		return printErr("Could not find managed PostgreSQL database", err)
	}
	bindings, err := client.ListManagedPostgresBindings(ctx, databaseID)
	if err != nil {
		return printErr("Could not list PostgreSQL bindings", err)
	}
	apps, err := client.ListApps(ctx)
	if err != nil {
		return printErr("Could not list apps", err)
	}
	binding, slug, err := choosePostgresBinding(bindings.Items, apps, *appSlug, *envKey, *access, order)
	if err != nil {
		return printErr("No usable binding", fmt.Errorf("%w; attach one with `gregale postgres attach %s <app>`", err, positionals[0]))
	}
	key := binding.EnvironmentKey
	if !jsonOutput {
		PrintProgress(osStderr, "Using %s's %s binding (%s)", slug, key, binding.Access)
	}
	dsn, err := readAppTaskEnv(ctx, client, slug, key, *waitTimeout)
	if err != nil {
		return printErr("Could not read the binding from "+slug, err)
	}
	target, err := parsePostgresConnectTarget(dsn)
	if err != nil {
		return printErr("Unusable binding value", err)
	}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return printErr("Could not listen locally", err)
	}
	defer func() { _ = ln.Close() }()
	session, closeSession, err := openAppTaskStreamSession(ctx, client, slug, []string{api.AppTaskPortForwardCommand, target.hostPort}, *waitTimeout)
	if err != nil {
		return printErr("Could not start the tunnel", err)
	}
	defer closeSession()
	tunnelCtx, cancelTunnel := context.WithCancel(context.Background())
	tunnelDone := make(chan struct{})
	go func() {
		defer close(tunnelDone)
		_ = forwardAppTaskSession(tunnelCtx, ln, session, session.stdout, closeSession)
	}()
	defer func() {
		cancelTunnel()
		_ = ln.Close()
		_ = session.CloseStdin()
		<-tunnelDone
	}()

	opts := postgresSQLOptions{write: *write, maxRows: *maxRows, timeout: *timeout}
	hostAddr, localPort := tunnelEndpoint(ln.Addr())
	config, err := target.pgxConfig(hostAddr, localPort, opts)
	if err != nil {
		return printErr("Unusable binding value", err)
	}
	started := time.Now()
	results, err := runPostgresSQLOverConfig(ctx, config, query, opts)
	if err != nil {
		if ctx.Err() != nil {
			return printErr("Query cancelled", ctx.Err())
		}
		return printErr("Query failed", postgresSQLError(err, opts))
	}
	out := postgresSQLOutput{
		Database:   positionals[0],
		App:        slug,
		Binding:    key,
		Access:     binding.Access,
		ReadOnly:   !opts.write,
		MaxRows:    opts.maxRows,
		DurationMS: time.Since(started).Milliseconds(),
		Results:    results,
	}
	if jsonOutput {
		return jsonOut(writeJSON(out))
	}
	renderPostgresSQLResults(osStdout, out)
	return 0
}

// postgresSQLQueryText takes the query from the positional or --file (- is
// stdin), never both.
func postgresSQLQueryText(positional []string, file string, stdin io.Reader) (string, error) {
	var raw []byte
	switch {
	case len(positional) == 1 && file != "":
		return "", errors.New("pass the query as an argument or with --file, not both")
	case len(positional) == 1:
		raw = []byte(positional[0])
	case file == "-":
		data, err := io.ReadAll(io.LimitReader(stdin, postgresSQLQueryLimit+1))
		if err != nil {
			return "", err
		}
		raw = data
	case file != "":
		f, err := openCustomerFile(file)
		if err != nil {
			return "", err
		}
		defer func() { _ = f.Close() }()
		data, err := io.ReadAll(io.LimitReader(f, postgresSQLQueryLimit+1))
		if err != nil {
			return "", err
		}
		raw = data
	default:
		return "", errors.New("pass a query, e.g. `gregale postgres query orders \"select count(*) from orders\"`, or --file PATH")
	}
	if len(raw) > postgresSQLQueryLimit {
		return "", fmt.Errorf("the query is larger than %d bytes", postgresSQLQueryLimit)
	}
	query := strings.TrimSpace(string(raw))
	if query == "" {
		return "", errors.New("the query is empty")
	}
	return query, nil
}

// pgxConfig points pgx at the tunnel while keeping the binding's host for
// certificate verification and SNI, as libpqEnv does for psql.
func (t postgresConnectTarget) pgxConfig(hostAddr string, localPort int, opts postgresSQLOptions) (*pgx.ConnConfig, error) {
	// Only what psql gets from libpqEnv, plus TLS files: pgx sends any
	// parameter it does not know to the server as a startup parameter.
	params := url.Values{}
	for _, name := range postgresSQLForwardedParams {
		if values, ok := t.params[name]; ok {
			params[name] = slices.Clone(values)
		}
	}
	params.Set("sslmode", t.sslMode)
	params.Set("application_name", "gregale-postgres-sql")
	params.Set("connect_timeout", "15")
	u := url.URL{
		Scheme: "postgres",
		User:   url.UserPassword(t.user, t.password),
		Host:   net.JoinHostPort(t.host, strconv.Itoa(localPort)),
		Path:   "/" + t.database,
		// Encode writes a space as "+", which pgx keeps literally; a literal
		// "+" is already %2B, so the swap is exact.
		RawQuery: strings.ReplaceAll(params.Encode(), "+", "%20"),
	}
	config, err := pgx.ParseConfig(u.String())
	if err != nil {
		// pgx errors may quote the URL, which carries the password.
		return nil, errors.New("the binding does not parse as a connection string")
	}
	config.LookupFunc = func(context.Context, string) ([]string, error) { return []string{hostAddr}, nil }
	preparePostgresSQLConfig(config)
	return config, nil
}

// postgresSQLForwardedParams are the binding URL parameters passed on.
var postgresSQLForwardedParams = []string{"options", "sslrootcert", "sslcert", "sslkey", "sslpassword"}

// preparePostgresSQLConfig keeps the session pooler-safe: an unnamed
// statement (no prepared-statement cache) and no statement_timeout startup
// parameter, which transaction-mode poolers refuse. A cancelled context
// sends a cancel request, so a client-side timeout stops the server query.
func preparePostgresSQLConfig(config *pgx.ConnConfig) {
	config.DefaultQueryExecMode = pgx.QueryExecModeDescribeExec
	config.StatementCacheCapacity = 0
	config.DescriptionCacheCapacity = 0
	config.BuildContextWatcherHandler = func(conn *pgconn.PgConn) ctxwatch.Handler {
		return &pgconn.CancelRequestContextWatcherHandler{Conn: conn, DeadlineDelay: 5 * time.Second}
	}
}

// postgresSQLTimeoutStatement is the per-transaction statement timeout.
func postgresSQLTimeoutStatement(opts postgresSQLOptions) string {
	return "SET LOCAL statement_timeout = " + strconv.FormatInt(opts.timeout.Milliseconds(), 10)
}

func runPostgresSQLOverConfig(ctx context.Context, config *pgx.ConnConfig, query string, opts postgresSQLOptions) ([]postgresSQLResult, error) {
	connectCtx, cancelConnect := context.WithTimeout(ctx, 30*time.Second)
	defer cancelConnect()
	conn, err := pgx.ConnectConfig(connectCtx, config)
	if err != nil {
		return nil, fmt.Errorf("connect: %w", redactPostgresConnectError(err))
	}
	defer func() {
		closeCtx, closeCancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer closeCancel()
		_ = conn.Close(closeCtx)
	}()
	return runPostgresSQL(ctx, conn, query, opts)
}

// runPostgresSQL runs query on conn. Read-only runs exactly one statement in
// a READ ONLY transaction that is always rolled back, with a server-side
// statement timeout. The extended protocol refuses a second statement, so
// the query cannot end the transaction and keep going; that, the rollback
// and the READ ONLY mode are what keep it from writing.
//
// Write sends the text unchanged as a simple-protocol script, so a lone
// statement that refuses a transaction block (VACUUM, CREATE INDEX
// CONCURRENTLY) still runs. Its timeout is client-side: the context
// deadline sends a cancel request. Rows past --max-rows are drained, not
// kept.
func runPostgresSQL(ctx context.Context, conn *pgx.Conn, query string, opts postgresSQLOptions) ([]postgresSQLResult, error) {
	typeMap := conn.TypeMap()
	if opts.write {
		writeCtx, cancel := context.WithTimeout(ctx, opts.timeout)
		defer cancel()
		multi := conn.PgConn().Exec(writeCtx, query)
		var out []postgresSQLResult
		for multi.NextResult() {
			reader := multi.ResultReader()
			fields := slices.Clone(reader.FieldDescriptions())
			var raw [][][]byte
			truncated := false
			for reader.NextRow() {
				if len(raw) == opts.maxRows {
					truncated = true
					continue
				}
				raw = append(raw, clonePostgresSQLRow(reader.Values()))
			}
			tag, err := reader.Close()
			if err != nil {
				_ = multi.Close()
				return nil, postgresSQLDeadline(writeCtx, err)
			}
			out = append(out, newPostgresSQLResult(typeMap, fields, raw, tag.String(), truncated))
		}
		if err := multi.Close(); err != nil {
			return nil, postgresSQLDeadline(writeCtx, err)
		}
		return out, nil
	}

	queryCtx, cancel := context.WithTimeout(ctx, opts.timeout+30*time.Second)
	defer cancel()
	ctx = queryCtx
	tx, err := conn.BeginTx(ctx, pgx.TxOptions{AccessMode: pgx.ReadOnly})
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	if _, err := tx.Exec(ctx, postgresSQLTimeoutStatement(opts), pgx.QueryExecModeSimpleProtocol); err != nil {
		return nil, err
	}
	rows, err := tx.Query(ctx, query, pgx.QueryResultFormats{pgx.TextFormatCode})
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var raw [][][]byte
	truncated := false
	for rows.Next() {
		if len(raw) == opts.maxRows {
			truncated = true
			break
		}
		raw = append(raw, clonePostgresSQLRow(rows.RawValues()))
	}
	fields := rows.FieldDescriptions()
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	// Close drained the rest, so the tag counts every row, kept or not.
	return []postgresSQLResult{newPostgresSQLResult(typeMap, fields, raw, rows.CommandTag().String(), truncated)}, nil
}

// clonePostgresSQLRow copies a row out of the reader's reused buffer.
func clonePostgresSQLRow(values [][]byte) [][]byte {
	row := make([][]byte, len(values))
	for i, value := range values {
		if value != nil {
			row[i] = slices.Clone(value)
		}
	}
	return row
}

// postgresSQLDeadline reports a client-side --write timeout as one.
func postgresSQLDeadline(ctx context.Context, err error) error {
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return fmt.Errorf("%w: %w", errPostgresSQLTimeout, err)
	}
	return err
}

var errPostgresSQLTimeout = errors.New("statement timeout")

func newPostgresSQLResult(typeMap *pgtype.Map, fields []pgconn.FieldDescription, raw [][][]byte, command string, truncated bool) postgresSQLResult {
	result := postgresSQLResult{
		Command:   command,
		Columns:   make([]postgresSQLColumn, len(fields)),
		Rows:      make([]map[string]any, 0, len(raw)),
		RowCount:  len(raw),
		Truncated: truncated,
	}
	keys := postgresSQLColumnKeys(fields)
	for i, field := range fields {
		result.Columns[i] = postgresSQLColumn{Name: field.Name, Type: postgresSQLTypeName(typeMap, field.DataTypeOID)}
	}
	for _, values := range raw {
		row := make(map[string]any, len(fields))
		cells := make([]*string, len(fields))
		for i, field := range fields {
			var value []byte
			if i < len(values) {
				value = values[i]
			}
			row[keys[i]] = postgresSQLJSONValue(field.DataTypeOID, value)
			if value != nil {
				text := string(value)
				cells[i] = &text
			}
		}
		result.Rows = append(result.Rows, row)
		result.cells = append(result.cells, cells)
	}
	return result
}

// postgresSQLColumnKeys names each column's JSON key; a repeated name (two
// `count` columns) gets a _2, _3 suffix instead of overwriting.
func postgresSQLColumnKeys(fields []pgconn.FieldDescription) []string {
	keys := make([]string, len(fields))
	seen := make(map[string]int, len(fields))
	for i, field := range fields {
		name := field.Name
		seen[name]++
		for seen[name] > 1 {
			candidate := name + "_" + strconv.Itoa(seen[name])
			if _, taken := seen[candidate]; !taken {
				name = candidate
				seen[name] = 1
				break
			}
			seen[name]++
		}
		keys[i] = name
	}
	return keys
}

func postgresSQLTypeName(typeMap *pgtype.Map, oid uint32) string {
	if typ, ok := typeMap.TypeForOID(oid); ok {
		return typ.Name
	}
	return "oid:" + strconv.FormatUint(uint64(oid), 10)
}

// postgresSQLJSONValue converts one text-format value: numbers stay numbers
// (exact, as json.Number), booleans become booleans, json and jsonb are
// embedded, and everything else is its PostgreSQL text form.
func postgresSQLJSONValue(oid uint32, value []byte) any {
	if value == nil {
		return nil
	}
	text := string(value)
	switch oid {
	case pgtype.BoolOID:
		return text == "t"
	case pgtype.Int2OID, pgtype.Int4OID, pgtype.Int8OID, pgtype.OIDOID, pgtype.Float4OID, pgtype.Float8OID, pgtype.NumericOID:
		if json.Valid(value) {
			return json.Number(text)
		}
		return text // NaN, Infinity
	case pgtype.JSONOID, pgtype.JSONBOID:
		if json.Valid(value) {
			return json.RawMessage(slices.Clone(value))
		}
	}
	return text
}

// postgresSQLError adds the next step to the errors an agent will hit.
func postgresSQLError(err error, opts postgresSQLOptions) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		switch {
		case pgErr.Code == "25006" && !opts.write:
			return fmt.Errorf("%w; the statement writes, and this command is read-only unless you pass --write", err)
		case pgErr.Code == "42601" && !opts.write && strings.Contains(pgErr.Message, "multiple commands"):
			return fmt.Errorf("%w; read-only mode runs one statement: split them, or pass --write to run a script", err)
		case pgErr.Code == "57014" && !opts.write:
			return fmt.Errorf("%w; raise --timeout (now %s) if the query is expected to run longer", err, opts.timeout)
		}
	}
	if errors.Is(err, errPostgresSQLTimeout) {
		return fmt.Errorf("%w; raise --timeout (now %s) if the script is expected to run longer", err, opts.timeout)
	}
	return err
}

// redactPostgresConnectError keeps pgx connect errors from echoing the
// connection string, which carries the password.
func redactPostgresConnectError(err error) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return pgErr
	}
	var connectErr *pgconn.ConnectError
	if errors.As(err, &connectErr) && connectErr.Unwrap() != nil {
		return connectErr.Unwrap()
	}
	return err
}

func renderPostgresSQLResults(w io.Writer, out postgresSQLOutput) {
	for i, result := range out.Results {
		if i > 0 {
			_, _ = fmt.Fprintln(w)
		}
		if len(result.Columns) == 0 {
			_, _ = fmt.Fprintln(w, result.Command)
			continue
		}
		tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
		names := make([]string, len(result.Columns))
		for j, column := range result.Columns {
			names[j] = column.Name
		}
		_, _ = fmt.Fprintln(tw, strings.Join(names, "\t"))
		for _, cells := range result.cells {
			line := make([]string, len(cells))
			for j, cell := range cells {
				line[j] = postgresSQLCell(cell)
			}
			_, _ = fmt.Fprintln(tw, strings.Join(line, "\t"))
		}
		_ = tw.Flush()
		suffix := ""
		if result.Truncated {
			suffix = fmt.Sprintf("; stopped at --max-rows %d", out.MaxRows)
		}
		noun := "rows"
		if result.RowCount == 1 {
			noun = "row"
		}
		_, _ = fmt.Fprintf(w, "(%d %s%s)\n", result.RowCount, noun, suffix)
	}
	if !out.ReadOnly {
		return
	}
	_, _ = fmt.Fprintf(osStderr, "Read-only transaction, rolled back (%d ms).\n", out.DurationMS)
}

func postgresSQLCell(cell *string) string {
	if cell == nil {
		return "NULL"
	}
	text := strings.NewReplacer("\t", `\t`, "\n", `\n`, "\r", `\r`).Replace(*cell)
	if runes := []rune(text); len(runes) > postgresSQLCellLimit {
		text = string(runes[:postgresSQLCellLimit-1]) + "…"
	}
	return text
}
