//go:build !no_pg

package globaldirectory

// adr: 657
import (
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/onebox-faas/faas/migrations"
	"github.com/onebox-faas/faas/pkg/api"
)

func TestRegionalAppsDomainPostgresContract(t *testing.T) {
	_, f := postgresQuotaFixture(t, false, 1)
	exerciseAppsDomainRegistry(t, f)
}

func TestRegionalAppsDomainSQLValidationAndRollback(t *testing.T) {
	pool := directoryPG(t)
	id := uuid.NewString()
	if _, err := pool.Exec(t.Context(), "INSERT INTO global_directory_cells(id,region,api_base,enabled) VALUES($1,'eu-fsn','https://api.example.com',true)", id); err != nil {
		t.Fatal(err)
	}
	for _, domain := range []string{"apps.example.com", "", "123.example", "Apps.example.com", "localhost", "127.0.0.1", "001.2.3.4", "https://apps.example.com", "apps.example.com:443", "apps..example.com", "-apps.example.com", "apps-.example.com", "apps.example.com.", "bölgeler.example", strings.Repeat("a", 64) + ".example", strings.Repeat("a", 63) + "." + strings.Repeat("b", 63) + "." + strings.Repeat("c", 63) + "." + strings.Repeat("d", 21)} {
		_, err := pool.Exec(t.Context(), "UPDATE global_directory_cells SET apps_domain=$1 WHERE id=$2", domain, id)
		if valid := domain == "" || api.ValidRegionalAppsDomain(domain); (err == nil) != valid {
			t.Fatalf("SQL/API validation differs for %q: %v", domain, err)
		}
	}
	if _, err := pool.Exec(t.Context(), "UPDATE global_directory_cells SET apps_domain='apps.example.com' WHERE id=$1", id); err != nil {
		t.Fatal(err)
	}
	raw, err := migrations.FS.ReadFile("20261008000100001_regional_apps_domain.sql")
	if err != nil {
		t.Fatal(err)
	}
	down := strings.Split(string(raw), "-- +goose Down")[1]
	tx, err := pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	_, err = tx.Exec(t.Context(), down)
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "23514" {
		t.Fatal("rollback erased registered suffix", err)
	}
	_ = tx.Rollback(t.Context())
	var generation int64
	if err := pool.QueryRow(t.Context(), "UPDATE global_directory_cells SET apps_domain='' WHERE id=$1 RETURNING generation", id).Scan(&generation); err != nil {
		t.Fatal(err)
	}
	tx, err = pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(t.Context()) }()
	if _, err := tx.Exec(t.Context(), down); err != nil {
		t.Fatal("explicitly cleared rollback failed", err)
	}
	var next int64
	if err := tx.QueryRow(t.Context(), "UPDATE global_directory_cells SET enabled=false WHERE id=$1 RETURNING generation", id).Scan(&next); err != nil || next != generation+1 {
		t.Fatal("rollback lost registry protection", next, err)
	}
}
