package state_test

import (
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

// TestSQLLiteralsPrepareAgainstMigratedSchema prepares every literal SQL
// statement the repository hands to Query/QueryRow/Exec against a fully
// migrated schema. Postgres resolves every relation and column at PREPARE
// time, so a typo'd column, a dropped table or invalid syntax fails here
// even when no test executes that statement. Two such statements shipped
// before this gate: githubd's PR-preview member query selected a column its
// subquery never returned (42703 on every call), and
// PgStore.MarkDeploymentCancelled used "$2_old" as a placeholder (42601).
func TestSQLLiteralsPrepareAgainstMigratedSchema(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	ctx := t.Context()
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatal(err)
	}
	conn, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Release()

	// ADR-430: Commit's relay SQL belongs to a customer's independent
	// database. Restore qualification also owns a separate disposable DB.
	// Platform migrations must never install either one's public tables.
	var customerTablesInPlatform bool
	if err := conn.QueryRow(ctx, `SELECT to_regclass('public.gregale_outbox') IS NOT NULL OR to_regclass('public.gregale_commit_binding') IS NOT NULL OR to_regclass('public.gregale_qualification_restore_probe') IS NOT NULL`).Scan(&customerTablesInPlatform); err != nil {
		t.Fatal(err)
	}
	if customerTablesInPlatform {
		t.Fatal("platform migrations installed customer or qualification tables")
	}
	customerPool := pgtest.OpenDatabase(t)
	customerSchema, err := os.ReadFile("../../pkg/commit/schema.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := customerPool.Exec(ctx, string(customerSchema)); err != nil {
		t.Fatal(err)
	}
	customerConn, err := customerPool.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer customerConn.Release()

	qualificationPool := pgtest.OpenDatabase(t)
	if _, err := qualificationPool.Exec(ctx, `CREATE TABLE public.gregale_qualification_restore_probe (id integer PRIMARY KEY CHECK (id = 1), marker text NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	qualificationConn, err := qualificationPool.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer qualificationConn.Release()

	stmts := literalSQLStatements(t, "../../pkg", "../../cmd")
	if len(stmts) < 500 {
		t.Fatalf("found only %d SQL literals; the source walk is broken", len(stmts))
	}
	// sqlc validates queries.sql against schema.sql, which is maintained by
	// hand and drifts from a clean migration replay; prepare its generated
	// statements against the real migrated schema too.
	sqlcStmts := sqlcQueryConstants(t, "sqlc/queries.sql.go")
	if len(sqlcStmts) < 100 {
		t.Fatalf("found only %d sqlc query constants; the parse is broken", len(sqlcStmts))
	}
	stmts = append(stmts, sqlcStmts...)
	// Only errors that mean the statement can never run.
	definitive := map[string]bool{
		"42703": true, // undefined_column
		"42P01": true, // undefined_table
		"42883": true, // undefined_function
		"42601": true, // syntax_error
		"42702": true, // ambiguous_column
		"42P10": true, // invalid_column_reference
		"42P08": true, // ambiguous_parameter / inconsistent parameter types
	}
	var customerStatements, qualificationStatements int
	for i, s := range stmts {
		name := fmt.Sprintf("sql_gate_%d", i)
		target := conn
		query := strings.ReplaceAll(s.sql, "public.", "")
		pos := filepath.ToSlash(s.pos)
		if strings.HasPrefix(pos, "../../pkg/commit/") || strings.HasPrefix(pos, "../../pkg/commitmanaged/") {
			target = customerConn
			query = s.sql
			customerStatements++
		} else if strings.HasPrefix(pos, "../../pkg/managedpostgres/neon/restore_probe.go:") {
			target = qualificationConn
			query = s.sql
			qualificationStatements++
		}
		// pgtest isolates each test in its own schema, which stands in for
		// public; a few statements qualify public.<table> deliberately.
		// Customer and qualification SQL keep public qualification in their own DBs.
		_, err := target.Conn().Prepare(ctx, name, query)
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && definitive[pgErr.Code] {
			t.Errorf("%s: %s %s\n%s", s.pos, pgErr.Code, pgErr.Message, s.sql)
			continue
		}
		if err == nil {
			_ = target.Conn().Deallocate(ctx, name)
		}
	}
	if customerStatements == 0 {
		t.Fatal("customer Commit SQL was absent from the source walk")
	}
	if qualificationStatements == 0 {
		t.Fatal("restore qualification SQL was absent from the source walk")
	}
	t.Logf("validated %d platform, %d customer and %d qualification SQL literals", len(stmts)-customerStatements-qualificationStatements, customerStatements, qualificationStatements)
}

type sqlLiteral struct {
	pos string
	sql string
}

// literalSQLStatements collects string-literal (or literal-concatenation)
// arguments of .Query/.QueryRow/.Exec calls that start with a DML keyword.
// fmt-built statements are skipped: their text is not known statically.
func literalSQLStatements(t *testing.T, roots ...string) []sqlLiteral {
	t.Helper()
	fset := token.NewFileSet()
	var out []sqlLiteral
	for _, root := range roots {
		err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			src, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			f, err := parser.ParseFile(fset, path, src, 0)
			if err != nil {
				return fmt.Errorf("parse %s: %w", path, err)
			}
			ast.Inspect(f, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok {
					return true
				}
				sel, ok := call.Fun.(*ast.SelectorExpr)
				if !ok || (sel.Sel.Name != "Query" && sel.Sel.Name != "QueryRow" && sel.Sel.Name != "Exec") {
					return true
				}
				for i, arg := range call.Args {
					if i > 1 {
						break
					}
					s, ok := foldStringLiteral(arg)
					if !ok {
						continue
					}
					low := strings.ToLower(strings.TrimSpace(s))
					for _, kw := range []string{"select", "insert", "update", "delete", "with"} {
						if strings.HasPrefix(low, kw) && !strings.Contains(s, "%s") {
							out = append(out, sqlLiteral{pos: fset.Position(call.Pos()).String(), sql: s})
							break
						}
					}
					break
				}
				return true
			})
			return nil
		})
		if err != nil {
			t.Fatalf("walk %s: %v", root, err)
		}
	}
	return out
}

// sqlcQueryConstants returns every top-level string constant in sqlc's
// generated query file.
func sqlcQueryConstants(t *testing.T, path string) []sqlLiteral {
	t.Helper()
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, path, nil, 0)
	if err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	var out []sqlLiteral
	for _, decl := range f.Decls {
		gen, ok := decl.(*ast.GenDecl)
		if !ok || gen.Tok != token.CONST {
			continue
		}
		for _, spec := range gen.Specs {
			vs, ok := spec.(*ast.ValueSpec)
			if !ok || len(vs.Values) != 1 {
				continue
			}
			if s, ok := foldStringLiteral(vs.Values[0]); ok {
				out = append(out, sqlLiteral{pos: fset.Position(vs.Pos()).String(), sql: s})
			}
		}
	}
	return out
}

func foldStringLiteral(e ast.Expr) (string, bool) {
	switch x := e.(type) {
	case *ast.BasicLit:
		if x.Kind != token.STRING {
			return "", false
		}
		s, err := strconv.Unquote(x.Value)
		return s, err == nil
	case *ast.BinaryExpr:
		if x.Op != token.ADD {
			return "", false
		}
		a, ok1 := foldStringLiteral(x.X)
		b, ok2 := foldStringLiteral(x.Y)
		return a + b, ok1 && ok2
	case *ast.ParenExpr:
		return foldStringLiteral(x.X)
	}
	return "", false
}
