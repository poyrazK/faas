//go:build !no_pg

package migrations_test

// adr: 595

import (
	"errors"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

func TestMigrations_StandardSnapshotConsumptionCapabilities(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	if err := db.MigrateUp(t.Context(), pool); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, input string
		allowed     bool
	}{
		{"ordinary legacy", `{"persisted_revision":0,"adoptions":[],"materialized_fields":[],"runtime_artifacts":{"artifacts":[]}}`, true},
		{"source layer needs measured capability", `{"runtime_artifacts":{"artifacts":[{"kind":"source-app-layer"}]}}`, false},
		{"function layer needs measured capability", `{"runtime_artifacts":{"artifacts":[{"kind":"function-layer"}]}}`, false},
		{"retained controls need measured capability", `{"persisted_revision":1,"adoptions":[],"materialized_fields":[],"runtime_artifacts":{"artifacts":[]}}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := pool.Exec(t.Context(), `SELECT application_standard_native_artifact_protocol('{"protocol_version":1}'::jsonb,NULL,$1::jsonb,1::smallint)`, tc.input)
			if tc.allowed {
				if err != nil {
					t.Fatal("ordinary legacy contract changed", err)
				}
				return
			}
			var pgErr *pgconn.PgError
			if !errors.As(err, &pgErr) || pgErr.Code != "23514" || pgErr.ConstraintName != "application_standard_runtime_stale" {
				t.Fatal("native capability downgrade accepted", err)
			}
		})
	}
}
