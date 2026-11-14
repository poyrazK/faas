//go:build !no_pg

package bindings_test

import (
	"os"
	"testing"

	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestPgCheckedReleaseUsesEnvironmentMembership(t *testing.T) {
	testCheckedReleaseUsesEnvironmentMembership(t, state.NewPgStore(pgtest.OpenMigrated(t)))
}

func TestPgProjectReleaseMembershipMigrationReversible(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	raw, err := os.ReadFile("../../../migrations/20261104110832439_checked_project_environment_membership.sql")
	if err != nil {
		t.Fatal(err)
	}
	for _, up := range []bool{false, true, true} {
		if err := pgtest.ExecMigration(t.Context(), t, pool, string(raw), up); err != nil {
			t.Fatalf("membership migration up=%t: %v", up, err)
		}
	}
}
