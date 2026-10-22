//go:build !no_pg

package state_test

// adr: 435

import (
	"github.com/onebox-faas/faas/pkg/state"
	"testing"
)

func TestPgRegistryImageChainPublication(t *testing.T) {
	s, _ := state.RegistryVerificationPGStore(t)
	state.RegistryImageChainPublication(t, s)
}
func TestPgRegistryImageChainMetadata(t *testing.T) {
	s, _ := state.RegistryVerificationPGStore(t)
	state.RegistryImageChainRejectsTamperedMetadata(t, s)
}
