//go:build !no_pg

// adr: 569
package state_test

import "testing"

func TestPgEnvironmentQueueDeliveryClassesAndReceipts(t *testing.T) {
	store, _, _ := pgWithPool(t)
	testEnvironmentQueueDeliveryClassesAndReceipts(t, store)
}

func TestPgEnvironmentQueueDeliveryScopeAndCapacity(t *testing.T) {
	store, _, _ := pgWithPool(t)
	testEnvironmentQueueDeliveryScopeAndCapacity(t, store)
}

func TestPgEnvironmentQueueDeliveryLeaseRecoveryAndRetirement(t *testing.T) {
	store, _, _ := pgWithPool(t)
	testEnvironmentQueueDeliveryLeaseRecoveryAndRetirement(t, store)
}

func TestPgEnvironmentQueueDeliveryLeaseTimeoutsHonorAttemptBudget(t *testing.T) {
	store, _, _ := pgWithPool(t)
	testEnvironmentQueueDeliveryLeaseTimeoutsHonorAttemptBudget(t, store)
}
