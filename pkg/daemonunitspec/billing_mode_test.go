package daemonunitspec

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPublicBetaRoleOverridesBillingMode(t *testing.T) {
	root := filepath.Join("..", "..", "deploy", "ansible", "roles", "control_plane_service")
	defaults, err := os.ReadFile(filepath.Join(root, "defaults", "main.yml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(defaults), "faas_billing_mode: disabled") {
		t.Fatal("public-beta role does not default billing to disabled")
	}
	tasks, err := os.ReadFile(filepath.Join(root, "tasks", "main.yml"))
	if err != nil {
		t.Fatal(err)
	}
	for _, unit := range []string{"faas-apid", "faas-meterd"} {
		if !strings.Contains(string(tasks), "- "+unit) {
			t.Fatalf("billing mode drop-in does not cover %s", unit)
		}
	}
	tmpl, err := os.ReadFile(filepath.Join(root, "templates", "zz-faas-billing-mode.conf.j2"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(tmpl), "Environment=FAAS_BILLING_MODE={{ faas_billing_mode }}") {
		t.Fatal("billing mode drop-in does not render the role variable")
	}
}
