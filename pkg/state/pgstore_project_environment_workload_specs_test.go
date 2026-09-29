//go:build !no_pg

// adr: 375
package state_test

import "testing"

func TestPgEnvironmentWorkloadSettingsAreIsolatedAndPinned(t *testing.T) {
	store, _, _ := pgWithPool(t)
	testEnvironmentWorkloadSettings(t, store)
}
