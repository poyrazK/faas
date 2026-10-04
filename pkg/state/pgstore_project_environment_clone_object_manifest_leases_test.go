//go:build !no_pg

// adr: 567
package state_test

import "testing"

func TestPgCloneObjectManifestRejectsLostWorkerAuthority(t *testing.T) {
	store, _, _ := pgWithPool(t)
	cloneObjectManifestLeaseContract(t, store)
}
