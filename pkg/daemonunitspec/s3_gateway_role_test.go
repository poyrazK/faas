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
	templatePath := filepath.Join(root, "deploy", "ansible", "roles", "s3_gateway_service", "templates", "s3-origin.caddy.j2")
	template, err := os.ReadFile(templatePath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(template), "header_up Accept-Encoding identity") {
		t.Fatal("s3 gateway Caddy route does not pin Accept-Encoding to identity for SigV4")
	}
}
