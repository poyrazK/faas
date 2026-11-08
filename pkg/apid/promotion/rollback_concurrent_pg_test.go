//go:build !no_pg

package promotion_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/statetest"
)

type rollbackPgInterleave struct {
	PgStore *state.PgStore
	hook    *rollbackReadHook
}

func (f *rollbackPgInterleave) DeploymentByID(ctx context.Context, id string) (state.Deployment, error) {
	return f.hook.afterRead(f.PgStore.DeploymentByID(ctx, id))
}

func TestPgRollbackRejectsConcurrentRelease(t *testing.T) {
	for _, ttl := range []int{0, 3600} {
		t.Run(fmt.Sprint(ttl), func(t *testing.T) {
			store := state.NewPgStore(pgtest.OpenMigrated(t))
			hook := &rollbackReadHook{}
			testRollbackRejectsConcurrentRelease(t, store, statetest.NewPg(&rollbackPgInterleave{PgStore: store, hook: hook}), hook, ttl)
		})
	}
}

func TestPgGuardedActivationKeepsScope(t *testing.T) {
	for _, scope := range []string{"default", "production"} {
		t.Run(scope, func(t *testing.T) {
			testGuardedActivationKeepsScope(t, state.NewPgStore(pgtest.OpenMigrated(t)), scope)
		})
	}
}
