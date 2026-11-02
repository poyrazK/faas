//go:build !no_pg

package state_test

import "testing"

func TestPgObjectBucketOverlays(t *testing.T) {
	s, _, _ := pgWithPool(t)
	objectBucketOverlays(t, s)
}

func TestPgProjectEnvironmentBranchPrivateNetworks(t *testing.T) {
	s, _, _ := pgWithPool(t)
	projectEnvironmentBranchPrivateNetworks(t, s)
}
