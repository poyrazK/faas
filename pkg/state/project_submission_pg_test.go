//go:build !no_pg

package state_test

import "testing"

func TestProjectSubmissionPostgres(t *testing.T) {
	store, _ := pgStore(t)
	testProjectSubmission(t, store)
}
