package daemonunitspec

import (
	"strings"
	"testing"
)

func TestImagedAloneLoadsLifecycleCredential(t *testing.T) {
	const lifecycleFile = "/etc/faas/imaged-storage.env"
	for _, entry := range UnitEntries() {
		hasLifecycleCredential := strings.Contains(entry.Unit().EnvironmentFile, lifecycleFile)
		if entry.Name == "imaged" && !hasLifecycleCredential {
			t.Fatalf("imaged does not load %s", lifecycleFile)
		}
		if entry.Name != "imaged" && hasLifecycleCredential {
			t.Errorf("%s unexpectedly receives package deletion authority", entry.Name)
		}
	}
}
