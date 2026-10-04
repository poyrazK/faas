//go:build !no_pg

// adr: 531
package state_test

import "testing"

func TestPgCloneCapturesEffectiveProductionValueScopes(t *testing.T) {
	store, _, _ := pgWithPool(t)
	testCloneEffectiveProductionValueScopes(t, store)
}
