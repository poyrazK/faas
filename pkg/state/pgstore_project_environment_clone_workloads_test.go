//go:build !no_pg

package state_test

import "testing"

func TestPgProjectEnvironmentCloneCapturesAndPreparesWorkloads(t *testing.T) {
	s, _, _ := pgWithPool(t)
	projectEnvironmentCloneCapturesAndPreparesWorkloads(t, s)
}
