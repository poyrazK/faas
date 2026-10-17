package runtime

import (
	"context"
	"net"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/rehearsalpg"
)

type memoryCatalog struct {
	mu   sync.Mutex
	rows map[string]rehearsalpg.Resource
}

func (c *memoryCatalog) ReserveScenarioPostgres(_ context.Context, r rehearsalpg.Resource) (rehearsalpg.Resource, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	key := r.AccountID + r.RunID
	if _, ok := c.rows[key]; ok {
		return r, ErrFixture
	}
	c.rows[key] = rehearsalpg.CloneResource(r)
	return r, nil
}
func (c *memoryCatalog) GetScenarioPostgres(_ context.Context, a, r string) (rehearsalpg.Resource, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	v, ok := c.rows[a+r]
	if !ok {
		return v, ErrFixture
	}
	return rehearsalpg.CloneResource(v), nil
}
func (c *memoryCatalog) ScenarioPostgresForApp(_ context.Context, app string) (rehearsalpg.Resource, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, r := range c.rows {
		if r.AppID == app && r.Status == "ready" {
			return rehearsalpg.CloneResource(r), nil
		}
	}
	return rehearsalpg.Resource{}, ErrFixture
}
func (c *memoryCatalog) TransitionScenarioPostgres(_ context.Context, r rehearsalpg.Resource, expected string) (rehearsalpg.Resource, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	old, ok := c.rows[r.AccountID+r.RunID]
	if !ok || old.ID != r.ID || old.Status != expected || old.OwnerID != r.OwnerID {
		return r, ErrFixture
	}
	c.rows[r.AccountID+r.RunID] = rehearsalpg.CloneResource(r)
	return r, nil
}
func (c *memoryCatalog) ExpiredScenarioPostgres(context.Context) ([]rehearsalpg.Resource, error) {
	return nil, nil
}

func newPostgresTest(t *testing.T) (*Postgres, *Service) {
	t.Helper()
	dsn := os.Getenv("FAAS_REHEARSAL_PG_TEST_ADMIN_URL")
	if dsn == "" {
		t.Skip("set FAAS_REHEARSAL_PG_TEST_ADMIN_URL to a dedicated disposable PostgreSQL 16+ cluster")
	}
	p, e := NewPostgres(t.Context(), dsn)
	if e != nil {
		t.Fatalf("dedicated cluster validation: %v", e)
	}
	return p, NewService(&memoryCatalog{rows: map[string]rehearsalpg.Resource{}}, p)
}
func createPostgresTest(t *testing.T, s *Service) (rehearsalpg.Receipt, *pgx.Conn) {
	t.Helper()
	sql := "CREATE TABLE public.imports (item text NOT NULL);"
	req := rehearsalpg.CreateRequest{Spec: rehearsalpg.Spec{Version: 1, FixtureSHA256: rehearsalpg.HashBytes([]byte(sql)), Tables: []rehearsalpg.Table{{Schema: "public", Name: "imports", Columns: []string{"item"}}}, MaxLockWaitMS: 20}, SQL: sql}
	a, run, app := uuid.NewString(), strings.ReplaceAll(uuid.NewString(), "-", ""), uuid.NewString()
	receipt, connection, e := s.Create(t.Context(), a, run, app, "primary.svc.gregale", req)
	if e != nil {
		t.Fatalf("create fixture: %v", e)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if e := s.Delete(ctx, a, run); e != nil {
			t.Errorf("cleanup fixture: %v", e)
		}
	})
	p := s.Backend.(*Postgres)
	listen, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)
	proxy := rehearsalpg.Proxy{Target: net.JoinHostPort(p.config.Host, strconv.Itoa(int(p.config.Port))), Resolve: func(ctx context.Context, _ string) (rehearsalpg.Resource, error) {
		return s.Catalog.ScenarioPostgresForApp(ctx, app)
	}}
	go func() { _ = proxy.Serve(ctx, listen) }()
	u, e := url.Parse(connection)
	if e != nil {
		t.Fatal(e)
	}
	u.Host = listen.Addr().String()
	conn, e := pgx.Connect(t.Context(), u.String())
	if e != nil {
		t.Fatalf("connect through private proxy: %v", e)
	}
	t.Cleanup(func() { _ = conn.Close(context.Background()) })
	return receipt, conn
}
func sealPostgresTest(t *testing.T, s *Service, r rehearsalpg.Receipt) (rehearsalpg.Evidence, error) {
	t.Helper()
	resource, e := func() (rehearsalpg.Resource, error) {
		c := s.Catalog.(*memoryCatalog)
		c.mu.Lock()
		defer c.mu.Unlock()
		for _, v := range c.rows {
			if v.ID == r.ResourceID {
				return rehearsalpg.CloneResource(v), nil
			}
		}
		return rehearsalpg.Resource{}, ErrFixture
	}()
	if e != nil {
		t.Fatal(e)
	}
	return s.Seal(t.Context(), resource.AccountID, resource.RunID, uuid.NewString())
}

func TestPostgresRealIsolatedImportComparison(t *testing.T) {
	_, s := newPostgresTest(t)
	r1, c1 := createPostgresTest(t, s)
	if _, e := c1.Exec(t.Context(), "INSERT INTO public.imports VALUES ('same-item')"); e != nil {
		t.Fatal(e)
	}
	before, e := sealPostgresTest(t, s, r1)
	if e != nil {
		t.Fatalf("baseline seal: %+v %v", before, e)
	}
	r2, c2 := createPostgresTest(t, s)
	if _, e := c2.Exec(t.Context(), "INSERT INTO public.imports VALUES ('same-item'),('same-item')"); e != nil {
		t.Fatal(e)
	}
	after, e := sealPostgresTest(t, s, r2)
	if e != nil {
		t.Fatalf("candidate seal: %+v %v", after, e)
	}
	if before.InitialSHA256 != after.InitialSHA256 || before.ResourceID == after.ResourceID {
		t.Fatal("fixtures did not use isolated identical starting data")
	}
	if c := rehearsalpg.Compare(&before, &after); c.Status != "failed" || len(c.After[0].Inserted) != 2 {
		t.Fatalf("duplicate import was not detected: %+v", c)
	}
	if _, e = c2.Exec(t.Context(), "SELECT 1"); e == nil {
		t.Fatal("retired database connection survived")
	}
}

func TestPostgresRealPendingSQLAndUnsupportedObjectsCannotPass(t *testing.T) {
	for _, tc := range []struct{ name, sql string }{{"uncommitted write", "BEGIN; INSERT INTO public.imports VALUES ('pending')"}, {"undeclared table", "CREATE TABLE public.unobserved (id int)"}, {"view replacement", "DROP TABLE public.imports; CREATE VIEW public.imports AS SELECT 'forged'::text AS item"}, {"forced row security", "ALTER TABLE public.imports ENABLE ROW LEVEL SECURITY; ALTER TABLE public.imports FORCE ROW LEVEL SECURITY"}} {
		t.Run(tc.name, func(t *testing.T) {
			_, s := newPostgresTest(t)
			r, c := createPostgresTest(t, s)
			if _, e := c.Exec(t.Context(), tc.sql); e != nil {
				t.Fatal(e)
			}
			evidence, e := sealPostgresTest(t, s, r)
			if e == nil || evidence.Status == "complete" || !evidence.CleanupConfirmed {
				t.Fatalf("unsafe seal: %+v %v", evidence, e)
			}
		})
	}
}

func TestPostgresRealRoleCannotEscapeFixture(t *testing.T) {
	_, s := newPostgresTest(t)
	r, c := createPostgresTest(t, s)
	for _, query := range []string{"CREATE DATABASE rehearsal_escape", "CREATE ROLE rehearsal_escape SUPERUSER", "SELECT pg_read_file('/etc/passwd')", "COPY public.imports TO PROGRAM 'true'"} {
		if _, e := c.Exec(t.Context(), query); e == nil {
			t.Fatalf("privileged SQL allowed: %s", query)
		}
	}
	if _, e := sealPostgresTest(t, s, r); e != nil {
		t.Fatal(e)
	}
}

func TestPostgresRealMigrationLockWaitIsObserved(t *testing.T) {
	p, s := newPostgresTest(t)
	r, writer := createPostgresTest(t, s)
	resource, _ := s.Catalog.ScenarioPostgresForApp(t.Context(), r.AppID)
	lock, e := p.connect(t.Context(), resource.DatabaseName)
	if e != nil {
		t.Fatal(e)
	}
	defer func() { _ = lock.Close(context.Background()) }()
	tx, e := lock.Begin(t.Context())
	if e != nil {
		t.Fatal(e)
	}
	if _, e = tx.Exec(t.Context(), "LOCK TABLE public.imports IN ACCESS EXCLUSIVE MODE"); e != nil {
		t.Fatal(e)
	}
	done := make(chan error, 1)
	go func() {
		_, e := writer.Exec(t.Context(), "ALTER TABLE public.imports ADD COLUMN migrated integer")
		done <- e
	}()
	deadline := time.Now().Add(3 * time.Second)
	for {
		v, e := p.LockWait(t.Context(), resource)
		if e != nil {
			t.Fatal(e)
		}
		if v > 100 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("migration did not enter a lock wait")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if e = tx.Commit(t.Context()); e != nil {
		t.Fatal(e)
	}
	if e = <-done; e != nil {
		t.Fatal(e)
	}
	evidence, e := sealPostgresTest(t, s, r)
	if e != nil {
		t.Fatalf("seal: %+v %v", evidence, e)
	}
	if evidence.LockBudgetPassed || evidence.MaxObservedLockWaitMS < 20 {
		t.Fatalf("lock wait escaped observation: %+v", evidence)
	}
}

func TestPostgresRealOwnerLossCannotPass(t *testing.T) {
	_, s := newPostgresTest(t)
	original := s.Backend
	r2, _ := createPostgresTest(t, s)
	other := NewService(s.Catalog, original)
	resource, _ := s.Catalog.ScenarioPostgresForApp(t.Context(), r2.AppID)
	if _, e := other.Seal(t.Context(), resource.AccountID, resource.RunID, uuid.NewString()); e == nil {
		t.Fatal("new collector adopted previous owner")
	}
}
