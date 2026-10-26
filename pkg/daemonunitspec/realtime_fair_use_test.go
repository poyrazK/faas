package daemonunitspec

// adr: 1323

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Without fair use realtimed keeps the 10,000-connection node default, which
// does not fit its 512M MemoryMax, so both flags are protections every unit
// ships with. The gatewayd_internal_service role's realtimed unit is kept by
// hand, not by the generator, so it is checked here too.
func TestRealtimeUnitsEnableFairUse(t *testing.T) {
	root := repoRoot(t)
	fairUse := "Environment=FAAS_REALTIME_FAIR_USE_ENABLED=1\n"
	budget := "Environment=FAAS_REALTIME_OUTBOUND_BUDGET_ENABLED=1\n"
	for path, want := range map[string][]string{
		filepath.Join(root, "deploy", "systemd", "faas-apid.service"):                                                     {fairUse},
		filepath.Join(root, "deploy", "ansible", "roles", "control_plane_service", "files", "faas-apid.service"):          {fairUse},
		filepath.Join(root, "deploy", "systemd", "faas-realtimed.service"):                                                {fairUse, budget},
		filepath.Join(root, "deploy", "ansible", "roles", "gatewayd_internal_service", "files", "faas-realtimed.service"): {fairUse, budget},
	} {
		body, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		for _, line := range want {
			if !strings.Contains(string(body), line) {
				t.Errorf("%s lacks %q", path, strings.TrimSpace(line))
			}
		}
	}
}
