//go:build !no_pg

package state

// adr: 393

import "testing"

func TestPgRegistryImageChainPublication(t *testing.T) {
	s, _ := registryVerificationPGStore(t)
	registryImageChainPublication(t, s)
}
func TestPgRegistryImageChainMetadata(t *testing.T) {
	s, _ := registryVerificationPGStore(t)
	registryImageChainRejectsTamperedMetadata(t, s)
}
