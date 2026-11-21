//go:build !no_pg

package state_test

import "testing"

func TestPgExecutionPipelineFences(t *testing.T) {
	s, _, _ := pgStoreWithPool(t)
	exerciseExecutionPipelineFences(t, s)
}
