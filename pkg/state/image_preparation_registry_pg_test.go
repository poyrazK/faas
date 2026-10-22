//go:build !no_pg

package state_test

import (
	"github.com/onebox-faas/faas/pkg/state"
	"testing"
)

func TestPgRegistryImagePreparationAtomicCheckpoint(t *testing.T) {
	for _, kind := range []string{"app-layer", "full-rootfs"} {
		for _, outcome := range []string{"complete", "stale claim", "revoked publisher"} {
			t.Run(kind+"/"+outcome, func(t *testing.T) {
				s, _ := state.RegistryVerificationPGStore(t)
				registryImagePreparationContract(t, s, kind, outcome)
			})
		}
	}
}
