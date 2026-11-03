//go:build !no_pg

package state_test

import "testing"

func TestPgConsumerKeyQuotaHoldsUnderConcurrentIssues(t *testing.T) {
	store, _ := pgStore(t)
	consumerKeyQuotaContract(t, store)
}
