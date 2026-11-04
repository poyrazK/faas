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

func TestPgEnvironmentBranchCaptures(t *testing.T) {
	s, _, _ := pgWithPool(t)
	environmentBranchCaptures(t, s)
}

func TestPgEnvironmentBranchEgress(t *testing.T) {
	s, _, _ := pgWithPool(t)
	environmentBranchEgress(t, s)
}
