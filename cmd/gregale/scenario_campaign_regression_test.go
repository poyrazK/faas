package main_test

import (
	"bytes"
	"context"
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/cmd/gregale"
	"gopkg.in/yaml.v3"
)

func campaignRegressionFixture(t *testing.T) (main.TestScenario, main.TestRunReceipt, string) {
	t.Helper()
	s := main.CampaignCoverageScenario()
	s.Resilience.CampaignCoverage.MinExercised = map[int]int{0: 1, 1: 1}
	baseline := runCampaignRequirementsTest(t, s, false, 0)
	path := filepath.Join(t.TempDir(), "baseline.json")
	if err := main.WriteTestReport(path, []main.TestRunReceipt{baseline}); err != nil {
		t.Fatal(err)
	}
	next := main.CampaignCoverageScenario()
	next.Resilience.CampaignCoverage.Regression = &main.TestCampaignCoverageRegressionSpec{FromReport: path, RunID: baseline.RunID, MinExercisedRate: 0}
	return next, baseline, path
}

func TestCampaignCoverageRegressionGateReportsReplay(t *testing.T) {
	s, baseline, path := campaignRegressionFixture(t)
	before, _ := json.Marshal(s)
	x, err := main.ExpandTestFaultSweep(s)
	after, _ := json.Marshal(s)
	if !bytes.Equal(before, after) {
		t.Fatal("regression expansion mutated source")
	}
	if err != nil {
		t.Fatal(err)
	}
	reg := x.Resilience.CampaignCoverage.Regression
	if reg.Baseline == nil || len(reg.Baseline.ReportSHA256) != 64 || reg.Baseline.RunID != baseline.RunID {
		t.Fatal("baseline not pinned")
	}
	passed := runCampaignRequirementsTest(t, s, false, 0)
	if passed.Status != "passed" || passed.FaultMatrix.CampaignCoverage.Regression.Status != "passed" {
		t.Fatal("unchanged coverage rejected", passed.Error)
	}
	failed := runCampaignRequirementsTest(t, s, true, 0)
	if failed.Status != "failed" || failed.FaultMatrix.Passed != 3 || failed.RunID == "" || failed.Replay == nil || failed.Replay.Version != 10 || !failed.Replay.CampaignCoverage {
		t.Fatal("regression-only gate did not fail campaign", failed)
	}
	for _, choice := range failed.FaultMatrix.CampaignCoverage.Regression.Choices {
		if choice.BaselineExercised > 0 && (!choice.LostCoverage || choice.Status != "failed") {
			t.Fatal("lost choice not identified", choice)
		}
	}
	if main.FinishTestRunReports("", []main.TestRunReceipt{failed}, "", "") != 1 {
		t.Fatal("regression did not fail CI")
	}
	prepared, err := main.PrepareTestReplay(&failed, map[string]main.TestScenario{"checkout": s}, ".")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(prepared.Scenario.Resilience.CampaignCoverage.Regression.Baseline, reg.Baseline) {
		t.Fatal("replay changed baseline")
	}
	var out bytes.Buffer
	main.PrintTestCampaignCoverage(&out, failed.FaultMatrix.CampaignCoverage)
	if !strings.Contains(out.String(), "lost coverage true") || !strings.Contains(out.String(), baseline.RunID) {
		t.Fatal("terminal regression missing")
	}
	dir := t.TempDir()
	html := filepath.Join(dir, "report.html")
	junit := filepath.Join(dir, "report.xml")
	jsonReport := filepath.Join(dir, "report.json")
	if err := main.WriteTestHTMLReport(html, "", []main.TestRunReceipt{failed}); err != nil {
		t.Fatal(err)
	}
	if err := main.WriteTestJUnit(junit, []main.TestRunReceipt{failed}); err != nil {
		t.Fatal(err)
	}
	if err := main.WriteTestReport(jsonReport, []main.TestRunReceipt{failed}); err != nil {
		t.Fatal(err)
	}
	for _, file := range []string{html, junit, jsonReport} {
		body, _ := os.ReadFile(file)
		if !strings.Contains(string(body), "campaign coverage regression failed") || !strings.Contains(string(body), baseline.RunID) {
			t.Fatal("regression evidence missing", file)
		}
	}
	if err := main.ValidateTestCampaignGuidanceOptions(x, "checkout", []string{"cold"}, []string{path}); err == nil {
		t.Fatal("baseline overwrite allowed")
	}
	if err := main.ValidateTestCampaignGuidanceOptions(x, "checkout", []string{"warm"}, nil); err == nil {
		t.Fatal("profile mismatch allowed")
	}
	changed := s
	c := *s.Resilience.CampaignCoverage
	r := *s.Resilience
	edit := *c.Regression
	edit.MinExercisedRate = 0.5
	c.Regression = &edit
	r.CampaignCoverage = &c
	changed.Resilience = &r
	if _, err := main.PrepareTestReplay(&failed, map[string]main.TestScenario{"checkout": changed}, "."); err == nil {
		t.Fatal("changed regression policy replay allowed")
	}
	alias := filepath.Join(t.TempDir(), "baseline-alias.json")
	if err := os.Link(path, alias); err != nil {
		t.Fatal(err)
	}
	if err := main.ValidateTestCampaignGuidanceOptions(x, "checkout", []string{"cold"}, []string{alias}); err == nil {
		t.Fatal("hard-link baseline overwrite allowed")
	}
	noPolicy := s
	noR := *s.Resilience
	noC := *s.Resilience.CampaignCoverage
	noC.Regression = nil
	noR.CampaignCoverage = &noC
	noPolicy.Resilience = &noR
	if _, err := main.PrepareTestReplay(&failed, map[string]main.TestScenario{"checkout": noPolicy}, "."); err == nil {
		t.Fatal("removed regression policy replay allowed")
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = f.WriteString("\n")
	_ = f.Close()
	if err := main.ValidateTestCampaignGuidanceOptions(x, "checkout", []string{"cold"}, nil); err == nil {
		t.Fatal("changed baseline allowed")
	}
	if _, err := main.PrepareTestReplay(&failed, map[string]main.TestScenario{"checkout": s}, "."); err == nil {
		t.Fatal("changed baseline replay allowed")
	}
}

func TestCampaignCoverageRegressionRateBoundariesAndOptionalChoices(t *testing.T) {
	s, _, _ := campaignRegressionFixture(t)
	x, err := main.ExpandTestFaultSweep(s)
	if err != nil {
		t.Fatal(err)
	}
	reg := x.Resilience.CampaignCoverage.Regression
	// Check exact equality, below-minimum rates and mandatory loss at a zero floor.
	reg.Baseline.Planned = []int{4, 4, 0}
	reg.Baseline.Exercised = []int{3, 0, 0}
	e := &main.TestCampaignCoverageEvidence{Choices: []main.TestCampaignChoiceCoverage{{Index: 0, Planned: 4, Exercised: 2}, {Index: 1, Planned: 4, Exercised: 2}, {Index: 2, Planned: 0}}}
	reg.MinExercisedRate = 0.5
	if err := main.EvaluateTestCampaignCoverageRegression(x, e); err != nil || e.Regression.Choices[2].Status != "not_planned" {
		t.Fatal("equal boundary or unplanned choice rejected", err)
	}
	e.Choices[1].Exercised = 1
	if err := main.EvaluateTestCampaignCoverageRegression(x, e); err == nil || e.Regression.Choices[1].LostCoverage {
		t.Fatal("absolute rate floor not enforced")
	}
	reg.MinExercisedRate = 0
	e.Choices[0].Exercised = 0
	e.Choices[1].Exercised = 0
	if err := main.EvaluateTestCampaignCoverageRegression(x, e); err == nil || !e.Regression.Choices[0].LostCoverage || e.Regression.Choices[1].Status != "passed" {
		t.Fatal("loss detection at zero floor failed")
	}
	e.Choices[0].Exercised = 4
	reg.MinExercisedRate = 1
	e.Choices[1].Exercised = 4
	if err := main.EvaluateTestCampaignCoverageRegression(x, e); err != nil {
		t.Fatal("100 percent boundary rejected", err)
	}
	e.Choices[0].Planned = 3
	if err := main.EvaluateTestCampaignCoverageRegression(x, e); err == nil {
		t.Fatal("incompatible planned counts accepted")
	}
}

func TestCampaignCoverageRegressionRejectsInvalidBaselineAndPolicy(t *testing.T) {
	for _, kind := range []string{"negative", "over-one", "nan", "infinity", "missing-path", "missing-id", "seeds", "runs", "workload", "summary", "cleanup", "skipped", "duplicate"} {
		t.Run(kind, func(t *testing.T) {
			s, baseline, path := campaignRegressionFixture(t)
			reg := s.Resilience.CampaignCoverage.Regression
			switch kind {
			case "negative":
				reg.MinExercisedRate = -0.1
			case "over-one":
				reg.MinExercisedRate = 1.1
			case "nan":
				reg.MinExercisedRate = math.NaN()
			case "infinity":
				reg.MinExercisedRate = math.Inf(1)
			case "missing-path":
				reg.FromReport = ""
			case "missing-id":
				reg.RunID = ""
			case "seeds":
				s.Resilience.CampaignCoverage.Seeds[1] = 45
			case "runs":
				s.Resilience.Runs = 2
				s.Resilience.CampaignCoverage.MaxRuns = 6
				s.Resilience.CampaignCoverage.MaxWorkloadMinutes = 24
			case "workload":
				s.Timeout = "2m"
			case "summary":
				baseline.FaultMatrix.CampaignCoverage.Choices[0].Exercised++
			case "cleanup":
				baseline.CleanupError = "VM remains"
			case "skipped":
				baseline.FaultMatrix.Cases[0].Resilience.History[0].Status = "skipped"
			case "duplicate":
				baseline.FaultMatrix.Cases[1].Resilience.History[0].RunID = baseline.FaultMatrix.Cases[0].Resilience.History[0].RunID
			}
			if err := main.WriteTestReport(path, []main.TestRunReceipt{baseline}); err != nil {
				t.Fatal(err)
			}
			if _, err := main.ExpandTestFaultSweep(s); err == nil {
				t.Fatal("invalid regression baseline accepted")
			}
		})
	}
}

func TestCampaignCoverageRegressionOtherFailuresRemainFailures(t *testing.T) {
	s, _, _ := campaignRegressionFixture(t)
	failed := runCampaignRequirementsTest(t, s, false, 43)
	if failed.Status != "failed" || failed.FaultMatrix.CampaignCoverage.Regression.Status != "passed" {
		t.Fatal("passing regression cleared SLO failure")
	}
	var calls int
	wrong := main.RunTestFaultMatrix(context.Background(), main.TestRunPlan{Prepared: &main.TestPreparedScenario{Name: "checkout", Engine: "real-vm", Scenario: s}, Profile: "warm"}, func(context.Context, main.TestRunPlan) main.TestRunReceipt {
		calls++
		return main.TestRunReceipt{Status: "passed"}
	})
	if wrong.Status != "failed" || calls != 0 {
		t.Fatal("wrong profile provisioned")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	interrupted := main.RunSweepTest(ctx, s, func(context.Context, main.TestRunPlan) main.TestRunReceipt {
		t.Fatal("canceled run executed")
		return main.TestRunReceipt{}
	})
	if interrupted.Status != "failed" {
		t.Fatal("cancellation passed campaign")
	}
}

func TestCampaignCoverageRegressionOfflineRelativeBaseline(t *testing.T) {
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
		if err := os.WriteFile(manifest, body, 0600); err != nil {
			t.Fatal(err)
		}
		normalized, _, err := main.ReadTestManifestForScenario(manifest, "checkout", nil)
		if err != nil {
			t.Fatal(err)
		}
		return normalized
	}
	prior := main.CampaignCoverageScenario()
	prior.Resilience.CampaignCoverage.MinExercised = map[int]int{0: 1, 1: 1}
	baseline := runCampaignRequirementsTest(t, save(prior)["checkout"], false, 0)
	if err := main.WriteTestReport(filepath.Join(dir, "baseline.json"), []main.TestRunReceipt{baseline}); err != nil {
		t.Fatal(err)
	}
	next := main.CampaignCoverageScenario()
	next.Resilience.CampaignCoverage.Regression = &main.TestCampaignCoverageRegressionSpec{FromReport: "baseline.json", RunID: baseline.RunID, MinExercisedRate: 0.8}
	normalized := save(next)
	oldOut, oldJSON := (*main.OsStdout), (*main.JsonOutput)
	defer func() { (*main.OsStdout), (*main.JsonOutput) = oldOut, oldJSON }()
	var out bytes.Buffer
	(*main.OsStdout) = &out
	(*main.JsonOutput) = false
	t.Setenv("FAAS_TOKEN", "")
	if main.CmdTest([]string{"--manifest", manifest, "--scenario", "checkout", "--validate"}) != 0 || !strings.Contains(out.String(), "coverage regression baseline") {
		t.Fatal("offline baseline preview missing", out.String())
	}
	out.Reset()
	(*main.JsonOutput) = true
	if main.CmdTest([]string{"--manifest", manifest, "--scenario", "checkout", "--validate"}) != 0 || !strings.Contains(out.String(), "campaign_coverage_regression") {
		t.Fatal("JSON validation baseline missing", out.String())
	}
	result := runCampaignRequirementsTest(t, normalized["checkout"], true, 0)
	if _, err := main.PrepareTestReplay(&result, normalized, dir); err != nil {
		t.Fatal("normalized regression replay failed", err)
	}
}

func TestCampaignCoverageRegressionPartialRateAndCleanup(t *testing.T) {
	s, _, _ := campaignRegressionFixture(t)
	s.Resilience.CampaignCoverage.Regression.MinExercisedRate = 1
	result := main.RunSweepTest(context.Background(), s, func(ctx context.Context, p main.TestRunPlan) main.TestRunReceipt {
		r := main.CampaignCoverageReceipt(t, p, "partial-"+p.ReplayCase, false)
		if p.CampaignSeed.Seed == 42 {
			r.Steps[0].Load.ChaosPhases[3].Chaos.Matches[0].Matches = 0
		}
		return r
	})
	if result.Status != "failed" {
		t.Fatal("partial coverage below floor passed")
	}
	var found bool
	for _, c := range result.FaultMatrix.CampaignCoverage.Regression.Choices {
		if c.Status == "failed" && c.CurrentExercised > 0 && !c.LostCoverage {
			found = true
		}
	}
	if !found {
		t.Fatal("partial regression was not distinguished from lost coverage")
	}
	s.Resilience.CampaignCoverage.Regression.MinExercisedRate = 0
	cleanup := main.RunSweepTest(context.Background(), s, func(ctx context.Context, p main.TestRunPlan) main.TestRunReceipt {
		r := main.CampaignCoverageReceipt(t, p, "cleanup-"+p.ReplayCase, false)
		if p.CampaignSeed.Seed == 44 {
			r.CleanupError = "VM cleanup failed"
		}
		return r
	})
	if cleanup.Status != "failed" || cleanup.CleanupError == "" || cleanup.FaultMatrix.CampaignCoverage.Regression.Status != "passed" {
		t.Fatal("passing regression cleared cleanup failure")
	}
	x, err := main.ExpandTestFaultSweep(s)
	if err != nil {
		t.Fatal(err)
	}
	if err := main.ValidateTestSuiteOptions([]main.TestPreparedScenario{{Name: "checkout", Engine: "real-vm", Scenario: x, Profiles: []string{"warm"}}}, main.TestSuiteOptions{Repeat: 1}); err == nil {
		t.Fatal("suite accepted mismatched regression profile")
	}
}
