package daemonunitspec

import (
	"path/filepath"
	"slices"
	"testing"

	"github.com/onebox-faas/faas/pkg/daemonunit"
)

// apid serves and githubd dials the build-enqueue bridge on one socket, under
// a path both units may write. Without it githubd refuses every push and
// pull-request preview build (bug hunt #8 H8-10), and a bind apid cannot
// make stops apid from starting.
func TestGithubdBridgeSocketIsWiredOnBothUnits(t *testing.T) {
	for name, unit := range map[string]daemonunit.Unit{"apid": UnitApid(), "githubd": UnitGithubd()} {
		var got string
		for _, kv := range unit.Environment {
			if kv.Key == "FAAS_APID_GITHUBD_BRIDGE_SOCK" {
				got = kv.Value
			}
		}
		if got != GithubdBridgeSocket {
			t.Errorf("%s FAAS_APID_GITHUBD_BRIDGE_SOCK = %q, want %q", name, got, GithubdBridgeSocket)
		}
		if dir := filepath.Dir(GithubdBridgeSocket); !slices.Contains(unit.ReadWritePaths, dir) {
			t.Errorf("%s ReadWritePaths %v lacks %s, where the bridge socket lives", name, unit.ReadWritePaths, dir)
		}
	}
}
