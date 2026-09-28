//go:build !no_pg

package state_test

import "testing"

func TestPgProjectEnvironmentQualificationContract(t *testing.T) {
	store, _, _ := pgStoreWithPool(t)
	testProjectEnvironmentQualificationContract(t, store)
}
