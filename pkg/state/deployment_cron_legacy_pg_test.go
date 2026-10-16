//go:build !no_pg

package state_test

import "testing"

func TestLegacyDeploymentCronOwnershipPG(t *testing.T) {
	store, _ := pgStore(t)
	testLegacyCronOwnership(t, store)
}
