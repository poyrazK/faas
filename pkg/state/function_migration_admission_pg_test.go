//go:build !no_pg

package state

// adr: 1663

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/onebox-faas/faas/pkg/runtimeadmission"
)

func TestFunctionMigrationAdmissionPg(t *testing.T) {
	t.Run("lifecycle", func(t *testing.T) { s, _ := runtimeCapturePGStore(t); functionMigrationAdmissionLifecycle(t, s) })
	t.Run("expired lease recovery", func(t *testing.T) { s, _ := runtimeCapturePGStore(t); functionMigrationAdmissionExpiry(t, s) })
	for _, restart := range []bool{false, true} {
		for _, recovery := range []bool{false, true} {
			t.Run(map[bool]string{true: "restart", false: "policy"}[restart]+map[bool]string{true: "/recovery", false: "/capture"}[recovery], func(t *testing.T) {
				s, _ := runtimeCapturePGStore(t)
				functionMigrationAdmissionDrift(t, s, restart, recovery)
			})
		}
	}
}

func TestFunctionMigrationAdmissionPgRenewal(t *testing.T) {
	s, pool := runtimeCapturePGStore(t)
	functionMigrationAdmissionRenewal(t, s, func(g runtimeadmission.FunctionMigrationGrant) {
		raw, err := json.Marshal(g)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := pool.Exec(t.Context(), `UPDATE function_migration_sources SET recovery_grant=$1::jsonb WHERE lease_token=$2`, raw, g.LeaseToken); err != nil {
			t.Fatal(err)
		}
	})
}

func TestFunctionMigrationAdmissionPgAtomicRefusal(t *testing.T) {
	s, _ := runtimeCapturePGStore(t)
	functionMigrationAdmissionAtomicRefusal(t, s)
}

func TestFunctionMigrationAdmissionPgPermissionPairConstraint(t *testing.T) {
	s, pool := runtimeCapturePGStore(t)
	_, req := functionMigrationAdmissionFixture(t, s)
	if _, err := s.IssueFunctionMigrationCapture(t.Context(), req); err != nil {
		t.Fatal(err)
	}
	for _, statement := range []string{
		`UPDATE function_migration_sources SET capture_token=NULL WHERE lease_token=$1`,
		`UPDATE function_migration_sources SET capture_grant=NULL WHERE lease_token=$1`,
		`UPDATE function_migration_sources SET capture_grant=capture_grant-'parent' WHERE lease_token=$1`,
		`UPDATE function_migration_sources SET recovery_grant=capture_grant WHERE lease_token=$1`,
	} {
		_, err := pool.Exec(t.Context(), statement, req.Source.LeaseToken)
		var check *pgconn.PgError
		if !errors.As(err, &check) || check.Code != "23514" || check.ConstraintName != "function_migration_permission_pair" {
			t.Fatal("incomplete or mis-scoped permission escaped the database constraint", err)
		}
	}
}
