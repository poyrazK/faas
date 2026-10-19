//go:build !no_pg

// adr: 595 PostgreSQL retains the same deployed capacity contract.
// adr: 590 Deployed environment settings retain their immutable revision.
package sched

import (
	"testing"

	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestPostgresApplicationStandardRefreshRetainsPinnedEnvironmentReplicas(t *testing.T) {
	exerciseStandardEnvironmentRefresh(t, state.NewPgStore(pgtest.OpenMigrated(t)))
}
