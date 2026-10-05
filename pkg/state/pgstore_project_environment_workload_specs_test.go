//go:build !no_pg

// adr: 585
package state_test

import "testing"

func TestPgEnvironmentWorkloadSettingsAreIsolatedAndPinned(t *testing.T) {
	store, _, _ := pgWithPool(t)
	testEnvironmentWorkloadSettings(t, store)
}
