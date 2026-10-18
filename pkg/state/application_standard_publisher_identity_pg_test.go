//go:build !no_pg

package state

import "testing"

func TestPgApplicationStandardPublisherInheritance(t *testing.T) {
	s, _ := registryVerificationPGStore(t)
	standardPublisherInheritance(t, s)
}
func TestPgApplicationStandardPublisherSourceAlias(t *testing.T) {
	s, _ := registryVerificationPGStore(t)
	standardPublisherSourceAlias(t, s)
}
func TestPgApplicationStandardPublisherRegistryAlias(t *testing.T) {
	s, _ := registryVerificationPGStore(t)
	standardPublisherRegistryAlias(t, s)
}
