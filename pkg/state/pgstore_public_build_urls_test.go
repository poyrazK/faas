//go:build !no_pg

package state_test

import "testing"

func TestPgPublicBuildURLSnapshots(t *testing.T) {
	s, _, _ := pgStoreWithPool(t)
	testPublicBuildURLSnapshots(t, s)
}
