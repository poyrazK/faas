//go:build !no_pg

// adr: 1157 — exact candidate service allocation, expiry and routing preservation.
package sched

import (
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state"
	"testing"
)

func TestProjectReleaseStagingSchedulerPostgres(t *testing.T) {
	exerciseReleaseStagingCancellation(t, state.NewPgStore(pgtest.OpenMigrated(t)))
}
