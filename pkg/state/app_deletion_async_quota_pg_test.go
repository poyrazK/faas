//go:build !no_pg

package state_test

import (
	"testing"

	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state"
)

// adr: 202610171908
func TestPostgresAppDeletionReleasesPooledAsyncQuota(t *testing.T) {
	appDeletionReleasesPooledAsyncQuota(t, func(t *testing.T) state.Store { return state.NewPgStore(pgtest.OpenMigrated(t)) })
}
