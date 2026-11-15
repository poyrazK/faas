//go:build !no_pg

package state_test

import "testing"

func TestPgWorkflowRunStartsContract(t *testing.T) {
	store, _, _ := pgWithPool(t)
	testWorkflowRunStarts(t, store)
}
