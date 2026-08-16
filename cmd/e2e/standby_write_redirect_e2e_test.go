// standby_write_redirect_e2e_test.go — Tier A9 / ADR-089 §14 M9
// filesystem-walk e2e.
//
// Spec §14 M9 row "Standby write-redirect" lands in three
// artifacts; this e2e pins the docs/ops-side surface:
//
//  1. docs/adr/089-standby-write-redirect.md exists with the
//     canonical ADR sections (`## Decision` and
//     `## Acceptance`).
//  2. docs/runbooks/standby-write-redirect.md exists with the
//     7-section operator surface (Pre-flight, Procedure,
//     Validation matrix, Rollback, Escalation, References,
//     Acceptance).
//  3. The 7×3 closed (outcome × auth_kind) vocabulary
//     appears in the runbook's validation matrix.
//  4. The 8-tierA9 deliverable files exist on disk
//     (Makefile target, drill script, runbook, ADR, property
//     test, e2e, migration, gate handler).
//  5. The Makefile target exists and references the runbook
//     and the drill script verbatim.
//  6. The drill script is bash-syntax-clean (bash -n).
//
// The e2e does NOT stand up daemons or exercise the
// drill live — that's the
// `deploy/lima/run-ha-write-redirect.sh` script's job
// (read-only, two-node Lima fleet). This e2e is the
// static-analysis gate: a PR that renames a runbook, drops
// a section, or breaks the Makefile target fails here
// before the Lima fleet ever boots.
//
// No `//go:build` tag — runs in CI on every host.

package e2e_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// --- TestStandbyWriteRedirect_ADR_ExistsAndHasDecisionAndAcceptance ---
//
// The ADR is the canonical source of truth for the design;
// without `## Decision` and `## Acceptance` headings the
// reviewer has no contract to verify the implementation
// against. The decision section is where the 8-case tree
// lives; the acceptance section is where the closed
// vocabulary gets pinned.
func TestStandbyWriteRedirect_ADR_ExistsAndHasDecisionAndAcceptance(t *testing.T) {
	root := repoRoot()
	if root == "" {
		t.Skip("module root not reachable")
	}
	path := filepath.Join(root, "docs", "adr", "089-standby-write-redirect.md")
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v — spec §14 M9 ADR is missing", path, err)
	}
	for _, heading := range []string{"## Decision", "## Acceptance"} {
		if !strings.Contains(string(body), heading) {
			t.Errorf("%s missing %q heading", path, heading)
		}
	}
}

// --- TestStandbyWriteRedirect_Runbook_ExistsAndHasSevenSections ---
//
// The runbook is the operator-facing counterpart. Per the
// existing runbooks (active-passive-ha.md, multi-host-rollout.md),
// the convention is Pre-flight / Procedure / Validation
// matrix / Rollback / Escalation / References / Acceptance.
// The test pins all 7 so a future edit can't silently drop
// one.
func TestStandbyWriteRedirect_Runbook_ExistsAndHasSevenSections(t *testing.T) {
	root := repoRoot()
	if root == "" {
		t.Skip("module root not reachable")
	}
	path := filepath.Join(root, "docs", "runbooks", "standby-write-redirect.md")
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v — spec §14 M9 runbook is missing", path, err)
	}

	required := []string{
		"## Pre-flight",
		"## Procedure",
		"## Validation matrix",
		"## Rollback / recovery",
		"## Escalation",
		"## References",
		"## Acceptance",
	}
	missing := []string{}
	for _, h := range required {
		if !strings.Contains(string(body), h) {
			missing = append(missing, h)
		}
	}
	if len(missing) > 0 {
		t.Fatalf("%s is missing %d/%d required sections: %v",
			path, len(missing), len(required), missing)
	}
}

// --- TestStandbyWriteRedirect_Runbook_ValidationMatrixHasClosedVocabulary ---
//
// The 7 outcomes × 3 auth kinds are the closed vocabulary
// that pre-instantiates the Prometheus counter at boot
// (TestOpsMetrics_WriteRedirectPreinstantiated). If any cell
// goes missing from the runbook, the operator's on-call
// surface loses a row — a regression in the §12 PromQL
// alerting panel that surfaces when the redirect layer is
// unhealthy.
func TestStandbyWriteRedirect_Runbook_ValidationMatrixHasClosedVocabulary(t *testing.T) {
	root := repoRoot()
	if root == "" {
		t.Skip("module root not reachable")
	}
	path := filepath.Join(root, "docs", "runbooks", "standby-write-redirect.md")
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}

	// Every WriteOutcome value (closed vocabulary from
	// pkg/gateway/writegate/writegate.go::AllWriteOutcomes)
	// must appear in the runbook's validation matrix. The
	// runbook uses backtick-quoted strings; a regression
	// where the cell was renamed without updating the
	// matrix would break the dashboard's
	// `outcome=~"..."` regex matcher.
	outcomes := []string{
		"same_box",
		"relayed",
		"redirect_307",
		"leader_unreachable",
		"loop_prevented",
		"mTLS_failure",
		"error",
	}
	missing := []string{}
	for _, o := range outcomes {
		if !strings.Contains(string(body), "`"+o+"`") {
			missing = append(missing, o)
		}
	}
	if len(missing) > 0 {
		t.Errorf("runbook validation matrix missing %d/%d outcome cells: %v",
			len(missing), len(outcomes), missing)
	}

	// Every AuthKind value (closed vocabulary from
	// pkg/gateway/writegate/writegate.go::AllAuthKinds)
	// must also appear. Same regression guard.
	authKinds := []string{
		"bearer",
		"cookie",
		"anonymous",
	}
	missingAuth := []string{}
	for _, k := range authKinds {
		if !strings.Contains(string(body), "`"+k+"`") {
			missingAuth = append(missingAuth, k)
		}
	}
	if len(missingAuth) > 0 {
		t.Errorf("runbook validation matrix missing %d/%d auth_kind cells: %v",
			len(missingAuth), len(authKinds), missingAuth)
	}
}

// --- TestStandbyWriteRedirect_AllEightArtifactsPresent ---
//
// Tier A9 / ADR-089 spans 8 deliverable files. A PR that
// ships the runbook + ADR but forgets the property test
// (or vice versa) leaves the slice half-built. The test
// walks the canonical file list and asserts every entry
// exists on disk.
func TestStandbyWriteRedirect_AllEightArtifactsPresent(t *testing.T) {
	root := repoRoot()
	if root == "" {
		t.Skip("module root not reachable")
	}
	artifacts := []string{
		"docs/adr/089-standby-write-redirect.md",
		"docs/runbooks/standby-write-redirect.md",
		"Makefile", // contains the ha-write-redirect-drill target
		"deploy/lima/run-ha-write-redirect.sh",
		"tests/property/write_redirect_test.go",
		"cmd/e2e/standby_write_redirect_e2e_test.go",
		"migrations/00174_compute_nodes_public_ip.sql",
		"pkg/gateway/writegate/writegate.go",
	}
	missing := []string{}
	for _, a := range artifacts {
		path := filepath.Join(root, a)
		if _, err := os.Stat(path); err != nil {
			missing = append(missing, a)
		}
	}
	if len(missing) > 0 {
		t.Errorf("Tier A9 / ADR-089 deliverables missing from disk: %v", missing)
	}
}

// --- TestStandbyWriteRedirect_MakefileTarget_ReferencesRunbookAndScript ---
//
// The drill target is the operator's entry point; a typo in
// the runbook path or drill script path silently breaks
// `make ha-write-redirect-drill` (it prints a 7-step
// procedure referencing the runbook; the procedure text
// must contain the runbook filename). This test pins the
// string-level cross-reference so a rename of either
// triggers a CI failure.
func TestStandbyWriteRedirect_MakefileTarget_ReferencesRunbookAndScript(t *testing.T) {
	root := repoRoot()
	if root == "" {
		t.Skip("module root not reachable")
	}
	makefilePath := filepath.Join(root, "Makefile")
	body, err := os.ReadFile(makefilePath)
	if err != nil {
		t.Fatalf("read %s: %v", makefilePath, err)
	}
	required := []string{
		"ha-write-redirect-drill",                 // the target name
		"docs/runbooks/standby-write-redirect.md", // cross-ref to the runbook
		"deploy/lima/run-ha-write-redirect.sh",    // cross-ref to the drill script
		"FAAS_LEADER_REDIRECT_TLS_CERT",           // the deploy-time opt-in flag
		"compute_nodes_changed",                   // the pg_notify channel (post-00276 split)
	}
	missing := []string{}
	for _, s := range required {
		if !strings.Contains(string(body), s) {
			missing = append(missing, s)
		}
	}
	if len(missing) > 0 {
		t.Errorf("Makefile ha-write-redirect-drill target missing %d/%d required cross-references: %v",
			len(missing), len(required), missing)
	}
}

// --- TestStandbyWriteRedirect_DrillScript_BashSyntaxClean ---
//
// The drill script is shell, not Go — the linter doesn't
// touch it. A bash typo (mismatched quote, missing
// semicolon) only surfaces on first run, on the operator's
// Mac. The e2e invokes `bash -n` against the script in CI
// so the typo fails the gate.
func TestStandbyWriteRedirect_DrillScript_BashSyntaxClean(t *testing.T) {
	root := repoRoot()
	if root == "" {
		t.Skip("module root not reachable")
	}
	scriptPath := filepath.Join(root, "deploy", "lima", "run-ha-write-redirect.sh")
	if _, err := os.Stat(scriptPath); err != nil {
		t.Fatalf("read %s: %v", scriptPath, err)
	}
	cmd := exec.Command("bash", "-n", scriptPath)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("bash -n %s failed: %v\noutput:\n%s", scriptPath, err, out)
	}
}

// --- TestStandbyWriteRedirect_PropertyTest_CompilesAndRuns ---
//
// The property test is the load-bearing proof of the
// 8-case decision tree. A test that fails to compile, or
// that exists as a stub, would let a regression in the
// gate's classifyRequest() logic slip through. This e2e
// invokes the test binary against the property package.
func TestStandbyWriteRedirect_PropertyTest_CompilesAndRuns(t *testing.T) {
	root := repoRoot()
	if root == "" {
		t.Skip("module root not reachable")
	}
	propertyTestPath := filepath.Join(root, "tests", "property", "write_redirect_test.go")
	if _, err := os.Stat(propertyTestPath); err != nil {
		t.Fatalf("read %s: %v", propertyTestPath, err)
	}
	// Run go test against the property package; if the
	// file fails to compile, the test fails here. We
	// filter to the Tier A9 tests so a Tier A8 regression
	// in concurrency_test.go doesn't gate this slice.
	cmd := exec.Command("go", "test", "-count=1", "-run", "TestWriteRedirect", "./tests/property/")
	cmd.Dir = root
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("go test ./tests/property/ failed: %v\noutput:\n%s", err, out)
	}
}

// --- TestStandbyWriteRedirect_ADR_ReferencesProductionPRs ---
//
// The ADR's `## Cross-references` (or equivalent) section
// must name the prerequisite ADRs (083, 070, 052) and the
// production code paths (PR-A / PR-B). A future edit that
// deletes a cross-reference silently drops the lineage.
func TestStandbyWriteRedirect_ADR_ReferencesProductionPRs(t *testing.T) {
	root := repoRoot()
	if root == "" {
		t.Skip("module root not reachable")
	}
	path := filepath.Join(root, "docs", "adr", "089-standby-write-redirect.md")
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	// The ADR is the canonical source; references to the
	// prerequisite ADRs (083, 070, 052) plus the
	// production code paths are mandatory.
	required := []string{
		"ADR-083", // Tier A8 prerequisite
		"ADR-070", // Tier A7 prerequisite
		"ADR-052", // PKI / cert layout keep set
		"writeGate",
		"LeaderResolver",
	}
	missing := []string{}
	for _, s := range required {
		if !strings.Contains(string(body), s) {
			missing = append(missing, s)
		}
	}
	if len(missing) > 0 {
		t.Errorf("ADR-089 cross-references missing %d/%d entries: %v",
			len(missing), len(required), missing)
	}
}
