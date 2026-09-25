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

	stmts := literalSQLStatements(t, "../../pkg", "../../cmd")
	if len(stmts) < 500 {
		t.Fatalf("found only %d SQL literals; the source walk is broken", len(stmts))
	}
	// Only errors that mean the statement can never run.
	definitive := map[string]bool{
		"42703": true, // undefined_column
		"42P01": true, // undefined_table
		"42883": true, // undefined_function
		"42601": true, // syntax_error
		"42702": true, // ambiguous_column
		"42P10": true, // invalid_column_reference
	}
	for i, s := range stmts {
		name := fmt.Sprintf("sql_gate_%d", i)
		// pgtest isolates each test in its own schema, which stands in for
		// public; a few statements qualify public.<table> deliberately.
		_, err := conn.Conn().Prepare(ctx, name, strings.ReplaceAll(s.sql, "public.", ""))
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && definitive[pgErr.Code] {
			t.Errorf("%s: %s %s\n%s", s.pos, pgErr.Code, pgErr.Message, s.sql)
			continue
		}
		if err == nil {
			_ = conn.Conn().Deallocate(ctx, name)
		}
	}
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
