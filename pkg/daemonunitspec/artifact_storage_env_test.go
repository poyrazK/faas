package daemonunitspec_test

// Tests for ArtifactStorageEnvNames — the set of contract rows a caller must
// forward to give a daemon the same artifact store the node's own units use.
//
// The bug these pin: the native e2e harness boots daemons as subprocesses
// with an explicit environment. Naming no storage variable does not mean
// "inherit"; it means pkg/storage takes its local default rooted at /srv/fc.
// On an OCI-backed node that silently points the harness at a directory
// imaged never wrote to, and the first symptom is vmmd's issue #299 gate
// refusing a cold boot with "scan sidecar missing" — for a sidecar that
// exists, in the registry, CRITICAL-clean.

import (
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/daemonunitspec"
)

func TestArtifactStorageEnvNames_SelectsBackendRoutingAndCredentials(t *testing.T) {
	got := make(map[string]bool)
	for _, name := range daemonunitspec.ArtifactStorageEnvNames() {
		got[name] = true
	}

	// Without the backend selector a forwarded environment cannot move the
	// daemon off the local default at all, which is the whole point.
	// Credentials matter just as much: an OCI backend that cannot
	// authenticate fails later and less legibly than one never configured.
	for _, want := range []string{
		"FAAS_STORAGE_BACKEND",
		"FAAS_STORAGE_ROOT",
		"FAAS_STORAGE_CACHE_DIR",
		"FAAS_STORAGE_LOCAL_PREFIXES",
		"FAAS_OCI_REGISTRY",
		"FAAS_OCI_REPO_PREFIX",
		"FAAS_OCI_USERNAME",
		"FAAS_OCI_PASSWORD",
	} {
		if !got[want] {
			t.Errorf("ArtifactStorageEnvNames() is missing %s; a forwarded "+
				"environment without it cannot reproduce the node's storage route", want)
		}
	}
}

func TestArtifactStorageEnvNames_ExcludesDevOnlyAndUnrelatedRows(t *testing.T) {
	got := make(map[string]bool)
	for _, name := range daemonunitspec.ArtifactStorageEnvNames() {
		got[name] = true
	}

	tests := []struct {
		name string
		why  string
	}{
		{
			name: "FAAS_OCI_INSECURE",
			why:  "dev-only: forwarding it would let a harness downgrade registry transport security",
		},
		{
			name: "FAAS_STORAGE_ROLLUP_INTERVAL",
			why:  "meterd's billing rollup cadence — shares the prefix, configures nothing about artifacts",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got[tc.name] {
				t.Errorf("ArtifactStorageEnvNames() returned %s; %s", tc.name, tc.why)
			}
		})
	}
}

// Every returned name must be a real contract row. A typo here would forward
// an always-empty variable and reintroduce the silent-local-default bug while
// looking correct at the call site.
func TestArtifactStorageEnvNames_AllRowsAreDeclaredInTheContract(t *testing.T) {
	declared := daemonunitspec.EnvContractByName()
	names := daemonunitspec.ArtifactStorageEnvNames()
	if len(names) == 0 {
		t.Fatal("ArtifactStorageEnvNames() returned nothing; the contract should declare storage rows")
	}
	for _, name := range names {
		if _, ok := declared[name]; !ok {
			t.Errorf("ArtifactStorageEnvNames() returned %q, which is not in EnvContract", name)
		}
		if !strings.HasPrefix(name, "FAAS_STORAGE_") && !strings.HasPrefix(name, "FAAS_OCI_") {
			t.Errorf("ArtifactStorageEnvNames() returned %q, outside the storage/OCI prefixes", name)
		}
	}
}
