// Synthetic-fixture test for the ADR-123 PR-C alert_preset signal
// rules (issue #1233 follow-up to PR-B migration 00516).
//
// What this exercises: the new `faas_alert_preset_signals` group
// in `deploy/ansible/roles/prometheus/files/faas.rules.yml`:
//   1. FaasApiDownAccount, FaasSpendEur20Account,
//      FaasDeployFailedAccount, FaasCertExpiring14dAccount,
//      FaasQueueBacklogGrowingApp — per-signal alerts (severity=warn)
//      with `for:` clauses matched to each preset's catalog
//      `window_spec` and `default_cooldown_minutes`.
//   2. FaasAlertPresetAnyFiringAccount — account-level correlation
//      using `count by (account_id) (...) >= 1`. The
//      gateway_queue_depth signal is excluded (it carries an `app`
//      label, not `account_id`).
//   3. Negative case — all signals within bounds → no alert fires.
//
// Why this fixture exists: TestFaasRulesSyntax validates the rule
// file parse-only; the per-family driver files (anomaly_score_test.go,
// error_rate_test.go, cpu_starvation_test.go, tenant_abuse_test.go,
// data_placement_sec11_test.go, wake_failure_test.go) validate each
// alert family at the threshold boundary. This file is the
// alert_preset_signals family's driver. Auto-discovered by
// TestFaasRulesAcceptance (rules_test.go:67-99) which globs
// testdata/*.test.yml.
//
// Build tag: integration. Skipped on plain `go test` runs because
// promtool may not be installed on the dev box; CI installs it
// explicitly via the workflow step at .github/workflows/ci.yml.
// Matches the existing TestFaasErrorRateEval precedent.

//go:build integration

package promqlrules_test

import (
	"os/exec"
	"path/filepath"
	"testing"
)

func TestFaasAlertPresetSignalsEval(t *testing.T) {
	if _, err := exec.LookPath("promtool"); err != nil {
		t.Skip("promtool not installed on PATH; the CI step at .github/workflows/ci.yml runs the check unconditionally")
	}
	repoRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("abs: %v", err)
	}
	fixture := filepath.Join(repoRoot, "pkg", "promqlrules", "testdata", "alert_preset_signals.test.yml")
	cmd := exec.Command("promtool", "test", "rules", fixture)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("promtool test rules failed for %s:\n%s\n%v", fixture, out, err)
	}
}
