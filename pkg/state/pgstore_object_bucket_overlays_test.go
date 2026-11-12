//go:build !no_pg

package state_test

import "testing"

func TestPgObjectBucketOverlays(t *testing.T) {
	s, _, _ := pgWithPool(t)
	objectBucketOverlays(t, s)
}

func TestPgContainedEnvironmentBranchRefusesObjectStorage(t *testing.T) {
	s, _, _ := pgWithPool(t)
	containedEnvironmentBranchRefusesObjectStorage(t, s)
}

func TestPgEnvironmentBranchObjectRelay(t *testing.T) {
	s, _, _ := pgWithPool(t)
	environmentBranchObjectRelay(t, s)
}

func TestPgProjectEnvironmentBranchPrivateNetworks(t *testing.T) {
	s, _, _ := pgWithPool(t)
	projectEnvironmentBranchPrivateNetworks(t, s)
}

func TestPgEnvironmentBranchCaptures(t *testing.T) {
	s, _, _ := pgWithPool(t)
	environmentBranchCaptures(t, s)
}

func TestPgEnvironmentBranchSeed(t *testing.T) {
	s, _, _ := pgWithPool(t)
	environmentBranchSeed(t, s)
}

func TestPgEnvironmentBranchEgress(t *testing.T) {
	s, _, _ := pgWithPool(t)
	environmentBranchEgress(t, s)
}
