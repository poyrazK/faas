//go:build !no_pg

package state_test

import (
	"github.com/onebox-faas/faas/pkg/state"
	"testing"
)

func TestPgApplicationStandardPublisherInheritance(t *testing.T) {
	s, _ := state.RegistryVerificationPGStore(t)
	state.StandardPublisherInheritance(t, s)
}
func TestPgApplicationStandardPublisherSourceAlias(t *testing.T) {
	s, _ := state.RegistryVerificationPGStore(t)
	state.StandardPublisherSourceAlias(t, s)
}
func TestPgApplicationStandardPublisherRegistryAlias(t *testing.T) {
	s, _ := state.RegistryVerificationPGStore(t)
	state.StandardPublisherRegistryAlias(t, s)
}
