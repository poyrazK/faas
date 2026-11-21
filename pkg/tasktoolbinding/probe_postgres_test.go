package tasktoolbinding_test

import (
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/onebox-faas/faas/pkg/tasktoolbinding"
	"github.com/onebox-faas/faas/pkg/tasktoolbinding/bindingtest"
)

// The trusted connection seam redirects only tests to an owned private database.
// Production still requires public destinations, verified TLS and fixed SQL.
func TestHostedTaskBindingPostgresRoles(t *testing.T) {
	dsns, roles, connect, exec := bindingtest.Postgres(t)
	ident := func(s string) string { return pgx.Identifier{s}.Sanitize() }
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
