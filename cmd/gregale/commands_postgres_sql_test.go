package main_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
)

func TestPostgresSQLQueryText(t *testing.T) {
	if got, err := main.PostgresSQLQueryText([]string{"  select 1  "}, "", nil); err != nil || got != "select 1" {
		t.Fatalf("positional = %q, %v", got, err)
	}
	if got, err := main.PostgresSQLQueryText(nil, "-", strings.NewReader("select 2\n")); err != nil || got != "select 2" {
		t.Fatalf("stdin = %q, %v", got, err)
	}
	path := t.TempDir() + "/q.sql"
	if err := os.WriteFile(path, []byte("select 3;"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got, err := main.PostgresSQLQueryText(nil, path, nil); err != nil || got != "select 3;" {
		t.Fatalf("file = %q, %v", got, err)
	}
	for name, call := range map[string]func() (string, error){
		"both":  func() (string, error) { return main.PostgresSQLQueryText([]string{"select 1"}, path, nil) },
		"none":  func() (string, error) { return main.PostgresSQLQueryText(nil, "", nil) },
		"empty": func() (string, error) { return main.PostgresSQLQueryText([]string{"  "}, "", nil) },
		"huge": func() (string, error) {
			return main.PostgresSQLQueryText(nil, "-", bytes.NewReader(make([]byte, main.PostgresSQLQueryLimit+1)))
		},
	} {
		if _, err := call(); err == nil {
			t.Errorf("%s: no error", name)
		}
	}
}

func TestPostgresSQLBindingOrder(t *testing.T) {
	apps := []api.AppResponse{{ID: "a1", Slug: "api"}}
	bindings := []api.ManagedPostgresBinding{
		{AppID: "a1", EnvironmentKey: "DATABASE_URL", Access: "read_write", State: "ready"},
		{AppID: "a1", EnvironmentKey: "DATABASE_RO_URL", Access: "read_only", State: "ready"},
	}
	got, err := main.ChoosePostgresBinding(bindings, apps, main.PostgresSQLReadOnlyOrder)
	if err != nil || got.Access != "read_only" {
		t.Fatalf("read-only picked %+v, %v", got, err)
	}
	got, err = main.ChoosePostgresBinding(bindings, apps, main.PostgresSQLWriteOrder)
	if err != nil || got.Access != "read_write" {
		t.Fatalf("write picked %+v, %v", got, err)
	}
	if _, err := main.ChoosePostgresBinding(bindings[1:], apps, main.PostgresSQLWriteOrder); err == nil {
		t.Fatal("write accepted a read_only binding")
	}
	// connect keeps its own preference.
	got, err = main.ChoosePostgresConnectBindingDefault(bindings, apps)
	if err != nil || got.Access != "read_write" {
		t.Fatalf("connect picked %+v, %v", got, err)
	}
}

func TestPostgresSQLJSONValue(t *testing.T) {
	cases := []struct {
		oid   uint32
		value []byte
		want  string
	}{
		{pgtype.Int8OID, []byte("9007199254740993"), `9007199254740993`},
		{pgtype.NumericOID, []byte("12.50"), `12.50`},
		{pgtype.Float8OID, []byte("NaN"), `"NaN"`},
		{pgtype.BoolOID, []byte("t"), `true`},
		{pgtype.BoolOID, []byte("f"), `false`},
		{pgtype.JSONBOID, []byte(`{"a": [1, 2]}`), `{"a":[1,2]}`},
		{pgtype.TimestamptzOID, []byte("2026-11-22 10:00:00+00"), `"2026-11-22 10:00:00+00"`},
		{pgtype.TextOID, nil, `null`},
	}
	for _, tc := range cases {
		encoded, err := json.Marshal(main.PostgresSQLJSONValue(tc.oid, tc.value))
		if err != nil || string(encoded) != tc.want {
			t.Errorf("oid %d %q = %s, %v; want %s", tc.oid, tc.value, encoded, err, tc.want)
		}
	}
}

func TestPostgresSQLColumnKeys(t *testing.T) {
	fields := []pgconn.FieldDescription{{Name: "count"}, {Name: "count"}, {Name: "count_2"}, {Name: "id"}}
	keys := main.PostgresSQLColumnKeys(fields)
	if keys[0] != "count" || keys[3] != "id" {
		t.Fatalf("keys = %v", keys)
	}
	seen := map[string]bool{}
	for _, key := range keys {
		if seen[key] {
			t.Fatalf("duplicate key %q in %v", key, keys)
		}
		seen[key] = true
	}
}

func TestPostgresSQLConfigDialsTheTunnel(t *testing.T) {
	config, err := main.PostgresSQLConfig("postgres://app:s3cr%40t@ep-x.db.example:5432/orders?sslmode=require&options=-c%20search_path%3Dapp&pool_max_conns=5", "127.0.0.1", 54001)
	if err != nil {
		t.Fatal(err)
	}
	if config.Host != "ep-x.db.example" || config.Port != 54001 || config.Password != "s3cr@t" || config.Database != "orders" {
		t.Fatalf("config host=%s port=%d db=%s", config.Host, config.Port, config.Database)
	}
	if config.TLSConfig == nil || config.TLSConfig.ServerName != "ep-x.db.example" {
		t.Fatalf("TLS server name = %+v", config.TLSConfig)
	}
	addrs, err := config.LookupFunc(context.Background(), config.Host)
	if err != nil || len(addrs) != 1 || addrs[0] != "127.0.0.1" {
		t.Fatalf("lookup = %v, %v", addrs, err)
	}
	if config.DefaultQueryExecMode != pgx.QueryExecModeDescribeExec || config.StatementCacheCapacity != 0 {
		t.Fatalf("exec mode = %v cache = %d", config.DefaultQueryExecMode, config.StatementCacheCapacity)
	}
	if config.RuntimeParams["options"] != "-c search_path=app" {
		t.Fatalf("options = %q", config.RuntimeParams["options"])
	}
	for name := range config.RuntimeParams {
		if name != "options" && name != "application_name" {
			t.Errorf("unexpected startup parameter %q (poolers refuse unknown ones)", name)
		}
	}
}

func TestPostgresSQLErrorHints(t *testing.T) {
	for _, tc := range []struct {
		err   error
		write bool
		want  string
	}{
		{&pgconn.PgError{Code: "25006", Message: "cannot execute INSERT in a read-only transaction"}, false, "pass --write"},
		{&pgconn.PgError{Code: "42601", Message: "cannot insert multiple commands into a prepared statement"}, false, "runs one statement"},
		{&pgconn.PgError{Code: "57014", Message: "canceling statement due to statement timeout"}, false, "raise --timeout (now 30s)"},
	} {
		if got := main.PostgresSQLError(tc.err, tc.write, 30*time.Second).Error(); !strings.Contains(got, tc.want) {
			t.Errorf("%v: %q missing %q", tc.err, got, tc.want)
		}
	}
	other := errors.New("boom")
	if got := main.PostgresSQLError(other, false, time.Second); !errors.Is(got, other) || got.Error() != other.Error() {
		t.Fatal("unrelated error rewritten")
	}
}

func TestRenderPostgresSQLResults(t *testing.T) {
	fields := []pgconn.FieldDescription{{Name: "id", DataTypeOID: pgtype.Int4OID}, {Name: "note", DataTypeOID: pgtype.TextOID}}
	result := main.NewPostgresSQLResult(pgtype.NewMap(), fields, [][][]byte{{[]byte("1"), []byte("a\tb\nc")}, {[]byte("2"), nil}}, "SELECT 5", true)
	var out bytes.Buffer
	main.RenderPostgresSQLResults(&out, main.PostgresSQLOutput{MaxRows: 2, Results: []main.PostgresSQLResult{result, {Command: "UPDATE 3"}}})
	got := out.String()
	for _, want := range []string{"id  note", `1   a\tb\nc`, "2   NULL", "(2 rows; stopped at --max-rows 2)", "UPDATE 3"} {
		if !strings.Contains(got, want) {
			t.Errorf("output missing %q:\n%s", want, got)
		}
	}
	if result.Columns[0].Type != "int4" || result.Rows[1]["note"] != nil {
		t.Fatalf("columns=%+v rows=%+v", result.Columns, result.Rows)
	}
}

// TestRunPostgresSQLAgainstPostgres needs a scratch database: DATABASE_URL.
func TestRunPostgresSQLAgainstPostgres(t *testing.T) {
	dsn := os.Getenv("DATABASE_URL")
	if testing.Short() || dsn == "" || os.Getenv("FAAS_SKIP_PG_TESTS") != "" {
		t.Skip("set DATABASE_URL to run against PostgreSQL")
	}
	ctx := context.Background()
	config, err := pgx.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	main.PreparePostgresSQLConfig(config)
	conn, err := pgx.ConnectConfig(ctx, config)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close(ctx) }()
	table := "gregale_sql_test"
	if _, err := conn.Exec(ctx, "DROP TABLE IF EXISTS "+table+"; CREATE TABLE "+table+" (id int primary key, doc jsonb)"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = conn.Exec(context.Background(), "DROP TABLE IF EXISTS "+table) })
	read := func(query string, maxRows int) ([]main.PostgresSQLResult, error) {
		return main.RunPostgresSQL(ctx, conn, query, false, maxRows, 5*time.Second)
	}
	write := func(query string, maxRows int) ([]main.PostgresSQLResult, error) {
		return main.RunPostgresSQL(ctx, conn, query, true, maxRows, 5*time.Second)
	}

	results, err := write("insert into "+table+" select g, jsonb_build_object('g', g) from generate_series(1, 3) g; select count(*) from "+table, 100)
	if err != nil || len(results) != 2 || results[0].Command != "INSERT 0 3" || results[1].Rows[0]["count"] != json.Number("3") {
		t.Fatalf("write script = %+v, %v", results, err)
	}
	// A lone statement that refuses a transaction block still runs.
	if _, err := write("create index concurrently "+table+"_doc on "+table+" ((doc->>'g'))", 100); err != nil {
		t.Fatalf("create index concurrently: %v", err)
	}
	// --write keeps --max-rows rows but drains the rest.
	results, err = write("select g from generate_series(1, 1000) g; select 1 as after", 10)
	if err != nil || len(results) != 2 || results[0].RowCount != 10 || !results[0].Truncated || results[0].Command != "SELECT 1000" || results[1].RowCount != 1 {
		t.Fatalf("write truncation = %+v, %v", results, err)
	}

	results, err = read("select id, doc from "+table+" order by id", 2)
	if err != nil || len(results) != 1 || !results[0].Truncated || results[0].RowCount != 2 || results[0].Command != "SELECT 3" {
		t.Fatalf("truncated select = %+v, %v", results, err)
	}
	if doc, _ := json.Marshal(results[0].Rows[0]["doc"]); string(doc) != `{"g":1}` {
		t.Fatalf("doc = %s", doc)
	}

	for name, query := range map[string]string{
		"insert":          "insert into " + table + " values (9, null)",
		"cte delete":      "with d as (delete from " + table + " returning 1) select count(*) from d",
		"two statements":  "select 1; delete from " + table,
		"read-write flip": "select set_config('transaction_read_only', 'off', true)",
	} {
		if _, err := read(query, 10); err == nil {
			t.Errorf("%s: read-only mode ran %q", name, query)
		}
	}
	// Statements that may succeed but must not let a later write through.
	for _, query := range []string{"commit", "set transaction read write", "set session characteristics as transaction read write"} {
		_, _ = read(query, 10)
		if _, err := read("insert into "+table+" values (10, null)", 10); err == nil {
			t.Fatalf("write succeeded after %q", query)
		}
	}
	results, err = read("select count(*) from "+table, 10)
	if err != nil || results[0].Rows[0]["count"] != json.Number("3") {
		t.Fatalf("table changed: %+v, %v", results, err)
	}

	if _, err := main.RunPostgresSQL(ctx, conn, "select pg_sleep(2)", false, 1, 200*time.Millisecond); err == nil {
		t.Fatal("read statement timeout not applied")
	} else if !strings.Contains(main.PostgresSQLError(err, false, 200*time.Millisecond).Error(), "raise --timeout") {
		t.Fatalf("read timeout error = %v", err)
	}
	if _, err := main.RunPostgresSQL(ctx, conn, "select pg_sleep(3)", true, 1, 200*time.Millisecond); err == nil {
		t.Fatal("write timeout not applied")
	} else if !strings.Contains(main.PostgresSQLError(err, true, 200*time.Millisecond).Error(), "raise --timeout") {
		t.Fatalf("write timeout error = %v", err)
	}
}
