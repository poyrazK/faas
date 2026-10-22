//go:build !no_pg

package state_test

import (
	"github.com/onebox-faas/faas/pkg/state"
	"testing"
)

func TestProductControlOverviewPostgres(t *testing.T) {
	s := state.XNewPgStore(t)
	productOverviewContract(t, s, state.ProductBillingPGFixture(t, s))
}
