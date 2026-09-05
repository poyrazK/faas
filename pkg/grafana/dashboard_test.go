// Tests that the canonical Grafana dashboard at
// deploy/grafana/faas-fleet.json is valid JSON and that the panels
// introduced by issue #303 / ADR-039 (ids 80, 81, 82, 83), the
// issue #303 follow-up panel (id 90, "Anomaly spikes (last 7d)"), and
// the compute scrape coverage panels (ids 410, 411, 412) are present
// with non-empty PromQL expressions. The repo has no
// precedent for dashboard JSON validation; the only related test
// pattern is the per-component emitter-side scrape test in
// pkg/wire/metrics_cardinality_test.go. The validation is
// intentionally narrow — it parses the JSON, locates each new panel
// by `id`, and asserts the first `targets[].expr` is non-empty. It
// does not attempt to evaluate PromQL.
//
// The dashboard is provisioned by the Ansible role at
// deploy/ansible/roles/grafana/tasks/main.yml:172-181, which copies
// the role file at deploy/ansible/roles/grafana/files/faas-fleet.json
// (byte-identical to deploy/grafana/faas-fleet.json) onto the box.
// Validating one copy is sufficient; the byte-identical invariant is
// checked by the `cmp -s` step in the role's task body.

package grafana

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// panel is the minimum shape needed to extract (id, expr) for the
// required panels. Decoded into a typed struct so a typo in the JSON
// doesn't make every assertion noisy.
type panel struct {
	ID      int `json:"id"`
	Targets []struct {
		Expr string `json:"expr"`
	} `json:"targets"`
}

// dashboardRoot is the minimum shape of the Grafana 11 export.
type dashboardRoot struct {
	Panels []panel `json:"panels"`
}

func TestFaasFleetDashboardParses(t *testing.T) {
	// Walk up from the package's working directory (pkg/grafana/) to
	// the repo root, then resolve the canonical dashboard path.
	repoRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("abs: %v", err)
	}
	rel := filepath.Join(repoRoot, "deploy", "grafana", "faas-fleet.json")
	b, err := os.ReadFile(rel)
	if err != nil {
		t.Fatalf("read %s: %v", rel, err)
	}
	var root dashboardRoot
	if err := json.Unmarshal(b, &root); err != nil {
		t.Fatalf("unmarshal %s: %v", rel, err)
	}
	// Each new panel (issue #303 / ADR-039) must be present with a
	// non-empty PromQL expression. The ids are stable contract:
	// review-future contributors must add new panels with new ids
	// rather than reusing these, so the assertions stay accurate.
	for _, want := range []int{80, 81, 82, 83, 90, 410, 411, 412} {
		var found *panel
		for i := range root.Panels {
			if root.Panels[i].ID == want {
				found = &root.Panels[i]
				break
			}
		}
		if found == nil {
			t.Errorf("panel id %d not found in %s", want, rel)
			continue
		}
		if len(found.Targets) == 0 {
			t.Errorf("panel id %d has no targets", want)
			continue
		}
		if found.Targets[0].Expr == "" {
			t.Errorf("panel id %d has empty expr on targets[0]", want)
		}
	}
}

// TestFaasFleetDashboardByteIdentical asserts that the dashboard at
// deploy/grafana/faas-fleet.json and the role-copied file at
// deploy/ansible/roles/grafana/files/faas-fleet.json are
// byte-identical. The Ansible role at
// deploy/ansible/roles/grafana/tasks/main.yml:172-181 copies the
// role file onto the box; the deploy/grafana/ copy is the
// developer-facing canonical and is hand-edited, so drift between
// the two silently breaks the dashboard provisioning. The `cmp -s`
// step in the role task is the runtime gate; this test is the
// PR-time gate.
func TestFaasFleetDashboardByteIdentical(t *testing.T) {
	repoRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("abs: %v", err)
	}
	canon := filepath.Join(repoRoot, "deploy", "grafana", "faas-fleet.json")
	role := filepath.Join(repoRoot, "deploy", "ansible", "roles", "grafana", "files", "faas-fleet.json")
	canonB, err := os.ReadFile(canon)
	if err != nil {
		t.Fatalf("read %s: %v", canon, err)
	}
	roleB, err := os.ReadFile(role)
	if err != nil {
		t.Fatalf("read %s: %v", role, err)
	}
	if len(canonB) != len(roleB) {
		t.Fatalf("dashboard byte-length differs: %s=%d %s=%d", canon, len(canonB), role, len(roleB))
	}
	for i := range canonB {
		if canonB[i] != roleB[i] {
			t.Fatalf("dashboard byte-identical invariant broken at offset %d: %s vs %s", i, canon, role)
		}
	}
}

// TestFaasTopTenantsDashboardParses (issue #300, ADR-042) — the
// per-tenant noisy-customer dashboard at deploy/grafana/top-tenants.json
// is valid JSON and the 4 panels (ids 1-4) are present with
// non-empty PromQL expressions. Same parse-only contract as
// TestFaasFleetDashboardParses: no PromQL evaluation, just shape
// pins so a typo in the JSON doesn't silently break the §12 panel.
func TestFaasTopTenantsDashboardParses(t *testing.T) {
	repoRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("abs: %v", err)
	}
	rel := filepath.Join(repoRoot, "deploy", "grafana", "top-tenants.json")
	b, err := os.ReadFile(rel)
	if err != nil {
		t.Fatalf("read %s: %v", rel, err)
	}
	var root dashboardRoot
	if err := json.Unmarshal(b, &root); err != nil {
		t.Fatalf("unmarshal %s: %v", rel, err)
	}
	// Panel ids are a stable contract (issue #300 acceptance #2):
	// "Top-10 noisy customers (5m, apid)" → id 1, "Top-10 noisy
	// apps (5m, gateway)" → id 2, "Customer share of fleet
	// traffic" → id 3, "Other bucket growth" → id 4. Future
	// contributors must add new panels with new ids rather than
	// reusing these, so the assertions stay accurate.
	for _, want := range []int{1, 2, 3, 4} {
		var found *panel
		for i := range root.Panels {
			if root.Panels[i].ID == want {
				found = &root.Panels[i]
				break
			}
		}
		if found == nil {
			t.Errorf("panel id %d not found in %s", want, rel)
			continue
		}
		if len(found.Targets) == 0 {
			t.Errorf("panel id %d has no targets", want)
			continue
		}
		if found.Targets[0].Expr == "" {
			t.Errorf("panel id %d has empty expr on targets[0]", want)
		}
	}
}

// TestFaasTopTenantsDashboardByteIdentical (issue #300, ADR-042)
// mirrors TestFaasFleetDashboardByteIdentical for the new
// top-tenants dashboard. The Ansible role copies the file at
// deploy/ansible/roles/grafana/files/top-tenants.json onto the
// box; deploy/grafana/top-tenants.json is the developer-facing
// canonical. Drift between the two silently breaks the dashboard
// provisioning, so the byte-identical invariant is gated here.
func TestFaasTopTenantsDashboardByteIdentical(t *testing.T) {
	repoRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("abs: %v", err)
	}
	canon := filepath.Join(repoRoot, "deploy", "grafana", "top-tenants.json")
	role := filepath.Join(repoRoot, "deploy", "ansible", "roles", "grafana", "files", "top-tenants.json")
	canonB, err := os.ReadFile(canon)
	if err != nil {
		t.Fatalf("read %s: %v", canon, err)
	}
	roleB, err := os.ReadFile(role)
	if err != nil {
		t.Fatalf("read %s: %v", role, err)
	}
	if len(canonB) != len(roleB) {
		t.Fatalf("dashboard byte-length differs: %s=%d %s=%d", canon, len(canonB), role, len(roleB))
	}
	for i := range canonB {
		if canonB[i] != roleB[i] {
			t.Fatalf("dashboard byte-identical invariant broken at offset %d: %s vs %s", i, canon, role)
		}
	}
}
