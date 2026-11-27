//go:build !no_pg

package state_test

import "testing"

func TestPgRunEnvironmentAdmissionRetirementAndGC(t *testing.T) {
	store, _, _ := pgStoreWithPool(t)
	exerciseEnvironmentLifecycle(t, store)
}

func TestPgExecutionClassClaimsStayInTheirLane(t *testing.T) {
	store, _, _ := pgStoreWithPool(t)
	exerciseClassClaims(t, store)
}

func TestPgSystemPackageEnvironmentUsesVersionedWorkerLane(t *testing.T) {
	store, _, _ := pgStoreWithPool(t)
	exerciseSystemEnvironmentLane(t, store)
}
