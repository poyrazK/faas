//go:build !no_pg

// adr: 375
package state_test

import "testing"

func TestPgCloneObjectCredentialPreparationIsAtomicAndRecoverable(t *testing.T) {
	s, _, _ := pgWithPool(t)
	cloneObjectCredentialPreparationContract(t, s)
}
