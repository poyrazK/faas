//go:build !no_pg

package state_test

import "testing"

func TestPgInvocationSendsContract(t *testing.T) {
	store, _, _ := pgWithPool(t)
	testInvocationSends(t, store)
}
