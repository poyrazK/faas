// adr:531
package connectionfence

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/managedpostgres"
)

type fixture struct {
	root, maintenance *pgxpool.Pool
	c                 *Controller
	config            Config
	request           Request
	admin, tenant     string
	password          string
	bootstrap         *pgxpool.Config
}

func newFixture(t *testing.T, lockTimeout ...string) fixture {
	t.Helper()
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL required for isolated database connection fence contracts")
	}
	ctx := t.Context()
	config, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	root, err := pgxpool.NewWithConfig(ctx, config.Copy())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(root.Close)
	if err := root.Ping(ctx); err != nil {
		t.Fatal(err)
	}
	suffix := strings.ReplaceAll(uuid.NewString(), "-", "")[:16]
	f := fixture{root: root, bootstrap: config, admin: "gf_admin_" + suffix, tenant: "gf_client_" + suffix, password: uuid.NewString(),
		config: Config{MaintenanceDatabase: "gf_maintenance_" + suffix, MaintenanceRole: "gf_admin_" + suffix}}
	f.request = Request{Identity: Identity{OwnerToken: uuid.NewString(), SourceResourceID: "project/branch-" + suffix},
		DatabaseNames: []string{"gf_source_" + suffix, "gf_closed_" + suffix + "\";%"}}
	// All DDL is scoped to these fresh random role/database names. The test
	// never changes connection settings on the shared bootstrap database.
	createdDBs, createdRoles := []string{}, []string{}
	t.Cleanup(func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		for _, db := range createdDBs {
			if _, err := root.Exec(cleanup, "DROP DATABASE "+pgx.Identifier{db}.Sanitize()+" WITH (FORCE)"); err != nil {
				t.Errorf("drop owned database: %v", err)
			}
		}
		for _, role := range createdRoles {
			if _, err := root.Exec(cleanup, "DROP ROLE "+pgx.Identifier{role}.Sanitize()); err != nil {
				t.Errorf("drop owned role: %v", err)
			}
		}
	})
	for _, role := range []string{f.admin, f.tenant} {
		if _, err := root.Exec(ctx, "CREATE ROLE "+pgx.Identifier{role}.Sanitize()+" LOGIN NOSUPERUSER NOCREATEROLE NOCREATEDB PASSWORD '"+f.password+"'"); err != nil {
			t.Fatal(err)
		}
		createdRoles = append(createdRoles, role)
	}
	for _, db := range append([]string{f.config.MaintenanceDatabase}, f.request.DatabaseNames...) {
		if _, err := root.Exec(ctx, "CREATE DATABASE "+pgx.Identifier{db}.Sanitize()+" OWNER "+pgx.Identifier{f.admin}.Sanitize()+" TEMPLATE template0"); err != nil {
			t.Fatal(err)
		}
		createdDBs = append(createdDBs, db)
	}
	if _, err := root.Exec(ctx, "REVOKE ALL ON DATABASE "+pgx.Identifier{f.config.MaintenanceDatabase}.Sanitize()+" FROM PUBLIC"); err != nil {
		t.Fatal(err)
	}
	if _, err := root.Exec(ctx, "ALTER DATABASE "+pgx.Identifier{f.request.DatabaseNames[1]}.Sanitize()+" ALLOW_CONNECTIONS false"); err != nil {
		t.Fatal(err)
	}
	maint := config.Copy()
	maint.ConnConfig.Database = f.config.MaintenanceDatabase
	maint.ConnConfig.User = f.admin
	maint.ConnConfig.Password = f.password
	if len(lockTimeout) != 0 {
		maint.ConnConfig.RuntimeParams["lock_timeout"] = lockTimeout[0]
	}
	maint.MaxConns = 2
	f.maintenance, err = pgxpool.NewWithConfig(ctx, maint)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(f.maintenance.Close)
	f.c, err = New(ctx, f.maintenance, f.config)
	if err != nil {
		t.Fatalf("private maintenance: %v", err)
	}
	return f
}

func (f fixture) connect(ctx context.Context, t *testing.T, name, user string) (*pgx.Conn, error) {
	t.Helper()
	config := f.bootstrap.ConnConfig.Copy()
	config.Database = name
	config.User = user
	if user != f.bootstrap.ConnConfig.User {
		config.Password = f.password
	}
	conn, err := pgx.ConnectConfig(ctx, config)
	if err == nil {
		t.Cleanup(func() {
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			_ = conn.Close(ctx)
		})
	}
	return conn, err
}

func TestConnectionFenceClosesAdmissionAndObservesDrain(t *testing.T) {
	f := newFixture(t)
	ctx := t.Context()
	for range 2 {
		if err := f.c.Install(ctx); err != nil {
			t.Fatal(err)
		}
	}
	admin, err := f.connect(t.Context(), t, f.request.DatabaseNames[0], f.admin)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := admin.Exec(ctx, "CREATE TABLE data (value integer NOT NULL); GRANT INSERT ON data TO "+pgx.Identifier{f.tenant}.Sanitize()); err != nil {
		t.Fatal(err)
	}
	if err := admin.Close(ctx); err != nil {
		t.Fatal(err)
	}
	client, err := f.connect(t.Context(), t, f.request.DatabaseNames[0], f.tenant)
	if err != nil {
		t.Fatal(err)
	}
	closed, err := f.c.Close(ctx, f.request)
	if err != nil || closed.State != "closed" || closed.Drained || len(closed.Databases) != 2 {
		t.Fatalf("close with admitted client: %+v %v", closed, err)
	}
	if _, err := client.Exec(ctx, "INSERT INTO data VALUES (1)"); err != nil {
		t.Fatalf("closure killed an admitted session: %v", err)
	}
	if _, err := client.Exec(ctx, "ALTER DATABASE "+pgx.Identifier{f.request.DatabaseNames[0]}.Sanitize()+" ALLOW_CONNECTIONS true"); err == nil {
		t.Fatal("client reopened database")
	}
	if _, err := f.connect(t.Context(), t, f.request.DatabaseNames[0], f.admin); err == nil {
		t.Fatal("new admin connection bypassed closure")
	} else {
		var pe *pgconn.PgError
		if !errors.As(err, &pe) || pe.Code != "55000" {
			t.Fatalf("closed admission: %v", err)
		}
	}
	// New controller represents a replacement worker after acknowledgement loss.
	replacement, err := New(ctx, f.maintenance, f.config)
	if err != nil {
		t.Fatal(err)
	}
	replayed, err := replacement.Close(ctx, f.request)
	if err != nil || replayed.Drained || !replayed.ClosedAt.Equal(closed.ClosedAt) {
		t.Fatalf("close recovery: %+v %v", replayed, err)
	}
	if err := client.Close(ctx); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		observed, err := replacement.Observe(ctx, f.request.Identity)
		if err != nil {
			t.Fatal(err)
		}
		if observed.Drained {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("disconnected client did not drain")
		}
		time.Sleep(10 * time.Millisecond)
	}
	for _, fault := range []string{"owner", "source", "database_set", "another_owner"} {
		bad := f.request
		bad.DatabaseNames = append([]string(nil), bad.DatabaseNames...)
		switch fault {
		case "owner", "another_owner":
			bad.OwnerToken = uuid.NewString()
		case "source":
			bad.SourceResourceID += "-other"
		case "database_set":
			bad.DatabaseNames = bad.DatabaseNames[:1]
		}
		if _, err := replacement.Close(ctx, bad); !errors.Is(err, managedpostgres.ErrConflict) {
			t.Fatalf("%s close adopted source: %v", fault, err)
		}
		if fault == "owner" || fault == "source" {
			if _, err := replacement.Release(ctx, bad.Identity); !errors.Is(err, managedpostgres.ErrConflict) {
				t.Fatalf("%s released source: %v", fault, err)
			}
		}
	}
	for range 2 {
		released, err := replacement.Release(ctx, f.request.Identity)
		if err != nil || released.State != "released" || released.ReleasedAt.IsZero() || released.Drained {
			t.Fatalf("release recovery: %+v %v", released, err)
		}
	}
	if _, err := f.connect(t.Context(), t, f.request.DatabaseNames[0], f.tenant); err != nil {
		t.Fatalf("original open setting not restored: %v", err)
	}
	if _, err := f.connect(t.Context(), t, f.request.DatabaseNames[1], f.admin); err == nil {
		t.Fatal("original closed setting was reopened")
	}
	if _, err := replacement.Close(ctx, f.request); !errors.Is(err, managedpostgres.ErrConflict) {
		t.Fatalf("released owner reused: %v", err)
	}
}

func TestConnectionFenceRejectsChangedDatabaseIdentity(t *testing.T) {
	for _, fault := range []string{"rename", "owner", "reopened", "replacement"} {
		t.Run(fault, func(t *testing.T) {
			f := newFixture(t)
			ctx := t.Context()
			if err := f.c.Install(ctx); err != nil {
				t.Fatal(err)
			}
			if _, err := f.c.Close(ctx, f.request); err != nil {
				t.Fatal(err)
			}
			name := f.request.DatabaseNames[0]
			switch fault {
			case "rename":
				other := name + "_renamed"
				if _, err := f.root.Exec(ctx, "ALTER DATABASE "+pgx.Identifier{name}.Sanitize()+" RENAME TO "+pgx.Identifier{other}.Sanitize()); err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() {
					if _, err := f.root.Exec(context.Background(), "ALTER DATABASE "+pgx.Identifier{other}.Sanitize()+" RENAME TO "+pgx.Identifier{name}.Sanitize()); err != nil {
						t.Error(err)
					}
				})
			case "owner":
				if _, err := f.root.Exec(ctx, "ALTER DATABASE "+pgx.Identifier{name}.Sanitize()+" OWNER TO "+pgx.Identifier{f.tenant}.Sanitize()); err != nil {
					t.Fatal(err)
				}
			case "reopened":
				if _, err := f.root.Exec(ctx, "ALTER DATABASE "+pgx.Identifier{name}.Sanitize()+" ALLOW_CONNECTIONS true"); err != nil {
					t.Fatal(err)
				}
			case "replacement":
				if _, err := f.root.Exec(ctx, "DROP DATABASE "+pgx.Identifier{name}.Sanitize()); err != nil {
					t.Fatal(err)
				}
				if _, err := f.root.Exec(ctx, "CREATE DATABASE "+pgx.Identifier{name}.Sanitize()+" OWNER "+pgx.Identifier{f.admin}.Sanitize()+" TEMPLATE template0"); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := f.c.Observe(ctx, f.request.Identity); !errors.Is(err, managedpostgres.ErrConflict) {
				t.Fatalf("%s changed source observed as drained: %v", fault, err)
			}
			if _, err := f.c.Release(ctx, f.request.Identity); !errors.Is(err, managedpostgres.ErrConflict) {
				t.Fatalf("%s changed source was released: %v", fault, err)
			}
			if _, err := f.c.Abandon(ctx, f.request.Identity); !errors.Is(err, managedpostgres.ErrConflict) {
				t.Fatalf("%s changed source was abandoned: %v", fault, err)
			}
		})
	}
}

func TestConnectionFenceRequiresPrivateMaintenance(t *testing.T) {
	for _, fault := range []string{"public_connect", "role_membership", "schema_grant", "function_grant", "foreign_session"} {
		t.Run(fault, func(t *testing.T) {
			f := newFixture(t)
			ctx := t.Context()
			if err := f.c.Install(ctx); err != nil {
				t.Fatal(err)
			}
			switch fault {
			case "public_connect":
				if _, err := f.root.Exec(ctx, "GRANT CONNECT ON DATABASE "+pgx.Identifier{f.config.MaintenanceDatabase}.Sanitize()+" TO PUBLIC"); err != nil {
					t.Fatal(err)
				}
			case "role_membership":
				if _, err := f.root.Exec(ctx, "GRANT "+pgx.Identifier{f.admin}.Sanitize()+" TO "+pgx.Identifier{f.tenant}.Sanitize()); err != nil {
					t.Fatal(err)
				}
			case "schema_grant":
				if _, err := f.maintenance.Exec(ctx, "GRANT USAGE ON SCHEMA gregale_checkpoint TO PUBLIC"); err != nil {
					t.Fatal(err)
				}
			case "function_grant":
				if _, err := f.maintenance.Exec(ctx, "GRANT EXECUTE ON FUNCTION gregale_checkpoint.close_connections(uuid,text,text[]) TO PUBLIC"); err != nil {
					t.Fatal(err)
				}
			case "foreign_session":
				// Infrastructure superusers are trusted, but their active
				// maintenance sessions still invalidate isolation observations.
				if _, err := f.connect(t.Context(), t, f.config.MaintenanceDatabase, f.bootstrap.ConnConfig.User); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := f.c.Close(ctx, f.request); !errors.Is(err, managedpostgres.ErrUnsupported) {
				t.Fatalf("%s accepted unsafe maintenance: %v", fault, err)
			}
			var allowed bool
			if err := f.root.QueryRow(ctx, "SELECT datallowconn FROM pg_database WHERE datname=$1", f.request.DatabaseNames[0]).Scan(&allowed); err != nil || !allowed {
				t.Fatalf("unsafe maintenance changed source: %t %v", allowed, err)
			}
		})
	}
}

func TestConnectionFenceAdmissionIsAtomicAndConcurrentOwnersConflict(t *testing.T) {
	f := newFixture(t)
	ctx := t.Context()
	if err := f.c.Install(ctx); err != nil {
		t.Fatal(err)
	}
	bad := f.request
	bad.DatabaseNames = append(append([]string(nil), bad.DatabaseNames...), "absent_database")
	if _, err := f.c.Close(ctx, bad); !errors.Is(err, managedpostgres.ErrConflict) {
		t.Fatalf("missing source accepted: %v", err)
	}
	var count int
	if err := f.maintenance.QueryRow(ctx, "SELECT count(*) FROM gregale_checkpoint.connection_fences").Scan(&count); err != nil || count != 0 {
		t.Fatalf("failed closure recorded intent: %d %v", count, err)
	}
	start := make(chan struct{})
	var wg sync.WaitGroup
	results := make(chan Identity, 2)
	errs := make(chan error, 2)
	for range 2 {
		request := f.request
		request.OwnerToken = uuid.NewString()
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, err := f.c.Close(ctx, request)
			if err == nil {
				results <- request.Identity
			}
			errs <- err
		}()
	}
	close(start)
	wg.Wait()
	close(results)
	close(errs)
	wins, conflicts := 0, 0
	for err := range errs {
		if err == nil {
			wins++
		} else if errors.Is(err, managedpostgres.ErrConflict) {
			conflicts++
		} else {
			t.Fatal(err)
		}
	}
	if wins != 1 || conflicts != 1 {
		t.Fatalf("owners: wins=%d conflicts=%d", wins, conflicts)
	}
	for winner := range results {
		if _, err := f.c.Release(ctx, winner); err != nil {
			t.Fatal(err)
		}
	}
}

func TestConnectionFenceLockTimeoutRollsBackPartialClosure(t *testing.T) {
	f := newFixture(t, "750ms")
	ctx := t.Context()
	if err := f.c.Install(ctx); err != nil {
		t.Fatal(err)
	}
	// Hold the second database's shared-object lock. Close changes the first
	// database, then waits on this independently held lock in the same SQL
	// transaction. A server-rejected statement must roll back both the first
	// flag and ledger. Client cancellation alone is an unknown commit outcome.
	blocker, err := f.root.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = blocker.Rollback(context.Background()) }()
	if _, err := blocker.Exec(ctx, "ALTER DATABASE "+pgx.Identifier{f.request.DatabaseNames[1]}.Sanitize()+" ALLOW_CONNECTIONS false"); err != nil {
		t.Fatal(err)
	}
	result := make(chan error, 1)
	go func() {
		_, err := f.c.Close(ctx, f.request)
		result <- err
	}()
	waitForCloseDatabaseLock(t, f)
	select {
	case err := <-result:
		if !errors.Is(err, managedpostgres.ErrUnavailable) {
			t.Fatalf("server lock timeout: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("server lock timeout did not interrupt the closure")
	}
	if err := blocker.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	// The server has acknowledged rollback. A different operation can now
	// acquire the source without adopting a partial original-setting record.
	retry := f.request
	retry.OwnerToken = uuid.NewString()
	closed, err := f.c.Close(ctx, retry)
	if err != nil || !closed.Drained {
		t.Fatalf("replacement after rollback: %+v %v", closed, err)
	}
	for _, db := range closed.Databases {
		if db.Name == f.request.DatabaseNames[0] && !db.OriginalAllowConnections {
			t.Fatal("partial closure changed recorded original setting")
		}
	}
	if _, err := f.c.Observe(ctx, f.request.Identity); !errors.Is(err, managedpostgres.ErrNotFound) {
		t.Fatalf("rejected owner ledger survived: %v", err)
	}
	if _, err := f.c.Release(ctx, retry.Identity); err != nil {
		t.Fatal(err)
	}
}

func waitForCloseDatabaseLock(t *testing.T, f fixture) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for {
		var waiting bool
		if err := f.root.QueryRow(t.Context(), `SELECT EXISTS (SELECT 1 FROM pg_stat_activity
 WHERE datname=$1 AND wait_event_type='Lock' AND query LIKE '%close_connections%')`, f.config.MaintenanceDatabase).Scan(&waiting); err != nil {
			t.Fatal(err)
		}
		if waiting {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("closure did not reach the held database lock")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestConnectionFenceAbandonPreventsLateClose(t *testing.T) {
	f := newFixture(t)
	ctx := t.Context()
	if err := f.c.Install(ctx); err != nil {
		t.Fatal(err)
	}
	marker, err := f.c.Abandon(ctx, f.request.Identity)
	if err != nil || marker.State != "abandoned" || !marker.ClosedAt.IsZero() || marker.ReleasedAt.IsZero() || marker.Drained || len(marker.Databases) != 0 {
		t.Fatalf("abandon before dispatch: %+v %v", marker, err)
	}
	if _, err := f.c.Close(ctx, f.request); !errors.Is(err, managedpostgres.ErrConflict) {
		t.Fatalf("delayed close passed terminal marker: %v", err)
	}
	other := f.request
	other.OwnerToken = uuid.NewString()
	if _, err := f.c.Close(ctx, other); err != nil {
		t.Fatal(err)
	}
	replayed, err := f.c.Abandon(ctx, f.request.Identity)
	if err != nil || !replayed.ReleasedAt.Equal(marker.ReleasedAt) {
		t.Fatalf("terminal marker recovery: %+v %v", replayed, err)
	}
	fresh := f.request.Identity
	fresh.OwnerToken = uuid.NewString()
	if _, err := f.c.Abandon(ctx, fresh); err != nil {
		t.Fatalf("unrelated abandonment: %v", err)
	}
	active, err := f.c.Observe(ctx, other.Identity)
	if err != nil || active.State != "closed" || !active.Drained {
		t.Fatalf("abandonment released another owner: %+v %v", active, err)
	}
	wrong := f.request.Identity
	wrong.SourceResourceID += "-other"
	if _, err := f.c.Abandon(ctx, wrong); !errors.Is(err, managedpostgres.ErrConflict) {
		t.Fatalf("marker source was changed: %v", err)
	}
	if _, err := f.c.Release(ctx, f.request.Identity); !errors.Is(err, managedpostgres.ErrConflict) {
		t.Fatalf("abandoned capture was treated as completed: %v", err)
	}
	if _, err := f.c.Abandon(ctx, other.Identity); err != nil {
		t.Fatal(err)
	}
}

func TestConnectionFenceAbandonRecoversCancelledInFlightClose(t *testing.T) {
	f := newFixture(t)
	ctx := t.Context()
	if err := f.c.Install(ctx); err != nil {
		t.Fatal(err)
	}
	blocker, err := f.root.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = blocker.Rollback(context.Background()) }()
	if _, err := blocker.Exec(ctx, "ALTER DATABASE "+pgx.Identifier{f.request.DatabaseNames[1]}.Sanitize()+" ALLOW_CONNECTIONS false"); err != nil {
		t.Fatal(err)
	}
	closing, cancel := context.WithCancel(ctx)
	defer cancel()
	result := make(chan error, 1)
	go func() {
		_, err := f.c.Close(closing, f.request)
		result <- err
	}()
	waitForCloseDatabaseLock(t, f)
	cancel()
	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("cancelled client: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("cancelled client did not return")
	}
	// Recover using a new controller and the durable owner. The original
	// statement may still hold its transaction and may commit after unblock.
	replacement, err := New(ctx, f.maintenance, f.config)
	if err != nil {
		t.Fatal(err)
	}
	type recoveryResult struct {
		observation Observation
		err         error
	}
	recovered := make(chan recoveryResult, 1)
	go func() {
		out, err := replacement.Abandon(ctx, f.request.Identity)
		recovered <- recoveryResult{out, err}
	}()
	if err := blocker.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case recovery := <-recovered:
		if recovery.err != nil || recovery.observation.Drained || recovery.observation.State != "released" && recovery.observation.State != "abandoned" {
			t.Fatalf("unknown close outcome recovery: %+v", recovery)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("abandonment did not recover the pending close")
	}
	if _, err := replacement.Close(ctx, f.request); !errors.Is(err, managedpostgres.ErrConflict) {
		t.Fatalf("cancelled owner reopened closure: %v", err)
	}
	for i, name := range f.request.DatabaseNames {
		var allowed bool
		if err := f.root.QueryRow(ctx, "SELECT datallowconn FROM pg_database WHERE datname=$1", name).Scan(&allowed); err != nil || allowed != (i == 0) {
			t.Fatalf("original setting %q: %t %v", name, allowed, err)
		}
	}
}

func TestConnectionFenceValidationAndCancellation(t *testing.T) {
	f := newFixture(t)
	ctx := t.Context()
	if err := f.c.Install(ctx); err != nil {
		t.Fatal(err)
	}
	for _, names := range [][]string{nil, {f.config.MaintenanceDatabase}, {""}, {"bad\x00name"}, {f.request.DatabaseNames[0], f.request.DatabaseNames[0]}} {
		bad := f.request
		bad.DatabaseNames = names
		if _, err := f.c.Close(ctx, bad); !errors.Is(err, managedpostgres.ErrInvalid) {
			t.Fatalf("invalid names %q: %v", names, err)
		}
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := f.c.Close(cancelled, f.request); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled closure: %v", err)
	}
	if _, err := f.c.Observe(ctx, f.request.Identity); !errors.Is(err, managedpostgres.ErrNotFound) {
		t.Fatalf("cancelled closure committed: %v", err)
	}
	if err := classifyError(fmt.Errorf("sensitive provider info: %w", &pgconn.PgError{Code: "XX000", Message: "password"})); err.Error() != "managed postgres unavailable" {
		t.Fatal("raw SQL error escaped")
	}
}
