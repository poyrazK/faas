//go:build !no_pg

package state_test

import (
	"testing"

	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state"
)

// adr: 520
func TestPgWildcardAllowlistEndsWithItsClaim(t *testing.T) {
	wildcardAllowlistEndsWithItsClaim(t, state.NewPgStore(pgtest.OpenMigrated(t)))
}
