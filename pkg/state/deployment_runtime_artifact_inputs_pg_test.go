//go:build !no_pg

package state_test

// adr: 435. Producer identity and renewable leases on real PostgreSQL.

import (
	"github.com/onebox-faas/faas/pkg/state"
	"testing"
)

func TestPgRuntimeArtifactInputsRenewal(t *testing.T) {
	s, _ := state.RegistryVerificationPGStore(t)
	state.RuntimeArtifactInputRenewal(t, s)
}

func TestPgRuntimeArtifactInputsTwoDrives(t *testing.T) {
	s, _ := state.RegistryVerificationPGStore(t)
	state.RuntimeArtifactInputsTwoDrives(t, s)
}

func TestPgRuntimeArtifactInputsSidecars(t *testing.T) {
	s, _ := state.RegistryVerificationPGStore(t)
	state.RuntimeArtifactInputsSidecars(t, s)
}

func TestPgRuntimeArtifactInputsDatabaseDeadline(t *testing.T) {
	s, _ := state.RegistryVerificationPGStore(t)
	state.RuntimeArtifactInputDatabaseDeadline(t, s)
}
