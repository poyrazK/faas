//go:build !no_pg

package state_test

import "testing"

func TestPgProjectEnvironmentBranchLifecycle(t *testing.T) {
	s, _, _ := pgWithPool(t)
	projectEnvironmentBranchLifecycle(t, s)
}

func TestPgProjectEnvironmentBranchExtend(t *testing.T) {
	s, ctx, pool := pgWithPool(t)
	// A managed secret needs a real binding row here, so the branch's data
	// is put back to pending directly, as a branch waiting for its copies.
	projectEnvironmentBranchExtend(t, s, func(t *testing.T, accountID, projectID, _ string) string {
		t.Helper()
		branch := createExtendBranch(ctx, t, s, accountID, projectID, "pr-2")
		if _, err := pool.Exec(ctx, `update project_environment_branches set data_state = 'pending' where environment_id = $1`, branch.EnvironmentID); err != nil {
			t.Fatal(err)
		}
		return "pr-2"
	})
}

func TestPgProjectEnvironmentBranchSlot(t *testing.T) {
	s, _, _ := pgWithPool(t)
	projectEnvironmentBranchSlot(t, s)
}
