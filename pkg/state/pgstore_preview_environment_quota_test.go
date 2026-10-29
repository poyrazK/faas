//go:build !no_pg

package state_test

import (
	"context"
	"testing"
)

func TestPgPreviewEnvironmentAllowance(t *testing.T) {
	runPreviewEnvironmentAllowanceContract(t, func(t *testing.T) (previewEnvironmentStore, context.Context) {
		store, _, ctx := pgStoreWithPool(t)
		return store, ctx
	})
}
