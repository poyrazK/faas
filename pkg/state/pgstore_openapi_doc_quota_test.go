//go:build !no_pg

package state_test

import "testing"

func TestPgOpenAPIDocQuotaHoldsUnderConcurrentCaptures(t *testing.T) {
	store, _ := pgStore(t)
	openAPIDocQuotaContract(t, store)
}
