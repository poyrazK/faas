//go:build !no_pg

package state_test

import "testing"

func TestPgPipelineCompletionAndAgentObligation(t *testing.T) {
	s, _, _ := pgStoreWithPool(t)
	exercisePipelineCompletion(t, s)
}
