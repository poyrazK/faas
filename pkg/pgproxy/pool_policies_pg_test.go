//go:build !no_pg

package pgproxy

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"google.golang.org/grpc"

	scheddpb "github.com/onebox-faas/faas/api/proto/onebox/faas/schedd/v1"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/dbcreds"
	"github.com/onebox-faas/faas/pkg/managedpostgres"
	"github.com/onebox-faas/faas/pkg/managedpostgres/firecracker"
	"github.com/onebox-faas/faas/pkg/pgscram"
)

type timeoutTestScheduler struct {
	firecracker.Scheduler
	admin *pgx.Conn
}

func (s timeoutTestScheduler) DatabaseAdmin(ctx context.Context, in *scheddpb.DatabaseAdminRequest, _ ...grpc.CallOption) (*scheddpb.DatabaseAdminResponse, error) {
	_, err := s.admin.PgConn().Exec(ctx, in.GetSql()).ReadAll()
	return &scheddpb.DatabaseAdminResponse{}, err
}

func TestBindingTimeoutPolicyRecyclesPooledConnections(t *testing.T) {
	if os.Getenv("DATABASE_URL") == "" || os.Getenv("FAAS_SKIP_PG_TESTS") != "" {
		t.Skip("DATABASE_URL with SCRAM host auth required")
	}
	catalog := pgtest.OpenMigrated(t)
	verifiers, err := dbcreds.NewVerifierStore(catalog, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	h := newHarnessWithCredentials(t, func(c *Config) { c.PoolMaxServers = 8 }, verifiers)
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	database := uuid.NewString()
	provider, err := firecracker.New(firecracker.Config{
		Database: h.dbName, CredentialKey: []byte(strings.Repeat("a", 64)),
	}, timeoutTestScheduler{admin: h.backend}, verifiers)
	if err != nil {
		t.Fatal(err)
	}
	connect := func(prefix, suffix string) (*pgx.Conn, string) {
		t.Helper()
		role := prefix + strings.ReplaceAll(uuid.NewString()+uuid.NewString(), "-", "")[:40]
		password := uuid.NewString()
		v, err := pgscram.NewVerifier(password, pgscram.DefaultIterations)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = h.backend.Exec(ctx, "CREATE ROLE "+role+" LOGIN PASSWORD '"+v.String()+"'"); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _, _ = h.backend.Exec(context.WithoutCancel(ctx), "DROP ROLE IF EXISTS "+role) })
		if err := verifiers.Put(ctx, database, role, v); err != nil {
			t.Fatal(err)
		}
		conn, err := h.connect(ctx, database+suffix+"."+testSuffix, role, password, h.dbName, "require", nil)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = conn.Close(context.WithoutCancel(ctx)) })
		return conn, role
	}
	serving, servingRole := connect("gregale_rt_", "-pooler")
	reader, readerRole := connect("gregale_ro_", "-ro-pooler")
	migration, _ := connect("gregale_mig_", "")

	getPID := func(conn *pgx.Conn) int32 {
		t.Helper()
		var pid int32
		if err := conn.QueryRow(ctx, "SELECT pg_backend_pid()").Scan(&pid); err != nil {
			t.Fatal(err)
		}
		return pid
	}
	// Wait for a real revision read and establish warm backends.
	waitFor := func(check func() bool) {
		t.Helper()
		for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); {
			if check() {
				return
			}
			time.Sleep(20 * time.Millisecond)
		}
		t.Fatal("pool did not adopt the published revision")
	}
	ready := func(revision int64, idle bool) bool {
		set := h.proxy.pools
		set.mu.Lock()
		defer set.mu.Unlock()
		seen := 0
		for key, pl := range set.pools {
			if key.databaseID != database {
				continue
			}
			pl.mu.Lock()
			ok := pl.revision.revision == revision && time.Now().Before(pl.revision.expiresAt) && (!idle || len(pl.idle) > 0)
			pl.mu.Unlock()
			if !ok {
				return false
			}
			seen++
		}
		return seen == 2
	}
	waitFor(func() bool { return ready(0, false) })
	_ = getPID(serving)
	idlePID := getPID(reader)
	waitFor(func() bool { return ready(0, true) })
	if _, err := serving.Exec(ctx, "BEGIN"); err != nil {
		t.Fatal(err)
	}
	borrowedPID := getPID(serving)
	policy := managedpostgres.BindingTimeoutPolicy{Revision: 2,
		BindingTimeoutDefaults: managedpostgres.BindingTimeoutDefaults{StatementMS: 180, LockMS: 70, IdleInTransactionMS: 450}}
	for _, role := range []string{servingRole, readerRole} {
		if err := provider.ApplyBindingTimeouts(ctx, database, role, policy); err != nil {
			t.Fatal(err)
		}
		if err := provider.ApplyBindingTimeouts(ctx, database, role, managedpostgres.BindingTimeoutPolicy{Revision: 1}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := catalog.Exec(ctx, "UPDATE database_role_verifiers SET pool_revision = 2 WHERE database_id = $1", database); err != nil {
		t.Fatal(err)
	}
	waitFor(func() bool { return ready(2, false) })

	var oldIdleExists bool
	if err := h.backend.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE pid = $1)", idlePID).Scan(&oldIdleExists); err != nil {
		t.Fatal(err)
	}
	if oldIdleExists {
		t.Fatal("old idle backend is still connected")
	}
	if got := getPID(serving); got != borrowedPID {
		t.Fatalf("active transaction switched backends from %d to %d", borrowedPID, got)
	}
	if _, err := serving.Exec(ctx, "SELECT pg_sleep(0.25)"); err != nil {
		t.Fatalf("active transaction was interrupted: %v", err)
	}
	if _, err := serving.Exec(ctx, "COMMIT"); err != nil {
		t.Fatal(err)
	}
	if got := getPID(serving); got == borrowedPID {
		t.Fatal("old backend returned to the idle pool")
	}

	const lock = 15919
	if _, err := h.backend.Exec(ctx, "SELECT pg_advisory_lock($1)", lock); err != nil {
		t.Fatal(err)
	}
	for _, conn := range []*pgx.Conn{serving, reader} {
		if _, err := conn.Exec(ctx, "SELECT pg_sleep(0.35)"); sqlState(err) != "57014" {
			t.Fatalf("statement timeout: %v", err)
		}
		if _, err := conn.Exec(ctx, "SELECT pg_advisory_xact_lock($1)", lock); sqlState(err) != "55P03" {
			t.Fatalf("lock timeout: %v", err)
		}
		if _, err := conn.Exec(ctx, "BEGIN"); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := migration.Exec(ctx, "SELECT pg_sleep(0.6)"); err != nil {
		t.Fatalf("migration inherited an app timeout: %v", err)
	}
	for _, conn := range []*pgx.Conn{serving, reader} {
		if _, err := conn.Exec(ctx, "SELECT 1"); sqlState(err) != "25P03" {
			t.Fatalf("idle transaction timeout: %v", err)
		}
	}
}
