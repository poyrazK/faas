package runtime

import (
	"context"
	"errors"
	"fmt"
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

func (c taskParticipantCatalog) ScenarioPostgresTaskParticipants(context.Context, rehearsalpg.Resource) ([]rehearsalpg.TaskParticipant, error) {
	var participants []rehearsalpg.TaskParticipant
	for app, access := range c.access {
		participants = append(participants, rehearsalpg.TaskParticipant{AppID: app, Access: access})
	}
	return participants, nil
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

type taskPostgresFixture struct {
	backend                 *Postgres
	service                 *Service
	resource                rehearsalpg.Resource
	runtimeApp, observerApp string
	owner, writer, observer *pgx.Conn
}

func newTaskPostgresFixture(t *testing.T) taskPostgresFixture {
	t.Helper()
	p, s := newPostgresTest(t)
	ctx := t.Context()
	var fixture strings.Builder
	for _, table := range taskacl.ReadTables(false) {
		fixture.WriteString("CREATE TABLE public." + table + " (item text NOT NULL);")
	}
	fixture.WriteString("CREATE SEQUENCE public.gregale_mcp_task_claim_order_seq; CREATE SEQUENCE public.unrelated_seq;")
	fixture.WriteString("CREATE FUNCTION public.fixture_owner_function() RETURNS integer LANGUAGE sql SECURITY DEFINER AS 'SELECT 1';")
	fixture.WriteString("GRANT SELECT ON public.gregale_mcp_task_crypto_keys TO PUBLIC;")
	req := rehearsalpg.CreateRequest{
		SQL: fixture.String(),
		Spec: rehearsalpg.Spec{Version: 1, FixtureSHA256: rehearsalpg.HashBytes([]byte(fixture.String())), MaxLockWaitMS: 20,
			Tables: []rehearsalpg.Table{{Schema: "public", Name: "gregale_mcp_tasks", Columns: []string{"item"}}}},
	}
	account, run, primary := uuid.NewString(), strings.ReplaceAll(uuid.NewString(), "-", ""), uuid.NewString()
	_, primaryDSN, err := s.Create(ctx, account, run, primary, "primary.svc.gregale", req)
	if err != nil {
		t.Fatalf("create Task fixture: %v", err)
	}
	t.Cleanup(func() {
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
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
		return connectTaskFixture(ctx, t, p, dsn)
	}
	owner := connectTaskFixture(ctx, t, p, primaryDSN)
	return taskPostgresFixture{
		backend: p, service: s, resource: r, runtimeApp: runtimeApp, observerApp: observerApp,
		owner: owner, writer: bind(runtimeApp), observer: bind(observerApp),
	}
}

func connectTaskFixture(ctx context.Context, t *testing.T, p *Postgres, dsn string) *pgx.Conn {
	t.Helper()
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

func TestPostgresRealTaskParticipantsUseRestrictedLogins(t *testing.T) {
	f := newTaskPostgresFixture(t)
	p, s, r := f.backend, f.service, f.resource
	runtimeApp, writer, observer := f.runtimeApp, f.writer, f.observer
	ctx, account, run := t.Context(), r.AccountID, r.RunID
	if _, err := s.BindParticipant(ctx, account, run, uuid.NewString(), "fixture.svc.gregale"); !errors.Is(err, ErrFixture) {
		t.Fatal("binding was issued to an unenrolled app")
	}
	if _, err := s.BindParticipant(ctx, account, run, runtimeApp, "fixture.svc.gregale"); !errors.Is(err, ErrFixture) {
		t.Fatal("a duplicate bind replaced existing role credentials")
	}
	if _, err := writer.Exec(ctx, "INSERT INTO public.gregale_mcp_tasks VALUES ('queued')"); err != nil {
		t.Fatal(err)
	}
	var claimOrder int64
	if err := writer.QueryRow(ctx, "SELECT nextval('public.gregale_mcp_task_claim_order_seq')").Scan(&claimOrder); err != nil || claimOrder != 1 {
		t.Fatalf("advance Task claim order: %d %v", claimOrder, err)
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
	deny(observer, "SELECT nextval('public.gregale_mcp_task_claim_order_seq')")
	deny(writer, "CREATE TABLE public.forbidden (item text)")
	deny(writer, "CREATE TEMP TABLE forbidden (item text)")
	deny(writer, "TRUNCATE public.gregale_mcp_tasks")
	deny(writer, "INSERT INTO public.gregale_mcp_task_crypto_keys VALUES ('forbidden')")
	deny(writer, "SELECT public.fixture_owner_function()")
	deny(writer, "SELECT last_value FROM public.gregale_mcp_task_claim_order_seq")
	deny(writer, "SELECT setval('public.gregale_mcp_task_claim_order_seq', 99)")
	deny(writer, "SELECT nextval('public.unrelated_seq')")

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
	evidence, err := s.Seal(ctx, account, run, uuid.NewString())
	if err != nil || evidence.Status != "complete" || !evidence.CleanupConfirmed {
		t.Fatalf("seal restricted Task fixture: %s %s %v", evidence.Status, evidence.Reason, err)
	}
	for _, role := range roles {
		exists, err := q.RoleExists(ctx, admin, role)
		if err != nil || exists {
			t.Fatalf("Task login remains after fixture deletion: %v", err)
		}
	}
}

func TestPostgresRealTaskSealRejectsMigrationPrivilegeDrift(t *testing.T) {
	for _, tc := range []struct {
		name      string
		statement string
		observer  bool
	}{
		{"observer insert", "GRANT INSERT ON public.gregale_mcp_tasks TO %s", true},
		{"observer private reads", "GRANT SELECT ON public.gregale_mcp_task_crypto_keys TO %s", true},
		{"runtime schema create", "GRANT CREATE ON SCHEMA public TO %s", false},
		{"runtime callable definer", "GRANT EXECUTE ON FUNCTION public.fixture_owner_function() TO %s", false},
		{"runtime missing write", "REVOKE DELETE ON public.gregale_mcp_tasks FROM %s", false},
		{"runtime missing sequence usage", "REVOKE USAGE ON SEQUENCE public.gregale_mcp_task_claim_order_seq FROM %s", false},
		{"runtime sequence select", "GRANT SELECT ON SEQUENCE public.gregale_mcp_task_claim_order_seq TO %s", false},
		{"runtime sequence update", "GRANT UPDATE ON SEQUENCE public.gregale_mcp_task_claim_order_seq TO %s", false},
		{"runtime regrant", "GRANT USAGE ON SEQUENCE public.gregale_mcp_task_claim_order_seq TO %s WITH GRANT OPTION", false},
		{"runtime unrelated sequence", "GRANT USAGE ON SEQUENCE public.unrelated_seq TO %s", false},
		{"observer claim sequence", "GRANT USAGE ON SEQUENCE public.gregale_mcp_task_claim_order_seq TO %s", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newTaskPostgresFixture(t)
			r := f.resource
			ctx := t.Context()
			target, access := f.runtimeApp, rehearsalwork.PostgresTaskRuntime
			if tc.observer {
				target, access = f.observerApp, rehearsalwork.PostgresTaskObserver
			}
			role := rehearsalwork.PostgresParticipantRole(r.ID, target, access)
			if _, err := f.owner.Exec(ctx, fmt.Sprintf(tc.statement, pgx.Identifier{role}.Sanitize())); err != nil {
				t.Fatalf("apply migration grant: %v", err)
			}
			evidence, err := f.service.Seal(ctx, r.AccountID, r.RunID, uuid.NewString())
			if !errors.Is(err, ErrFixture) || evidence.Status != "incomplete" ||
				evidence.Reason != errTaskParticipantPrivileges.Error() || !evidence.CleanupConfirmed {
				t.Fatalf("seal accepted privilege drift: %s %s cleanup=%v %v", evidence.Status, evidence.Reason, evidence.CleanupConfirmed, err)
			}
			admin, err := f.backend.connect(ctx, "")
			if err != nil {
				t.Fatal(err)
			}
			defer closeTaskParticipantConnection(ctx, admin)
			q := sqlc.New()
			if exists, err := q.DatabaseExists(ctx, admin, r.DatabaseName); err != nil || exists {
				t.Fatalf("fixture database remains after failed seal: %v %v", exists, err)
			}
			if roles, err := q.ParticipantRoles(ctx, admin, r.RoleName); err != nil || len(roles) != 0 {
				t.Fatalf("fixture roles remain after failed seal: %d %v", len(roles), err)
			}
		})
	}
}

func TestPostgresRealTaskCleanupObservation(t *testing.T) {
	f := newTaskPostgresFixture(t)
	ctx, r := t.Context(), f.resource
	if _, _, err := f.service.ObserveDisposedTaskFixture(ctx, r.AccountID, r.RunID, r.AppID); err == nil {
		t.Fatal("live fixture was accepted as disposed")
	}
	if err := f.service.Delete(ctx, r.AccountID, r.RunID); err != nil {
		t.Fatal(err)
	}
	if _, _, err := f.service.ObserveDisposedTaskFixture(ctx, r.AccountID, r.RunID, uuid.NewString()); err == nil {
		t.Fatal("disposal evidence was returned for a different migration app")
	}
	database, logins, err := f.service.ObserveDisposedTaskFixture(ctx, r.AccountID, r.RunID, r.AppID)
	if err != nil || database || logins != 0 {
		t.Fatalf("disposed fixture observation: database=%v logins=%d err=%v", database, logins, err)
	}
}
