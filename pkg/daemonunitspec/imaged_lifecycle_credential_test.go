package daemonunitspec

import (
	"strings"
	"testing"
)

func TestSnapshotLifecycleDaemonsLoadLifecycleCredential(t *testing.T) {
	const lifecycleFile = "/etc/faas/imaged-storage.env"
	lifecycleDaemons := map[string]bool{"imaged": true, "vmmd": true}
	for _, entry := range UnitEntries() {
		hasLifecycleCredential := strings.Contains(entry.Unit().EnvironmentFile, lifecycleFile)
		if lifecycleDaemons[entry.Name] && !hasLifecycleCredential {
			t.Errorf("%s does not load %s", entry.Name, lifecycleFile)
		}
		if !lifecycleDaemons[entry.Name] && hasLifecycleCredential {
			t.Errorf("%s unexpectedly receives package deletion authority", entry.Name)
		}
	}
}
