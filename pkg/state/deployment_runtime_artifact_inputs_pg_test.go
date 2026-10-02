//go:build !no_pg

package state

// adr: 429. Producer identity and renewable leases on real PostgreSQL.

import "testing"

func TestPgRuntimeArtifactInputsRenewal(t *testing.T) {
	s, _ := registryVerificationPGStore(t)
	runtimeArtifactInputRenewal(t, s)
}

func TestPgRuntimeArtifactInputsTwoDrives(t *testing.T) {
	s, _ := registryVerificationPGStore(t)
	runtimeArtifactInputsTwoDrives(t, s)
}

func TestPgRuntimeArtifactInputsSidecars(t *testing.T) {
	s, _ := registryVerificationPGStore(t)
	runtimeArtifactInputsSidecars(t, s)
}

func TestPgRuntimeArtifactInputsDatabaseDeadline(t *testing.T) {
	s, _ := registryVerificationPGStore(t)
	runtimeArtifactInputDatabaseDeadline(t, s)
}
