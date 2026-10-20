//go:build !no_pg

package state_test

import (
	"github.com/onebox-faas/faas/pkg/state"
	"testing"
)

func TestPgSourceImagePreparationAtomicCheckpoint(t *testing.T) {
	for _, runtime := range []string{"", "node22"} {
		for _, outcome := range []string{"complete", "stale claim", "revoked publisher", "cancelled"} {
			t.Run(runtime+"/"+outcome, func(t *testing.T) {
				s, _ := state.RegistryVerificationPGStore(t)
				sourceImagePreparationContract(t, s, runtime, outcome)
			})
		}
	}
}
