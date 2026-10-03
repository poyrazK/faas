package db

import (
	"context"
	"errors"
	"net/url"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/migrations"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func cellDatabase(t *testing.T) *pgxpool.Pool {
	t.Helper()
	pool := pgtest.OpenMigrated(t)
	if err := MigrateUp(t.Context(), pool); err != nil {
		t.Fatal(err)
	}
	return pool
}

// pgx retains the original DSN even after pgtest changes Database/search_path.
// Reconstruct those fields so Open tests connect to the actual private fixture.
func cellDatabaseDSN(t *testing.T, pool *pgxpool.Pool) string {
	t.Helper()
	cfg := pool.Config().ConnConfig
	u, err := url.Parse(cfg.ConnString())
	if err != nil || (u.Scheme != "postgres" && u.Scheme != "postgresql") {
		t.Fatalf("cell fixture requires PostgreSQL URL: %v", err)
	}
	u.Path = "/" + cfg.Database
	q := u.Query()
	if searchPath := cfg.RuntimeParams["search_path"]; searchPath != "" {
		q.Set("search_path", searchPath)
	}
	u.RawQuery = q.Encode()
	return u.String()
}

func initializeTestCell(ctx context.Context, pool *pgxpool.Pool, cell CellIdentity) error {
	id := pgtype.UUID{Bytes: uuid.MustParse(cell.ID), Valid: true}
	_, err := (&sqlc.Queries{}).InitializeControlPlaneCell(ctx, pool, sqlc.InitializeControlPlaneCellParams{CellID: id, Region: cell.Region})
	return err
}

func expectCell(t *testing.T, cell CellIdentity) {
	t.Helper()
	t.Setenv(ControlPlaneCellIDEnv, cell.ID)
	t.Setenv(ControlPlaneCellRegionEnv, cell.Region)
	// Each test opts into a direct sibling explicitly.
	t.Setenv(DirectDSNEnv, "")
}

func TestControlPlaneCellIndependentDatabasesAndPools(t *testing.T) {
	a, b := cellDatabase(t), cellDatabase(t)
	cellA := CellIdentity{ID: uuid.NewString(), Region: "eu-fsn"}
	cellB := CellIdentity{ID: uuid.NewString(), Region: "eu-hel"}
	for _, pair := range []struct {
		pool *pgxpool.Pool
		cell CellIdentity
	}{{a, cellA}, {b, cellB}} {
		if err := initializeTestCell(t.Context(), pair.pool, pair.cell); err != nil {
			t.Fatal(err)
		}
	}
	expectCell(t, cellA)
	dsnA, dsnB := cellDatabaseDSN(t, a), cellDatabaseDSN(t, b)
	for _, tc := range []struct {
		name, ordinary, direct string
		wantError              bool
	}{
		{"own cell", dsnA, "", false},
		{"own session sibling", dsnA, dsnA, false},
		{"foreign ordinary", dsnB, "", true},
		{"foreign session sibling", dsnA, dsnB, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(DirectDSNEnv, tc.direct)
			pool, err := OpenWithAppName(t.Context(), tc.ordinary, "faas-schedd")
			if tc.wantError {
				if pool != nil || !errors.Is(err, ErrControlPlaneCell) {
					t.Fatalf("pool=%v err=%v", pool, err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			defer Close(pool)
			if err := MigrateUp(t.Context(), pool); err != nil {
				t.Fatalf("cell migration connection: %v", err)
			}
			if _, err := Status(t.Context(), pool); err != nil {
				t.Fatalf("cell migration status: %v", err)
			}
			// The separate Goose shim must retain the same identity hooks.
			wrong := pool.Config().Copy()
			wrong.ConnConfig.RuntimeParams["search_path"] = "pg_catalog"
			shim := migrationSQLDB(wrong)
			if err := shim.PingContext(t.Context()); !errors.Is(err, ErrControlPlaneCell) {
				t.Fatalf("migration shim bypassed identity: %v", err)
			}
			if err := shim.Close(); err != nil {
				t.Fatal(err)
			}
			reusedShim := migrationSQLDB(pool.Config())
			reusedShim.SetMaxOpenConns(1)
			reusedShim.SetMaxIdleConns(1)
			if _, err := reusedShim.ExecContext(t.Context(), "SET search_path TO pg_catalog"); err != nil {
				t.Fatal(err)
			}
			var identityVisible bool
			err = reusedShim.QueryRowContext(t.Context(), "SELECT to_regclass('control_plane_cells') IS NOT NULL").Scan(&identityVisible)
			if err != nil || !identityVisible {
				t.Fatalf("migration reset allowed foreign caller SQL: visible=%t err=%v", identityVisible, err)
			}
			if err := reusedShim.Close(); err != nil {
				t.Fatal(err)
			}
			// Reopening connections must preserve the guard on both pools.
			pool.Reset()
			DirectPool(pool).Reset()
			if err := pool.Ping(t.Context()); err != nil {
				t.Fatal(err)
			}
			if err := DirectPool(pool).Ping(t.Context()); err != nil {
				t.Fatal(err)
			}
			// Simulate reused frontend connections resolving a different schema.
			// The cell read must run again before the next query reaches a caller.
			for _, probe := range []*pgxpool.Pool{pool, DirectPool(pool)} {
				conn, err := probe.Acquire(t.Context())
				if err != nil {
					t.Fatal(err)
				}
				if _, err := conn.Exec(t.Context(), "SET search_path TO pg_catalog"); err != nil {
					t.Fatal(err)
				}
				conn.Release()
				if err := probe.Ping(t.Context()); !errors.Is(err, ErrControlPlaneCell) {
					t.Fatalf("reused connection bypassed guard: %v", err)
				}
				probe.Reset()
			}
		})
	}
	// Region is checked independently of UUID, and cell B can open itself.
	t.Setenv(ControlPlaneCellRegionEnv, cellB.Region)
	if pool, err := Open(t.Context(), dsnA); pool != nil || !errors.Is(err, ErrControlPlaneCell) {
		t.Fatalf("wrong region: pool=%v err=%v", pool, err)
	}
	expectCell(t, cellB)
	pool, err := OpenReadOnlyWithAppName(t.Context(), dsnB, "faas-schedd")
	if err != nil {
		t.Fatal(err)
	}
	Close(pool)
}

func TestControlPlaneCellLegacyAndMissingConfiguration(t *testing.T) {
	pool := cellDatabase(t)
	dsn := cellDatabaseDSN(t, pool)
	expectCell(t, CellIdentity{})
	legacy, err := Open(t.Context(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	Close(legacy)
	cell := CellIdentity{ID: uuid.NewString(), Region: "eu-fsn"}
	expectCell(t, cell)
	if got, err := Open(t.Context(), dsn); got != nil || !errors.Is(err, ErrControlPlaneCell) {
		t.Fatalf("unbound database accepted cell process: %v", err)
	}
	if err := initializeTestCell(t.Context(), pool, cell); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []CellIdentity{{}, {ID: cell.ID}, {Region: cell.Region}, {ID: "bad", Region: cell.Region}, {ID: uuid.Nil.String(), Region: cell.Region}, {ID: cell.ID, Region: "EU-FSN"}} {
		expectCell(t, bad)
		if got, err := Open(t.Context(), dsn); got != nil || !errors.Is(err, ErrControlPlaneCell) {
			t.Fatalf("bad configuration %+v: %v", bad, err)
		}
	}
	// Pre-migration ordinary databases still open without cell configuration.
	expectCell(t, CellIdentity{})
	fresh := pgtest.Open(t)
	legacy, err = Open(t.Context(), cellDatabaseDSN(t, fresh))
	if err != nil {
		t.Fatal(err)
	}
	Close(legacy)
}

func requireCellConstraint(t *testing.T, err error, constraint string) {
	t.Helper()
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "23514" || pgErr.ConstraintName != constraint {
		t.Fatalf("want %s; got %v", constraint, err)
	}
}

func TestControlPlaneCellInitializationAndGuards(t *testing.T) {
	pool := cellDatabase(t)
	cell := CellIdentity{ID: uuid.NewString(), Region: "eu-fsn"}
	if err := initializeTestCell(t.Context(), pool, cell); err != nil {
		t.Fatal(err)
	}
	if err := initializeTestCell(t.Context(), pool, cell); err != nil {
		t.Fatalf("idempotent init: %v", err)
	}
	// Replaying the additive migration must preserve a bound identity, while
	// the actual rollback refuses to remove it.
	migration, err := migrations.FS.ReadFile("20261003143601470_regional_control_plane_cell.sql")
	if err != nil {
		t.Fatal(err)
	}
	up, down, found := strings.Cut(string(migration), "-- +goose Down")
	if !found {
		t.Fatal("migration has no rollback")
	}
	if _, err := pool.Exec(t.Context(), up); err != nil {
		t.Fatalf("replay: %v", err)
	}
	if _, err := pool.Exec(t.Context(), down); err == nil || !strings.Contains(err.Error(), "cannot downgrade") {
		t.Fatalf("bound downgrade: %v", err)
	}
	requireCellConstraint(t, initializeTestCell(t.Context(), pool, CellIdentity{ID: uuid.NewString(), Region: cell.Region}), "control_plane_cell_identity")
	for _, query := range []string{
		"UPDATE control_plane_cells SET compute_region='eu-hel'",
		"UPDATE control_plane_cells SET id=gen_random_uuid()",
		"DELETE FROM control_plane_cells", "TRUNCATE control_plane_cells",
	} {
		_, err := pool.Exec(t.Context(), query)
		requireCellConstraint(t, err, "control_plane_cell_immutable")
	}
	for _, table := range []string{"apps", "projects", "jobs"} {
		_, err := pool.Exec(t.Context(), "INSERT INTO "+table+" (compute_region) VALUES ('eu-hel')")
		requireCellConstraint(t, err, "control_plane_cell_region")
	}
	nodeSQL := `INSERT INTO compute_nodes (name,target_url,vpcpus,mem_mb,max_concurrency,admission_ceiling_mb,region)
		VALUES ($1,'tcp://localhost:50051',2,1024,2,900,$2)`
	for _, region := range []any{nil, "", "eu-hel"} {
		_, err := pool.Exec(t.Context(), nodeSQL, "foreign", region)
		requireCellConstraint(t, err, "control_plane_cell_region")
	}
	if _, err := pool.Exec(t.Context(), nodeSQL, "own", cell.Region); err != nil {
		t.Fatal(err)
	}
	_, err = pool.Exec(t.Context(), "UPDATE compute_nodes SET region='eu-hel' WHERE name='own'")
	requireCellConstraint(t, err, "control_plane_cell_region")
	var count int
	if err := pool.QueryRow(t.Context(), "SELECT count(*) FROM compute_nodes WHERE name='default-local'").Scan(&count); err != nil || count != 0 {
		t.Fatalf("synthetic capacity remains: count=%d err=%v", count, err)
	}
}

func TestControlPlaneCellRefusesPopulatedDatabase(t *testing.T) {
	for _, query := range []string{
		"INSERT INTO accounts(email,plan) VALUES ('cell-test@example.com','free')",
		"UPDATE compute_nodes SET last_heartbeat_at=created_at+interval '1 second'",
		"UPDATE compute_nodes SET region=NULL",
		"UPDATE compute_nodes SET target_url='tcp://localhost:50051'",
	} {
		t.Run(query, func(t *testing.T) {
			pool := cellDatabase(t)
			if _, err := pool.Exec(t.Context(), query); err != nil {
				t.Fatal(err)
			}
			err := initializeTestCell(t.Context(), pool, CellIdentity{ID: uuid.NewString(), Region: "eu-fsn"})
			requireCellConstraint(t, err, "control_plane_cell_fresh")
			var count int
			if err := pool.QueryRow(t.Context(), "SELECT count(*) FROM control_plane_cells").Scan(&count); err != nil || count != 0 {
				t.Fatalf("failed init left identity: count=%d err=%v", count, err)
			}
		})
	}
}

func TestControlPlaneCellConcurrentInitialization(t *testing.T) {
	pool := cellDatabase(t)
	cells := []CellIdentity{{ID: uuid.NewString(), Region: "eu-fsn"}, {ID: uuid.NewString(), Region: "eu-hel"}}
	results := make(chan error, len(cells))
	var wg sync.WaitGroup
	start := make(chan struct{})
	ctx := t.Context()
	for _, cell := range cells {
		wg.Go(func() {
			<-start
			results <- initializeTestCell(ctx, pool, cell)
		})
	}
	close(start)
	wg.Wait()
	close(results)
	success := 0
	for err := range results {
		if err == nil {
			success++
		} else if !strings.Contains(err.Error(), "different control plane cell") {
			t.Fatal(err)
		}
	}
	if success != 1 {
		t.Fatalf("concurrent winners=%d", success)
	}
}
