package runtime

import (
	"context"
	"errors"
	"net"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/onebox-faas/faas/pkg/rehearsalpg"
	"github.com/onebox-faas/faas/pkg/rehearsalpg/runtime/sqlc"
	"github.com/onebox-faas/faas/pkg/rehearsalwork"
	"github.com/onebox-faas/faas/pkg/tasktoolbinding/taskacl"
)

type taskParticipantCatalog struct {
	*memoryCatalog
	fixture rehearsalpg.Resource
	access  map[string]string
}

func (c taskParticipantCatalog) ScenarioPostgresForApp(ctx context.Context, app string) (rehearsalpg.Resource, error) {
	access := c.access[app]
	if access == "" {
		return c.memoryCatalog.ScenarioPostgresForApp(ctx, app)
	}
	r, err := c.GetScenarioPostgres(ctx, c.fixture.AccountID, c.fixture.RunID)
	r.ParticipantAccess = access
	r.RoleName = rehearsalwork.PostgresParticipantRole(r.ID, app, access)
	return r, err
}

func TestPostgresRealTaskParticipantsUseRestrictedLogins(t *testing.T) {
	p, s := newPostgresTest(t)
	ctx := t.Context()
	var fixture strings.Builder
	for _, table := range taskacl.ReadTables(false) {
		fixture.WriteString("CREATE TABLE public." + table + " (item text NOT NULL);")
	}
	fixture.WriteString("CREATE FUNCTION public.fixture_owner_function() RETURNS integer LANGUAGE sql SECURITY DEFINER AS 'SELECT 1';")
	fixture.WriteString("GRANT SELECT ON public.gregale_mcp_task_crypto_keys TO PUBLIC;")
	req := rehearsalpg.CreateRequest{
		SQL: fixture.String(),
		Spec: rehearsalpg.Spec{Version: 1, FixtureSHA256: rehearsalpg.HashBytes([]byte(fixture.String())), MaxLockWaitMS: 20,
			Tables: []rehearsalpg.Table{{Schema: "public", Name: "gregale_mcp_tasks", Columns: []string{"item"}}}},
	}
	account, run, primary := uuid.NewString(), strings.ReplaceAll(uuid.NewString(), "-", ""), uuid.NewString()
	if _, _, err := s.Create(ctx, account, run, primary, "primary.svc.gregale", req); err != nil {
		t.Fatalf("create Task fixture: %v", err)
	}
	t.Cleanup(func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := s.Delete(cleanup, account, run); err != nil {
			t.Errorf("cleanup fixture: %v", err)
		}
	})
	r, err := s.Catalog.GetScenarioPostgres(ctx, account, run)
	if err != nil {
		t.Fatal(err)
	}
	runtimeApp, observerApp := uuid.NewString(), uuid.NewString()
	s.Catalog = taskParticipantCatalog{s.Catalog.(*memoryCatalog), r, map[string]string{
		runtimeApp: rehearsalwork.PostgresTaskRuntime, observerApp: rehearsalwork.PostgresTaskObserver,
	}}

	bind := func(app string) *pgx.Conn {
		t.Helper()
		dsn, err := s.BindParticipant(ctx, account, run, app, "fixture.svc.gregale")
		if err != nil {
			t.Fatalf("bind enrolled Task participant: %v", err)
		}
		u, err := url.Parse(dsn)
		if err != nil {
			t.Fatal("invalid generated fixture connection")
		}
		u.Host = net.JoinHostPort(p.config.Host, strconv.Itoa(int(p.config.Port)))
		conn, err := pgx.Connect(ctx, u.String())
		if err != nil {
			t.Fatal("Task participant could not connect")
		}
		t.Cleanup(func() { closeTaskParticipantConnection(ctx, conn) })
		return conn
	}
	writer, observer := bind(runtimeApp), bind(observerApp)
	if _, err := s.BindParticipant(ctx, account, run, uuid.NewString(), "fixture.svc.gregale"); !errors.Is(err, ErrFixture) {
		t.Fatal("binding was issued to an unenrolled app")
	}
	if _, err := s.BindParticipant(ctx, account, run, runtimeApp, "fixture.svc.gregale"); !errors.Is(err, ErrFixture) {
		t.Fatal("a duplicate bind replaced existing role credentials")
	}
	if _, err := writer.Exec(ctx, "INSERT INTO public.gregale_mcp_tasks VALUES ('queued')"); err != nil {
		t.Fatal(err)
	}
	var state string
	if err := observer.QueryRow(ctx, "SELECT item FROM public.gregale_mcp_tasks").Scan(&state); err != nil || state != "queued" {
		t.Fatalf("observer read state %q: %v", state, err)
	}
	deny := func(conn *pgx.Conn, query string) {
		t.Helper()
		_, err := conn.Exec(ctx, query)
		var pgErr *pgconn.PgError
		if !errors.As(err, &pgErr) || pgErr.Code != "42501" {
			t.Errorf("expected insufficient privilege for %q: %v", query, err)
		}
	}
	deny(observer, "INSERT INTO public.gregale_mcp_tasks VALUES ('forbidden')")
	deny(observer, "SELECT * FROM public.gregale_mcp_task_crypto_keys")
	deny(observer, "SELECT public.fixture_owner_function()")
	deny(writer, "CREATE TABLE public.forbidden (item text)")
	deny(writer, "CREATE TEMP TABLE forbidden (item text)")
	deny(writer, "TRUNCATE public.gregale_mcp_tasks")
	deny(writer, "INSERT INTO public.gregale_mcp_task_crypto_keys VALUES ('forbidden')")
	deny(writer, "SELECT public.fixture_owner_function()")

	admin, err := p.connect(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	defer closeTaskParticipantConnection(ctx, admin)
	q := sqlc.New()
	roles, err := q.ParticipantRoles(ctx, admin, r.RoleName)
	if err != nil || len(roles) != 2 {
		t.Fatalf("fixture-scoped cleanup discovered %d roles: %v", len(roles), err)
	}
	for _, role := range roles {
		var memberships int
		if err := admin.QueryRow(ctx, "SELECT count(*) FROM pg_catalog.pg_auth_members WHERE member=(SELECT oid FROM pg_catalog.pg_roles WHERE rolname=$1)", role).Scan(&memberships); err != nil || memberships != 0 {
			t.Fatalf("Task login has %d memberships: %v", memberships, err)
		}
	}
	if err := s.Delete(ctx, account, run); err != nil {
		t.Fatal(err)
	}
	for _, role := range roles {
		exists, err := q.RoleExists(ctx, admin, role)
		if err != nil || exists {
			t.Fatalf("Task login remains after fixture deletion: %v", err)
		}
	}
}
