// adr: 1297
package dashboard_test

import (
	"os/exec"
	"testing"
)

func TestManagedSuiteBrowserProgress(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("Node is required for browser progress behavior checks")
	}
	output, err := exec.Command(node, "--test", "testdata/automation_live_suite_progress.test.mjs").CombinedOutput()
	if err != nil {
		t.Fatalf("browser progress: %v\n%s", err, output)
	}
}
