//go:build !no_pg

package globaldirectory

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func TestRuntimeQuotaIssuerRegistryRace(t *testing.T) {
	for _, issuerFirst := range []bool{false, true} {
		name := "registry-first"
		if issuerFirst {
			name = "issuer-first"
		}
		t.Run(name, func(t *testing.T) {
			pool := directoryPG(t)
			store := NewPgStore(pool)
			id := uuid.NewString()
			tx := beginDirectoryTx(t, pool)
			stmt := "INSERT INTO global_directory_cells(id,region,api_base,enabled) VALUES ($1,'eu-fsn','https://fsn.example.com',true)"
			if issuerFirst {
				stmt = "INSERT INTO global_runtime_quota_issuer(issuer_id) VALUES ($1)"
			}
			if _, err := tx.Exec(t.Context(), stmt, id); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			defer cancel()
			result := make(chan error, 1)
			go func() {
				if issuerFirst {
					_, err := store.PutCell(ctx, Cell{ID: id, Region: "eu-fsn", APIBase: "https://fsn.example.com", Enabled: true})
					result <- err
				} else {
					result <- store.BindRuntimeQuotaIssuer(ctx, id)
				}
			}()
			deadline := time.Now().Add(5 * time.Second)
			blocked := false
			for time.Now().Before(deadline) {
				if err := pool.QueryRow(ctx, "SELECT EXISTS (SELECT 1 FROM pg_stat_activity WHERE datname=current_database() AND wait_event_type='Lock' AND pid<>pg_backend_pid())").Scan(&blocked); err != nil {
					t.Fatal(err)
				}
				if blocked {
					break
				}
				time.Sleep(10 * time.Millisecond)
			}
			if !blocked {
				t.Fatal("writer did not wait for issuer scope lock")
			}
			if err := tx.Commit(ctx); err != nil {
				t.Fatal(err)
			}
			if err := <-result; !errors.Is(err, ErrConflict) {
				t.Fatalf("conflicting identities both committed: %v", err)
			}
		})
	}
}

func TestRuntimeQuotaIssuerDowngrade(t *testing.T) {
	m, err := os.ReadFile("../../migrations/20261006153244436_global_runtime_quota_issuer.sql")
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.Split(string(m), "-- +goose Down")
	if len(parts) != 2 {
		t.Fatal("migration sections")
	}
	usageParts := runtimeUsageMigrationParts(t)
	for _, populated := range []bool{false, true} {
		name := "empty"
		if populated {
			name = "retained"
		}
		t.Run(name, func(t *testing.T) {
			pool := directoryPG(t)
			if populated {
				if err := NewPgStore(pool).BindRuntimeQuotaIssuer(t.Context(), uuid.NewString()); err != nil {
					t.Fatal(err)
				}
			}
			// Remove the empty later ledger before its issuer dependency, so
			// the retained case still tests the issuer's own downgrade guard.
			if _, err := pool.Exec(t.Context(), usageParts[1]); err != nil {
				t.Fatal(err)
			}
			_, err := pool.Exec(t.Context(), parts[1])
			if populated {
				if err == nil {
					t.Fatal("retained issuer downgraded")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if _, err := pool.Exec(t.Context(), parts[0]); err != nil {
				t.Fatal(err)
			}
			if _, err := pool.Exec(t.Context(), usageParts[0]); err != nil {
				t.Fatal(err)
			}
			if err := NewPgStore(pool).BindRuntimeQuotaIssuer(t.Context(), uuid.NewString()); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestRuntimeQuotaIssuerPostgres(t *testing.T) {
	pool := directoryPG(t)
	s := NewPgStore(pool)
	exerciseQuotaIssuer(t, s)
	id, err := (&sqlc.Queries{}).ReadGlobalRuntimeQuotaIssuer(t.Context(), pool)
	if err != nil {
		t.Fatal(err)
	}
	if err := NewPgStore(pool).BindRuntimeQuotaIssuer(t.Context(), id); err != nil {
		t.Fatal(err)
	}
	for _, stmt := range []string{
		"UPDATE global_runtime_quota_issuer SET issuer_id=gen_random_uuid()",
		"UPDATE global_runtime_quota_issuer SET singleton=false",
		"DELETE FROM global_runtime_quota_issuer",
		"TRUNCATE global_runtime_quota_issuer",
	} {
		if _, err := pool.Exec(t.Context(), stmt); err == nil {
			t.Fatalf("SQL bypass: %s", stmt)
		}
	}
	if _, err := (&sqlc.Queries{}).InitializeControlPlaneCell(t.Context(), pool, sqlc.InitializeControlPlaneCellParams{CellID: pgtype.UUID{Bytes: uuid.New(), Valid: true}, Region: "eu-fsn"}); err == nil {
		t.Fatal("directory issuer database rebound as cell")
	}
	if _, err := s.PutCell(t.Context(), Cell{ID: id, Region: "eu-hel", APIBase: "https://hel.example.com", Enabled: true}); !errors.Is(err, ErrConflict) {
		t.Fatal(err)
	}
}

func TestRuntimeQuotaIssuerRejectsRegionalDatabase(t *testing.T) {
	pool := directoryPG(t)
	if _, err := (&sqlc.Queries{}).InitializeControlPlaneCell(t.Context(), pool, sqlc.InitializeControlPlaneCellParams{CellID: pgtype.UUID{Bytes: uuid.New(), Valid: true}, Region: "eu-fsn"}); err != nil {
		t.Fatal(err)
	}
	if err := NewPgStore(pool).BindRuntimeQuotaIssuer(t.Context(), uuid.NewString()); !errors.Is(err, ErrBoundCell) {
		t.Fatal(err)
	}
	if _, err := pool.Exec(t.Context(), "INSERT INTO global_runtime_quota_issuer(issuer_id) VALUES ($1)", uuid.NewString()); err == nil {
		t.Fatal("regional SQL bypass")
	}
}
