//go:build !no_pg

package state_test

import "testing"

func TestPendingDeploymentCronsPostgres(t *testing.T) {
	store, _ := pgStore(t)
	testPendingDeploymentCrons(t, store)
}
