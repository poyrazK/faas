package daemonunitspec

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestS3GatewayRoleRefreshesPublicRoutingPins the deployment contract that
// makes a newly provisioned object-storage registry visible to the public
// gateway and keeps SigV4's signed encoding stable across the edge hop.
func TestS3GatewayRoleRefreshesPublicRoutingPins(t *testing.T) {
	root := repoRoot(t)
	tasksPath := filepath.Join(root, "deploy", "ansible", "roles", "s3_gateway_service", "tasks", "main.yml")
	tasks, err := os.ReadFile(tasksPath)
	if err != nil {
		t.Fatal(err)
	}
	tasksText := string(tasks)

	if got := strings.Count(tasksText, "- restart faas-gatewayd-public"); got < 3 {
		t.Fatalf("s3 gateway registry changes notify the public gateway %d times, want at least 3", got)
	}
	if !strings.Contains(tasksText, "header_up Accept-Encoding identity") {
		t.Fatal("s3 gateway Caddy route does not pin Accept-Encoding to identity for SigV4")
	}
}
