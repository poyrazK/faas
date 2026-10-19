package main_test

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/cmd/gregale"
	"gopkg.in/yaml.v3"
)

func campaignRecoveryRegressionFixture(t *testing.T) (main.TestScenario, main.TestRunReceipt, string) {
	t.Helper()
	s := main.CampaignCoverageScenario()
	s.Resilience.CampaignCoverage.MinExercised = map[int]int{0: 1, 1: 1}
	expanded, err := main.ExpandTestFaultSweep(s)
	if err != nil {
		t.Fatal(err)
	}
	baseline := main.RecoveryTrendReceipt(t, expanded, "baseline-", 100, 100, 0)
	path := filepath.Join(t.TempDir(), "baseline.json")
	if err = main.WriteTestReport(path, []main.TestRunReceipt{baseline}); err != nil {
		t.Fatal(err)
	}
	s.Resilience.CampaignCoverage.MinExercised = nil
	zero := 0.
	s.Resilience.CampaignCoverage.RecoveryRegression = &main.TestCampaignRecoveryRegressionSpec{FromReport: path, RunID: baseline.RunID, Checks: []main.TestCampaignRecoveryRegressionCheckSpec{{Choice: 0, Limits: main.TestRecoveryTrendConfig{MinSamples: 5, MaxRecoveryIncreasePercent: &zero, MaxP95IncreasePercent: &zero, MaxErrorRateIncrease: &zero}}}}
	return s, baseline, path
}

func TestCampaignRecoveryRegressionGateReportsAndReplay(t *testing.T) {
	s, baseline, path := campaignRecoveryRegressionFixture(t)
	before, _ := json.Marshal(s)
	expanded, err := main.ExpandTestFaultSweep(s)
	if err != nil {
		t.Fatal(err)
	}
	after, _ := json.Marshal(s)
	if !bytes.Equal(before, after) {
		t.Fatal("source mutated")
	}
	clone := expanded.Resilience.CampaignCoverage.RecoveryRegression
	clone.Checks[0].Limits.MinSamples = 6
	*clone.Checks[0].Limits.MaxRecoveryIncreasePercent = 1
	if s.Resilience.CampaignCoverage.RecoveryRegression.Checks[0].Limits.MinSamples != 5 || *s.Resilience.CampaignCoverage.RecoveryRegression.Checks[0].Limits.MaxRecoveryIncreasePercent != 0 {
		t.Fatal("expanded policy aliases source")
	}
	clone.Checks[0].Limits.MinSamples = 5
	*clone.Checks[0].Limits.MaxRecoveryIncreasePercent = 0
	pinned := expanded.Resilience.CampaignCoverage.RecoveryRegression.Baseline
	if pinned == nil || pinned.Coverage.RunID != baseline.RunID || len(pinned.Coverage.ReportSHA256) != 64 {
		t.Fatal("baseline not pinned")
	}
	passed := main.RecoveryTrendReceipt(t, s, "passing-", 100, 100, 0)
	if passed.Status != "passed" || passed.FaultMatrix.CampaignCoverage.RecoveryRegression.Status != "passed" {
		t.Fatal("equality rejected", passed.Error)
	}
	failed := main.RecoveryTrendReceipt(t, s, "failing-", 150, 160, 2)
	gate := failed.FaultMatrix.CampaignCoverage.RecoveryRegression
	if failed.RunID == "" || failed.Replay == nil || !failed.Replay.CampaignCoverage {
		t.Fatal("gate-only aggregate replay missing")
	}
	if failed.Status != "failed" || failed.FaultMatrix.Passed != 3 || gate.Status != "failed" || !strings.Contains(gate.Error, "recovery time") || !strings.Contains(gate.Error, "p95 latency") || !strings.Contains(gate.Error, "error rate") {
		t.Fatal("passing seed groups concealed regression", failed.Error)
	}
	if main.FinishTestRunReports("", []main.TestRunReceipt{failed}, "", "") != 1 {
		t.Fatal("CI did not fail")
	}
	replay, err := main.PrepareTestReplay(&failed, map[string]main.TestScenario{"checkout": s}, ".")
	if err != nil {
		t.Fatal(err)
	}
	if replay.Scenario.Resilience.CampaignCoverage.RecoveryRegression == nil {
		t.Fatal("replay lost gate")
	}
	original := s.Resilience.CampaignCoverage.RecoveryRegression.Checks[0].Limits.MinSamples
	s.Resilience.CampaignCoverage.RecoveryRegression.Checks[0].Limits.MinSamples = 6
	if _, err = main.PrepareTestReplay(&failed, map[string]main.TestScenario{"checkout": s}, "."); err == nil {
		t.Fatal("changed gate replay permitted")
	}
	s.Resilience.CampaignCoverage.RecoveryRegression.Checks[0].Limits.MinSamples = original
	dir := t.TempDir()
	html, junit, report := filepath.Join(dir, "report.html"), filepath.Join(dir, "report.xml"), filepath.Join(dir, "report.json")
	if err = main.WriteTestHTMLReport(html, "", []main.TestRunReceipt{failed}); err != nil {
		t.Fatal(err)
	}
	if err = main.WriteTestJUnit(junit, []main.TestRunReceipt{failed}); err != nil {
		t.Fatal(err)
	}
	if err = main.WriteTestReport(report, []main.TestRunReceipt{failed}); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{html, junit, report} {
		body, e := os.ReadFile(p)
		if e != nil || !bytes.Contains(body, []byte(baseline.RunID)) || !bytes.Contains(body, []byte(pinned.Coverage.ReportSHA256)) || !bytes.Contains(body, []byte("recovery regression")) {
			t.Fatal("missing report provenance", p, e)
		}
	}
	var out bytes.Buffer
	main.PrintTestCampaignCoverage(&out, failed.FaultMatrix.CampaignCoverage)
	if !strings.Contains(out.String(), "current recovery 150") {
		t.Fatal("terminal evidence missing", out.String())
	}
	if err = main.ValidateTestCampaignGuidanceOptions(expanded, "checkout", []string{"warm"}, nil); err == nil {
		t.Fatal("profile mismatch accepted")
	}
	alias := filepath.Join(dir, "alias.json")
	if err = os.Link(path, alias); err != nil {
		t.Fatal(err)
	}
	if err = main.ValidateTestCampaignGuidanceOptions(expanded, "checkout", []string{"cold"}, []string{alias}); err == nil {
		t.Fatal("baseline overwritten")
	}
	raw, e := os.ReadFile(path)
	if e != nil {
		t.Fatal(e)
	}
	if err = os.WriteFile(path, append(raw, '\n'), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = main.PrepareTestReplay(&failed, map[string]main.TestScenario{"checkout": s}, "."); err == nil {
		t.Fatal("changed baseline replay permitted")
	}
}

func TestCampaignRecoveryRegressionValidation(t *testing.T) {
	for _, kind := range []string{"no-checks", "no-limit", "duplicate", "choice", "request", "samples", "missing-baseline", "seeds", "unconfirmed-baseline"} {
		t.Run(kind, func(t *testing.T) {
			s, _, _ := campaignRecoveryRegressionFixture(t)
			reg := s.Resilience.CampaignCoverage.RecoveryRegression
			switch kind {
			case "no-checks":
				reg.Checks = nil
			case "no-limit":
				reg.Checks[0].Limits.MaxRecoveryIncreasePercent = nil
				reg.Checks[0].Limits.MaxP95IncreasePercent = nil
				reg.Checks[0].Limits.MaxErrorRateIncrease = nil
			case "duplicate":
				reg.Checks = append(reg.Checks, reg.Checks[0])
			case "choice":
				reg.Checks[0].Choice = 99
			case "request":
				reg.Checks[0].Request = "missing"
			case "samples":
				reg.Checks[0].Limits.MinSamples = 11
			case "missing-baseline":
				reg.FromReport = "missing.json"
			case "unconfirmed-baseline":
				receipts, _, err := main.ReadTestBaselineReport(reg.FromReport, nil)
				if err != nil {
					t.Fatal(err)
				}
				receipts[0].FaultMatrix.Cases[0].Resilience.History[0].Steps[0].Load.ChaosPhases[2].Recovery.Passed = false
				if err = main.WriteTestReport(reg.FromReport, receipts); err != nil {
					t.Fatal(err)
				}
			case "seeds":
				s.Resilience.CampaignCoverage.Seeds = []uint64{45, 46, 47}
			}
			if _, err := main.ExpandTestFaultSweep(s); err == nil {
				t.Fatal("invalid gate accepted")
			}
		})
	}
}

func TestCampaignRecoveryRegressionMissingEvidenceAndIndependentFailures(t *testing.T) {
	s, _, _ := campaignRecoveryRegressionFixture(t)
	// A passing load SLO cannot replace confirmed recovery evidence.
	result := main.RunSweepTest(context.Background(), s, func(ctx context.Context, p main.TestRunPlan) main.TestRunReceipt {
		return main.CampaignCoverageReceipt(t, p, "unconfirmed-"+p.ReplayCase, false)
	})
	if result.Status != "failed" || result.FaultMatrix.Passed != 3 || result.FaultMatrix.CampaignCoverage.RecoveryRegression.Status != "failed" {
		t.Fatal("missing confirmation did not fail gate", result.Error)
	}
	expanded, err := main.ExpandTestFaultSweep(s)
	if err != nil {
		t.Fatal(err)
	}
	good := main.RecoveryTrendReceipt(t, s, "current-", 100, 100, 0)
	good.Status = "failed"
	good.Error = "existing SLO failure"
	good.CleanupError = "cleanup failed"
	if err = main.EvaluateTestCampaignRecoveryRegression(expanded, &good); err != nil || good.Status != "failed" || good.Error != "existing SLO failure" || good.CleanupError != "cleanup failed" {
		t.Fatal("gate cleared independent failure")
	}
	good.FaultMatrix.Cases[0].Resilience = nil
	if err = main.EvaluateTestCampaignRecoveryRegression(expanded, &good); err == nil {
		t.Fatal("incomplete history accepted")
	}
}

func TestCampaignRecoveryRegressionOfflineManifestAndReplay(t *testing.T) {
	t.Setenv("FAAS_TOKEN", "")
	dir := t.TempDir()
	for name, body := range map[string]string{"package.json": `{"scripts":{"start":"node server.js"}}`, "server.js": "console.log('fixture');"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
	}
	manifest := filepath.Join(dir, "gregale-test.yaml")
	save := func(s main.TestScenario) map[string]main.TestScenario {
		t.Helper()
		body, err := yaml.Marshal(main.TestManifest{Version: 1, Scenarios: map[string]main.TestScenario{"checkout": s}})
		if err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(manifest, body, 0600); err != nil {
			t.Fatal(err)
		}
		scenarios, _, err := main.ReadTestManifestForScenario(manifest, "checkout", nil)
		if err != nil {
			t.Fatal(err)
		}
		return scenarios
	}
	source := main.CampaignCoverageScenario()
	source.Resilience.CampaignCoverage.MinExercised = map[int]int{0: 1, 1: 1}
	baseline := main.RecoveryTrendReceipt(t, save(source)["checkout"], "baseline-", 100, 100, 0)
	path := filepath.Join(dir, "baseline.json")
	if err := main.WriteTestReport(path, []main.TestRunReceipt{baseline}); err != nil {
		t.Fatal(err)
	}
	zero := 0.
	source.Resilience.CampaignCoverage.RecoveryRegression = &main.TestCampaignRecoveryRegressionSpec{FromReport: "baseline.json", RunID: baseline.RunID, Checks: []main.TestCampaignRecoveryRegressionCheckSpec{{Choice: 0, Limits: main.TestRecoveryTrendConfig{MinSamples: 5, MaxRecoveryIncreasePercent: &zero}}}}
	normalized := save(source)
	if main.CmdTest([]string{"--manifest", manifest, "--scenario", "checkout", "--validate"}) != 0 {
		t.Fatal("offline validation failed")
	}
	failed := main.RecoveryTrendReceipt(t, normalized["checkout"], "current-", 150, 100, 0)
	report := filepath.Join(dir, "failed.json")
	if err := main.WriteTestReport(report, []main.TestRunReceipt{failed}); err != nil {
		t.Fatal(err)
	}
	if main.CmdTestReplay([]string{"--manifest", manifest, "--from", report, "--run-id", failed.RunID, "--validate"}) != 0 {
		t.Fatal("offline aggregate replay failed")
	}
}

func TestCampaignRecoveryRegressionNamedTarget(t *testing.T) {
	s := main.CampaignCoverageScenario()
	s.Resilience.CampaignCoverage.MinExercised = map[int]int{0: 1, 1: 1}
	name := s.Steps[1].Requests[0].Name
	s.Steps[1].Load.ChaosSchedule[2].StepRecovery = map[string]*main.TestLoadRecoverySpec{name: s.Steps[1].Load.ChaosSchedule[2].Recovery}
	expanded, err := main.ExpandTestFaultSweep(s)
	if err != nil {
		t.Fatal(err)
	}
	baseline := main.RecoveryTrendReceipt(t, expanded, "baseline-", 100, 100, 0)
	path := filepath.Join(t.TempDir(), "baseline.json")
	if err = main.WriteTestReport(path, []main.TestRunReceipt{baseline}); err != nil {
		t.Fatal(err)
	}
	zero := 0.
	s.Resilience.CampaignCoverage.RecoveryRegression = &main.TestCampaignRecoveryRegressionSpec{FromReport: path, RunID: baseline.RunID, Checks: []main.TestCampaignRecoveryRegressionCheckSpec{{Choice: 0, Request: name, Limits: main.TestRecoveryTrendConfig{MinSamples: 5, MaxRecoveryIncreasePercent: &zero}}}}
	current := main.RecoveryTrendReceipt(t, s, "current-", 150, 100, 0)
	if current.Status != "failed" {
		t.Fatal("endpoint regression did not fail")
	}
	check := current.FaultMatrix.CampaignCoverage.RecoveryRegression.Checks[0]
	if check.Current.Request != name || check.Baseline.Request != name || !strings.Contains(check.Current.Error, "recovery time") {
		t.Fatal("wrong recovery target", check)
	}
}
