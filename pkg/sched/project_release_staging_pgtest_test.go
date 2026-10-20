//go:build !no_pg

// adr: 1351 — exact candidate service allocation, expiry and routing preservation.
package sched_test

import (
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state"
	"testing"
)

func TestProjectReleaseStagingSchedulerPostgres(t *testing.T) {
	exerciseReleaseStagingCancellation(t, state.NewPgStore(pgtest.OpenMigrated(t)))
}
