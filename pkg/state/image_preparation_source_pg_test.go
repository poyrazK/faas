//go:build !no_pg

package state

import "testing"

func TestPgSourceImagePreparationAtomicCheckpoint(t *testing.T) {
	for _, runtime := range []string{"", "node22"} {
		for _, outcome := range []string{"complete", "stale claim", "revoked publisher", "cancelled"} {
			t.Run(runtime+"/"+outcome, func(t *testing.T) {
				s, _ := registryVerificationPGStore(t)
				sourceImagePreparationContract(t, s, runtime, outcome)
			})
		}
	}
}
