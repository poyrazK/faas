package gregalemanifest

import (
	"strings"
	"testing"
)

// ADR-20261107180000: the strict manifest parser accepts the root project
// block and validates it; it never reaches an app's deployment.
func TestManifestProjectBlock(t *testing.T) {
	m, err := ParseBytes([]byte("project:\n  service_policy: declared\n  services:\n    billing:\n      visibility: internal\n      depends_on: [ledger]\n"))
	if err != nil {
		t.Fatal(err)
	}
	if m.Project == nil || m.Project.ServicePolicy != "declared" || m.Project.Services["billing"].Visibility != "internal" {
		t.Fatalf("project = %+v", m.Project)
	}
	if err := m.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	bad, err := ParseBytes([]byte("project:\n  services:\n    api:\n      visibility: private\n"))
	if err != nil {
		t.Fatal(err)
	}
	if err := bad.Validate(); err == nil || !strings.Contains(err.Error(), "project:") {
		t.Fatalf("Validate = %v, want a project visibility error", err)
	}
	if _, err := ParseBytes([]byte("project:\n  servics: {}\n")); err == nil {
		t.Fatal("unknown project key accepted")
	}
}
