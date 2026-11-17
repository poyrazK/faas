package tasktoolbinding_test

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

// The trusted connection seam redirects only tests to an owned private database.
// Production still requires public destinations, verified TLS and fixed SQL.
func TestHostedTaskBindingPostgresRoles(t *testing.T) {
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
	check := func(index int, want string) {
		t.Helper()
		got, err := tasktoolbinding.Probe(t.Context(), dsns, connect)
		if err != nil || len(got) != 4 || got[index].Status != want {
			t.Fatalf("probe %d want %s: %+v %v", index, want, got, err)
		}
	}
	for i := range 4 {
		check(i, "passed")
	}
	// Explicitly putting pg_catalog last allows tenant types/operators to shadow
	// builtins. Catalog probes must never evaluate this user-defined domain check.
	exec("CREATE FUNCTION public.binding_shadow_text(pg_catalog.text) RETURNS boolean LANGUAGE plpgsql AS 'BEGIN RAISE EXCEPTION ''tenant text cast executed''; END'")
	exec("CREATE DOMAIN public.text AS pg_catalog.text CHECK(public.binding_shadow_text(VALUE))")
	for _, role := range roles {
		exec("ALTER ROLE " + ident(role) + " SET search_path=public,pg_catalog")
	}
	for i := range 4 {
		check(i, "passed")
	}

	exec("GRANT INSERT ON gregale_mcp_tasks TO " + ident(roles[3]))
	check(3, "failed")
	exec("REVOKE INSERT ON gregale_mcp_tasks FROM " + ident(roles[3]))
	exec("GRANT CREATE ON SCHEMA public TO " + ident(roles[0]))
	check(1, "failed")
	exec("REVOKE CREATE ON SCHEMA public FROM " + ident(roles[0]))
	exec("GRANT " + ident(roles[2]) + " TO " + ident(roles[1]))
	check(1, "failed")
	exec("REVOKE " + ident(roles[2]) + " FROM " + ident(roles[1]))
	exec("CREATE FUNCTION public.unsafe_binding_fixture() RETURNS int LANGUAGE SQL SECURITY DEFINER AS 'SELECT 1'")
	check(3, "failed")
	exec("DROP FUNCTION public.unsafe_binding_fixture()")
	check(3, "passed")
}
