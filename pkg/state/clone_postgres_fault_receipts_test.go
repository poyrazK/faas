//go:build !no_pg

package state_test

import (
	"encoding/json"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	statesql "github.com/onebox-faas/faas/pkg/state/sqlc"
)

// Exercise the production borrower query against an isolated receipt projection.
// The real admission table and its recovery guards remain untouched.
func TestPgCloneDeploymentBorrowersCountPostgresFaultReceipts(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	tx, err := pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(t.Context())
	if _, err = tx.Exec(t.Context(), "CREATE TEMP TABLE scenario_postgres_faults (phase text NOT NULL, receipt jsonb NOT NULL) ON COMMIT DROP"); err != nil {
		t.Fatal(err)
	}
	var target, source, operation pgtype.UUID
	for _, id := range []*pgtype.UUID{&target, &source, &operation} {
		if err = id.Scan(uuid.NewString()); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = tx.Exec(t.Context(), `INSERT INTO pg_temp.scenario_postgres_faults VALUES
 ('failed',jsonb_build_object('deployment_id',$1::uuid::text)),
 ('recovered',jsonb_build_object('deployment_id',$2::uuid::text)),
 ('failed',jsonb_build_object('deployment_id',$2::uuid::text))`, target, source); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name string
		id   pgtype.UUID
		want int64
	}{{"target", target, 1}, {"source", source, 2}} {
		t.Run(tc.name, func(t *testing.T) {
			raw, err := statesql.New().ReadCloneDeploymentSoftBorrowers(t.Context(), tx, statesql.ReadCloneDeploymentSoftBorrowersParams{DeploymentID: tc.id, OperationID: operation})
			if err != nil {
				t.Fatal(err)
			}
			var counts map[string]int64
			if err = json.Unmarshal(raw, &counts); err != nil {
				t.Fatal(err)
			}
			if got := counts["scenario_postgres_faults.receipt.deployment_id"]; got != tc.want {
				t.Fatalf("original %s receipt count=%d, want %d", tc.name, got, tc.want)
			}
		})
	}
}
