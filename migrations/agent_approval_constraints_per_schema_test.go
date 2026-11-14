//go:build !no_pg

// adr: 20261104140058
package migrations_test

import (
	"testing"

	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

// The agent-approval CHECKs are added only when missing. The guard must look
// at the table in the current schema: a match anywhere in the database (an
// earlier isolated test schema) skipped the ADD, and the later VALIDATE of the
// approval floor then failed with "constraint ... does not exist".
func TestAgentApprovalConstraintsArePerSchema(t *testing.T) {
	ctx := t.Context()
	for i := range 2 {
		pool := pgtest.Open(t)
		migrateUpTo(t, ctx, pool, 20261107131503043)
		for table, constraint := range map[string]string{
			"org_agent_policies":      "org_agent_policies_approval_categories_check",
			"agent_approval_requests": "agent_approval_requests_decision_note_check",
		} {
			var n int
			if err := pool.QueryRow(ctx, `SELECT count(*) FROM pg_constraint WHERE conname = $1 AND conrelid = to_regclass($2)`, constraint, table).Scan(&n); err != nil || n != 1 {
				t.Fatalf("schema %d: %s on %s = %d (%v), want 1", i, constraint, table, n, err)
			}
		}
		pool.Close()
	}
}
