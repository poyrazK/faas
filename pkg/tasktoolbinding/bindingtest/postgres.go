// Package bindingtest provides disposable PostgreSQL roles for Task catalog tests.
package bindingtest

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/tasktoolbinding"
)

// Postgres seeds a disposable database with the four Task privileges. The
// returned connector redirects only fixture connections to that database.
func Postgres(t *testing.T) ([4]string, [4]string, tasktoolbinding.Connect, func(string)) {
	t.Helper()
	boot := pgtest.Open(t)
	name := "task_binding_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	ident := func(s string) string { return pgx.Identifier{s}.Sanitize() }
	if _, err := boot.Exec(t.Context(), "CREATE DATABASE "+ident(name)); err != nil {
		t.Fatal(err)
	}
	cfg := boot.Config().Copy()
	cfg.ConnConfig.Database = name
	delete(cfg.ConnConfig.RuntimeParams, "search_path")
	admin, err := pgxpool.NewWithConfig(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	roles := [4]string{name + "_web", name + "_worker", name + "_migration", name + "_observer"}
	t.Cleanup(func() {
		admin.Close()
		ctx, cancel := context.WithTimeout(context.WithoutCancel(t.Context()), 10*time.Second)
		defer cancel()
		_, _ = boot.Exec(ctx, "DROP DATABASE "+ident(name)+" WITH (FORCE)")
		for _, role := range roles {
			_, _ = boot.Exec(ctx, "DROP ROLE "+ident(role))
		}
	})
	exec := func(sql string) {
		t.Helper()
		if _, err := admin.Exec(t.Context(), sql); err != nil {
			t.Fatal(err)
		}
	}
	for _, role := range roles {
		exec("CREATE ROLE " + ident(role) + " LOGIN PASSWORD 'binding-fixture-only'")
	}
	exec("REVOKE ALL ON DATABASE " + ident(name) + " FROM PUBLIC")
	for _, role := range roles {
		exec("GRANT CONNECT ON DATABASE " + ident(name) + " TO " + ident(role))
	}
	exec("ALTER SCHEMA public OWNER TO " + ident(roles[2]))
	exec("REVOKE ALL ON SCHEMA public FROM PUBLIC")
	for _, role := range roles {
		exec("GRANT USAGE ON SCHEMA public TO " + ident(role))
	}
	tables := []string{"gregale_mcp_tasks", "gregale_mcp_task_owners", "gregale_mcp_task_workers", "gregale_mcp_task_worker_handlers", "gregale_mcp_task_schema", "gregale_mcp_task_admission_namespaces", "gregale_mcp_task_fairness", "gregale_mcp_task_crypto_keys", "gregale_mcp_task_admission"}
	writes := map[string]bool{"gregale_mcp_tasks": true, "gregale_mcp_task_owners": true, "gregale_mcp_task_workers": true, "gregale_mcp_task_worker_handlers": true, "gregale_mcp_task_fairness": true}
	for _, table := range tables {
		exec("CREATE TABLE " + ident(table) + "(id bigint)")
		exec("ALTER TABLE " + ident(table) + " OWNER TO " + ident(roles[2]))
		for _, role := range []string{roles[0], roles[1]} {
			privilege := "SELECT"
			if writes[table] {
				privilege += ",INSERT,UPDATE,DELETE"
			}
			exec("GRANT " + privilege + " ON " + ident(table) + " TO " + ident(role))
		}
		exec("GRANT SELECT ON " + ident(table) + " TO " + ident(roles[3]))
	}
	var dsns [4]string
	for i, role := range roles {
		dsns[i] = fmt.Sprintf("postgres://%s:binding-fixture-only@fixture.example.com/task?sslmode=verify-full", role)
	}
	connect := func(ctx context.Context, c *pgx.ConnConfig) (*pgx.Conn, error) {
		local := cfg.ConnConfig.Copy()
		local.User = c.User
		local.Password = c.Password
		local.RuntimeParams = map[string]string{"statement_timeout": "5000", "lock_timeout": "5000", "row_security": "on"}
		return pgx.ConnectConfig(ctx, local)
	}
	return dsns, roles, connect, exec
}
