//go:build !no_pg

package state_test

import "testing"

func TestPgProjectEnvironmentQualificationContract(t *testing.T) {
	store, _, _ := pgStoreWithPool(t)
	testProjectEnvironmentQualificationContract(t, store)
}

// adr: 590
func TestPgQualificationPinsWorkloadConfigurations(t *testing.T) {
	store, _, _ := pgStoreWithPool(t)
	testQualificationPinsWorkloadConfigurations(t, store)
}

// adr: 1139
func TestPgQualificationRecordsProjectSecrets(t *testing.T) {
	store, _, _ := pgStoreWithPool(t)
	testQualificationRecordsProjectSecrets(t, store)
}
