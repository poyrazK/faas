//go:build !no_pg

package state

import "testing"

func TestProductControlOverviewPostgres(t *testing.T) {
	s := newPgStore(t)
	productOverviewContract(t, s, productBillingPGFixture(t, s))
}
