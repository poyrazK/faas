//go:build !no_pg

package promotion_test

import (
	"fmt"
	"testing"

	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/statetest"
)

func TestPgRollbackClearPreservesStagedRelease(t *testing.T) {
	for _, traffic := range []int{0, 100} {
		for _, concurrent := range []bool{false, true} {
			t.Run(fmt.Sprintf("traffic=%d/concurrent=%t", traffic, concurrent), func(t *testing.T) {
				store, hook := state.NewPgStore(pgtest.OpenMigrated(t)), &rollbackReadHook{}
				testRollbackClearPreservesStagedRelease(t, store, statetest.NewPg(&rollbackPgInterleave{PgStore: store, hook: hook}), hook, traffic, concurrent)
			})
		}
	}
}

func TestPgRollbackClearsSoleWeightedRelease(t *testing.T) {
	testRollbackClearsSoleWeightedRelease(t, state.NewPgStore(pgtest.OpenMigrated(t)))
}
