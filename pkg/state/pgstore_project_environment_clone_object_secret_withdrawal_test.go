//go:build !no_pg

// adr: 590
package state_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/onebox-faas/faas/migrations"
	"github.com/onebox-faas/faas/pkg/api"
)

func TestPgCloneObjectSecretWithdrawalMigrationGuards(t *testing.T) {
	s, pool, l, _ := configurationOwnershipFixture(t)
	raw, err := migrations.FS.ReadFile("20261011232006406_clone_object_secret_withdrawal_receipts.sql")
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.SplitN(string(raw), "-- +goose Down", 2)
	tx, err := pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(t.Context()) }()
	for _, sql := range []string{parts[1], parts[0], parts[1], parts[0]} {
		if _, err = tx.Exec(t.Context(), sql); err != nil {
			t.Fatal("empty downgrade/replay", err)
		}
	}
	_ = tx.Rollback(t.Context())
	limits, _ := api.LimitsFor(api.PlanPro)
	if _, err = s.MaterializeProjectEnvironmentCloneForLease(t.Context(), l, nil, 0, limits); err != nil {
		t.Fatal(err)
	}
	// Retained evidence must block Down regardless of its observation validity.
	if _, err = pool.Exec(t.Context(), "INSERT INTO project_environment_clone_object_secret_withdrawals(operation_id,ownership_sha256,postgres_withdrawal_sha256,prior_manifest_sha256,publication_plan_sha256,publication_withdrawn_at,manifest,manifest_sha256) SELECT operation_id,manifest_sha256,'',manifest_sha256,manifest_sha256,clock_timestamp(),'{}',manifest_sha256 FROM project_environment_clone_configuration_ownership WHERE operation_id=$1", l.Operation.ID); err != nil {
		t.Fatal(err)
	}
	tx, err = pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(t.Context()) }()
	_, err = tx.Exec(t.Context(), parts[1])
	var pgerr *pgconn.PgError
	if !errors.As(err, &pgerr) || pgerr.ConstraintName != "clone_object_secret_withdrawal_down_no_evidence" {
		t.Fatal("retained evidence discarded", err)
	}
}
