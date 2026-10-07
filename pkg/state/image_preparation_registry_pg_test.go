//go:build !no_pg

package state

import "testing"

func TestPgRegistryImagePreparationAtomicCheckpoint(t *testing.T) {
	for _, kind := range []string{"app-layer", "full-rootfs"} {
		for _, outcome := range []string{"complete", "stale claim", "revoked publisher"} {
			t.Run(kind+"/"+outcome, func(t *testing.T) {
				s, _ := registryVerificationPGStore(t)
				registryImagePreparationContract(t, s, kind, outcome)
			})
		}
	}
}
