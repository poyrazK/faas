//go:build !no_pg

package state_test

import "testing"

func TestPgProjectEnvironmentBranchLifecycle(t *testing.T) {
	s, _, _ := pgWithPool(t)
	projectEnvironmentBranchLifecycle(t, s)
}
