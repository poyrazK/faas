//go:build !no_pg

package state_test

import (
	"github.com/google/uuid"
	"testing"
)

func TestPgPipelineProgressAndRerun(t *testing.T) {
	s, pool, ctx := pgStoreWithPool(t)
	exercisePipelineRecovery(t, s, func(id string) {
		tag, err := pool.Exec(ctx, `UPDATE agent_execution_workflows SET status='running',lease_token=$2,lease_owner='old-worker',lease_expires_at=clock_timestamp()+interval '1 minute' WHERE id=$1`, id, uuid.NewString())
		if err != nil || tag.RowsAffected() != 0 {
			t.Fatalf("old worker claimed progress pipeline: %v %v", tag, err)
		}
	})
}
