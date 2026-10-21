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

func TestCampaignCoverageDropBoundariesAndSampleMinimum(t *testing.T) {
	s, _, _ := campaignRegressionFixture(t)
	x, err := main.ExpandTestFaultSweep(s)
	if err != nil {
		t.Fatal(err)
	}
	reg := x.Resilience.CampaignCoverage.Regression
	reg.MinExercisedRate = 0.5
	reg.MinPlannedEvents = 5
	reg.MaxDropPercentagePoints = map[int]float64{0: 30}
	reg.Baseline.Planned = []int{10, 10, 0}
	reg.Baseline.Exercised = []int{9, 1, 0}
	e := &main.TestCampaignCoverageEvidence{Choices: []main.TestCampaignChoiceCoverage{{Index: 0, Planned: 10, Exercised: 6}, {Index: 1, Planned: 10, Exercised: 5}, {Index: 2}}}
	if err := main.EvaluateTestCampaignCoverageRegression(x, e); err != nil {
		t.Fatal("equal drop boundary failed", err)
	}
	c := e.Regression.Choices[0]
	if c.DropPercentagePoints == nil || *c.DropPercentagePoints != 30 || c.DropStatus != "passed" || c.CurrentRate != 0.6 || c.BaselineRate != 0.9 {
		t.Fatal("wrong percentage-point calculation", c)
	}
	if e.Regression.Choices[1].DropStatus != "" || e.Regression.Choices[2].Status != "not_planned" {
		t.Fatal("unconfigured choices gained checks")
	}
	reg.MaxDropPercentagePoints[0] = 29.99
	if err := main.EvaluateTestCampaignCoverageRegression(x, e); err == nil || e.Regression.Choices[0].LostCoverage || e.Regression.Choices[0].CurrentRate < reg.MinExercisedRate {
		t.Fatal("partial drop above absolute floor was missed")
	}
	reg.MaxDropPercentagePoints[0] = 0
	e.Choices[0].Exercised = 9
	if err := main.EvaluateTestCampaignCoverageRegression(x, e); err != nil || *e.Regression.Choices[0].MaxDropPercentagePoints != 0 {
		t.Fatal("zero allowance equality failed", err)
	}
	e.Choices[0].Exercised = 10
	if err := main.EvaluateTestCampaignCoverageRegression(x, e); err != nil || *e.Regression.Choices[0].DropPercentagePoints != -10 {
		t.Fatal("improvement was treated as regression", err)
	}
	e.Choices[0].Exercised = 8
	if err := main.EvaluateTestCampaignCoverageRegression(x, e); err == nil {
		t.Fatal("zero allowance permitted a drop")
	}
	reg.MaxDropPercentagePoints[0] = 100
	reg.MinExercisedRate = 0
	e.Choices[0].Exercised = 0
	if err := main.EvaluateTestCampaignCoverageRegression(x, e); err == nil || !e.Regression.Choices[0].LostCoverage || e.Regression.Choices[0].DropStatus != "passed" {
		t.Fatal("tolerance cleared complete coverage loss")
	}
	e.Choices[0].Exercised = 9
	reg.MinPlannedEvents = 11
	if err := main.EvaluateTestCampaignCoverageRegression(x, e); err == nil || e.Regression.Choices[0].DropPercentagePoints != nil {
		t.Fatal("insufficient samples passed drop check")
	}
	reg.MinPlannedEvents = 2
	reg.MaxDropPercentagePoints[0] = 100.0 / 3
	reg.Baseline.Planned[0] = 3
	reg.Baseline.Exercised[0] = 3
	e.Choices[0].Planned = 3
	e.Choices[0].Exercised = 2
	if err := main.EvaluateTestCampaignCoverageRegression(x, e); err != nil {
		t.Fatal("fractional count boundary failed", err)
	}
	reg.MaxDropPercentagePoints[0] = math.Nextafter(reg.MaxDropPercentagePoints[0], math.Inf(-1))
	if err := main.EvaluateTestCampaignCoverageRegression(x, e); err == nil {
		t.Fatal("fractional boundary permitted a larger drop")
	}
}

func TestCampaignCoverageDropValidation(t *testing.T) {
	for _, kind := range []string{"missing-min", "one", "too-many", "negative-index", "bad-index", "negative-drop", "over-drop", "nan", "infinity", "insufficient", "orphan-min"} {
		t.Run(kind, func(t *testing.T) {
			s, baseline, _ := campaignRegressionFixture(t)
			reg := s.Resilience.CampaignCoverage.Regression
			reg.MaxDropPercentagePoints = map[int]float64{0: 20}
			reg.MinPlannedEvents = 2
			switch kind {
			case "missing-min":
				reg.MinPlannedEvents = 0
			case "one":
				reg.MinPlannedEvents = 1
			case "too-many":
				reg.MinPlannedEvents = 181
			case "negative-index":
				reg.MaxDropPercentagePoints = map[int]float64{-1: 20}
			case "bad-index":
				reg.MaxDropPercentagePoints = map[int]float64{2: 20}
			case "negative-drop":
				reg.MaxDropPercentagePoints[0] = -0.1
			case "over-drop":
				reg.MaxDropPercentagePoints[0] = 100.1
			case "nan":
				reg.MaxDropPercentagePoints[0] = math.NaN()
			case "infinity":
				reg.MaxDropPercentagePoints[0] = math.Inf(1)
			case "insufficient":
				reg.MinPlannedEvents = baseline.FaultMatrix.CampaignCoverage.Choices[0].Planned + 1
			case "orphan-min":
				reg.MaxDropPercentagePoints = nil
			}
			if _, err := main.ExpandTestFaultSweep(s); err == nil {
				t.Fatal("invalid drop policy accepted")
			}
		})
	}
	s, baseline, _ := campaignRegressionFixture(t)
	reg := s.Resilience.CampaignCoverage.Regression
	reg.MaxDropPercentagePoints = map[int]float64{0: 0}
	reg.MinPlannedEvents = baseline.FaultMatrix.CampaignCoverage.Choices[0].Planned
	before, _ := json.Marshal(s)
	x, err := main.ExpandTestFaultSweep(s)
	if err != nil {
		t.Fatal("exact minimum rejected", err)
	}
	after, _ := json.Marshal(s)
	if !bytes.Equal(before, after) {
		t.Fatal("drop policy mutated source")
	}
	again, err := main.ExpandTestFaultSweep(x)
	if err != nil || !reflect.DeepEqual(x, again) {
		t.Fatal("drop expansion is not idempotent", err)
	}
}

func TestCampaignCoverageDropExecutionReportsAndReplay(t *testing.T) {
	s, baseline, _ := campaignRegressionFixture(t)
	reg := s.Resilience.CampaignCoverage.Regression
	reg.MinExercisedRate = 0.5
	reg.MinPlannedEvents = 2
	reg.MaxDropPercentagePoints = map[int]float64{0: 0}
	calls := 0
	result := main.RunSweepTest(context.Background(), s, func(ctx context.Context, p main.TestRunPlan) main.TestRunReceipt {
		calls++
		r := main.CampaignCoverageReceipt(t, p, "drop-"+p.ReplayCase, false)
		if p.CampaignSeed.Seed == 42 {
			r.Steps[0].Load.ChaosPhases[3].Chaos.Matches[0].Matches = 0
		}
		return r
	})
	if calls != 3 || result.Status != "failed" || result.FaultMatrix.Passed != 3 {
		t.Fatal("drop did not fail passing campaign", result.Error)
	}
	c := result.FaultMatrix.CampaignCoverage.Regression.Choices[0]
	if c.LostCoverage || c.CurrentRate < reg.MinExercisedRate || c.DropStatus != "failed" || c.MaxDropPercentagePoints == nil || *c.MaxDropPercentagePoints != 0 {
		t.Fatal("drop-only failure missing", c)
	}
	if main.FinishTestRunReports("", []main.TestRunReceipt{result}, "", "") != 1 {
		t.Fatal("drop did not fail CI")
	}
	prepared, err := main.PrepareTestReplay(&result, map[string]main.TestScenario{"checkout": s}, ".")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(prepared.Scenario.Resilience.CampaignCoverage.Regression.MaxDropPercentagePoints, reg.MaxDropPercentagePoints) {
		t.Fatal("replay lost tolerance")
	}
	for _, kind := range []string{"allowance", "minimum", "remove"} {
		changed := s
		r := *s.Resilience
		cov := *r.CampaignCoverage
		edit := *reg
		edit.MaxDropPercentagePoints = map[int]float64{0: 0}
		switch kind {
		case "allowance":
			edit.MaxDropPercentagePoints[0] = 100
		case "minimum":
			edit.MinPlannedEvents = 3
		case "remove":
			edit.MaxDropPercentagePoints = nil
			edit.MinPlannedEvents = 0
		}
		cov.Regression = &edit
		r.CampaignCoverage = &cov
		changed.Resilience = &r
		if _, err := main.PrepareTestReplay(&result, map[string]main.TestScenario{"checkout": changed}, "."); err == nil {
			t.Fatal("changed drop policy replay allowed", kind)
		}
	}
	var out bytes.Buffer
	main.PrintTestCampaignCoverage(&out, result.FaultMatrix.CampaignCoverage)
	if !strings.Contains(out.String(), "allowed 0.000000") || !strings.Contains(out.String(), "minimum 2 planned events") {
		t.Fatal("terminal drop evidence missing", out.String())
	}
	dir := t.TempDir()
	html := filepath.Join(dir, "drop.html")
	junit := filepath.Join(dir, "drop.xml")
	report := filepath.Join(dir, "drop.json")
	if err := main.WriteTestHTMLReport(html, "", []main.TestRunReceipt{result}); err != nil {
		t.Fatal(err)
	}
	if err := main.WriteTestJUnit(junit, []main.TestRunReceipt{result}); err != nil {
		t.Fatal(err)
	}
	if err := main.WriteTestReport(report, []main.TestRunReceipt{result}); err != nil {
		t.Fatal(err)
	}
	htmlBody, _ := os.ReadFile(html)
	if !strings.Contains(string(htmlBody), " / 0 (failed)") {
		t.Fatal("zero allowance missing from HTML", string(htmlBody))
	}
	for _, path := range []string{html, junit, report} {
		body, _ := os.ReadFile(path)
		if !strings.Contains(string(body), "coverage dropped") || !strings.Contains(string(body), baseline.RunID) {
			t.Fatal("drop report missing", path)
		}
	}
	encoded, _ := json.Marshal(result.FaultMatrix.CampaignCoverage.Regression)
	if !strings.Contains(string(encoded), `"max_drop_percentage_points":0`) {
		t.Fatal("zero tolerance omitted from evidence")
	}
}

func TestCampaignCoverageDropOfflineValidationAndReplay(t *testing.T) {
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
	next.Resilience.CampaignCoverage.Regression = &main.TestCampaignCoverageRegressionSpec{FromReport: "baseline.json", RunID: baseline.RunID, MinExercisedRate: 0.5, MaxDropPercentagePoints: map[int]float64{0: 0}, MinPlannedEvents: 2}
	normalized := save(next)
	oldOut, oldJSON := (*main.OsStdout), (*main.JsonOutput)
	defer func() { (*main.OsStdout), (*main.JsonOutput) = oldOut, oldJSON }()
	var out bytes.Buffer
	(*main.OsStdout) = &out
	(*main.JsonOutput) = false
	t.Setenv("FAAS_TOKEN", "")
	if main.CmdTest([]string{"--manifest", manifest, "--scenario", "checkout", "--validate"}) != 0 || !strings.Contains(out.String(), "drop allowances (percentage points)") {
		t.Fatal("drop preview missing", out.String())
	}
	out.Reset()
	(*main.JsonOutput) = true
	if main.CmdTest([]string{"--manifest", manifest, "--scenario", "checkout", "--validate"}) != 0 || !strings.Contains(out.String(), "min_planned_events") {
		t.Fatal("JSON drop preview missing", out.String())
	}
	result := runCampaignRequirementsTest(t, normalized["checkout"], true, 0)
	if _, err := main.PrepareTestReplay(&result, normalized, dir); err != nil {
		t.Fatal("normalized drop replay failed", err)
	}
}
