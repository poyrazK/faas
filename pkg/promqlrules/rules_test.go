// Tests that the Prometheus rule files linted by this repo are
// parseable by promtool. The canonical consolidated rules file is at
// deploy/ansible/roles/prometheus/files/faas.rules.yml; per-alert
// families ship as siblings in pkg/promqlrules/ (the first such
// sibling is data_placement.yaml, ADR-098 §9.A, issue #951). Issue
// #303 / ADR-039 introduces the first recording rules in the repo;
// this is the first Go-level gate that asserts the rule files are
// valid Prometheus artifacts. The CI step at
// .github/workflows/ci.yml:173-183 already runs the same check
// unconditionally — this test is the local-runner equivalent so devs
// can validate rule-file edits without pushing.
//
// Additionally, TestFaasRulesAcceptance walks every *.test.yml
// fixture in testdata/ via `promtool test rules` — the canonical
// synthetic-fixture pattern (ADR-098 §D4). Each fixture feeds real
// histogram / counter samples through the alert evaluator and
// asserts the alert opens at the configured threshold. Adding a new
// alert family requires adding a matching .test.yml fixture in
// testdata/.
//
// Build tag: integration. Skipped on plain `go test` runs because
// promtool may not be installed on the dev box; CI installs it
// explicitly via the workflow step. The convention matches the
// repo's other integration-gated tests.

//go:build integration

package promqlrules_test

import (
	"os/exec"
	"path/filepath"
	"testing"
)

func TestFaasRulesSyntax(t *testing.T) {
	if _, err := exec.LookPath("promtool"); err != nil {
		t.Skip("promtool not installed on PATH; the CI step at .github/workflows/ci.yml:173-183 runs the check unconditionally")
	}
	// Walk up from the test binary's working directory to the repo
	// root, then resolve each rule-file path. The package lives at
	// pkg/promqlrules/ so two dirs up is the repo root.
	repoRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("abs: %v", err)
	}
	// Rule-file inventory. New families ship as siblings in
	// pkg/promqlrules/ alongside the consolidated rules file; the
	// first such sibling is data_placement.yaml (ADR-098 §9.A,
	// issue #951). Each path is run through `promtool check rules`
	// unconditionally — a typo in any one breaks the box.
	ruleFiles := []string{
		filepath.Join(repoRoot, "deploy", "ansible", "roles", "prometheus", "files", "faas.rules.yml"),
		filepath.Join(repoRoot, "pkg", "promqlrules", "data_placement.yaml"),
		filepath.Join(repoRoot, "pkg", "promqlrules", "object_node_scrub.yaml"),
	}
	for _, rel := range ruleFiles {
		t.Run(filepath.Base(rel), func(t *testing.T) {
			cmd := exec.Command("promtool", "check", "rules", rel)
			out, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("promtool check rules failed for %s:\n%s\n%v", rel, out, err)
			}
		})
	}
}

// TestFaasRulesAcceptance walks testdata/*.test.yml and runs each
// through `promtool test rules`. See pkg/promqlrules/testdata/data_placement.test.yml
// for the canonical synthetic-fixture pattern (ADR-098). The
// contract is: every alert must have a fixture that asserts
// (a) it fires on the threshold boundary, (b) it does NOT fire
// in the opposite state, and (c) it carries ONLY the labels
// permitted by §11 (no plaintext host).
func TestFaasRulesAcceptance(t *testing.T) {
	if _, err := exec.LookPath("promtool"); err != nil {
		t.Skip("promtool not installed on PATH; CI runs the check unconditionally")
	}
	repoRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("abs: %v", err)
	}
	testdataDir := filepath.Join(repoRoot, "pkg", "promqlrules", "testdata")
	matches, err := filepath.Glob(filepath.Join(testdataDir, "*.test.yml"))
	if err != nil {
		t.Fatalf("glob: %v", err)
	}
	if len(matches) == 0 {
		t.Skipf("no synthetic fixtures in %s; add a *.test.yml to cover new alert families", testdataDir)
	}
	for _, fixture := range matches {
		t.Run(filepath.Base(fixture), func(t *testing.T) {
			cmd := exec.Command("promtool", "test", "rules", fixture)
			out, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("promtool test rules failed for %s:\n%s\n%v", fixture, out, err)
			}
		})
	}
}
