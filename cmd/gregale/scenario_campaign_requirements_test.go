package main_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/cmd/gregale"
	"gopkg.in/yaml.v3"
)

func runCampaignRequirementsTest(t *testing.T, s main.TestScenario, noMatches bool, failedSeed uint64) main.TestRunReceipt {
	t.Helper()
	calls := 0
	return main.RunSweepTest(context.Background(), s, func(ctx context.Context, plan main.TestRunPlan) main.TestRunReceipt {
		calls++
		r := main.CampaignCoverageReceipt(t, plan, fmt.Sprintf("requirements-leaf-%d", calls), failedSeed != 0 && plan.CampaignSeed.Seed == failedSeed)
		if noMatches {
			for i := range r.Steps[0].Load.ChaosPhases {
				if x := r.Steps[0].Load.ChaosPhases[i].Chaos; x != nil {
					x.Matches[0].Matches = 0
				}
			}
		}
		return r
	})
}

func TestCampaignRequirementsGateAndExactBoundary(t *testing.T) {
	s := main.CampaignCoverageScenario()
	baseline := runCampaignRequirementsTest(t, s, false, 0)
	limits := map[int]int{}
	for _, choice := range baseline.FaultMatrix.CampaignCoverage.Choices {
		limits[choice.Index] = choice.Exercised
	}
	s.Resilience.CampaignCoverage.MinExercised = limits
	result := runCampaignRequirementsTest(t, s, false, 0)
	if result.Status != "passed" || result.FaultMatrix.CampaignCoverage.RequirementsStatus != "passed" {
		t.Fatal("exact coverage boundary rejected", result.Error)
	}
	limits[0]++
	result = runCampaignRequirementsTest(t, s, false, 0)
	e := result.FaultMatrix.CampaignCoverage
	if result.Status != "failed" || result.FaultMatrix.Passed != 3 || result.FaultMatrix.Failed != 0 || e.Choices[0].RequirementStatus != "failed" || !strings.Contains(result.Error, "choice 0 exercised") {
		t.Fatal("passing SLOs hid missing coverage", result.Error)
	}
	if result.RunID == "" || result.Replay == nil || !result.Replay.CampaignCoverage || result.Replay.Version != 10 {
		t.Fatal("batch failure cannot be replayed")
	}
	if main.FinishTestRunReports("", []main.TestRunReceipt{result}, "", "") != 1 {
		t.Fatal("coverage failure did not fail CI exit status")
	}
	s.Resilience.CampaignCoverage.MinExercised = map[int]int{0: 1}
	result = runCampaignRequirementsTest(t, s, true, 0)
	if result.Status != "failed" || result.FaultMatrix.CampaignCoverage.Choices[0].Exercised != 0 || result.FaultMatrix.CampaignCoverage.Choices[1].RequirementStatus != "" {
		t.Fatal("unmatched or optional choices satisfied requirements")
	}
	s.Resilience.CampaignCoverage.MinExercised = nil
	result = runCampaignRequirementsTest(t, s, true, 0)
	if result.Status != "passed" || result.FaultMatrix.CampaignCoverage.RequirementsStatus != "" || result.Replay != nil {
		t.Fatal("omitted coverage requirements changed behavior")
	}
}

func TestCampaignRequirementsValidationAndUnselectedChoices(t *testing.T) {
	for _, invalid := range []map[int]int{{-1: 1}, {2: 1}, {0: 0}, {0: -1}, {0: 10}} {
		s := main.CampaignCoverageScenario()
		s.Resilience.CampaignCoverage.MinExercised = invalid
		if _, err := main.ExpandTestFaultSweep(s); err == nil {
			t.Fatal("invalid requirement accepted", invalid)
		}
	}
	s := main.CampaignCoverageScenario()
	c := s.Steps[1].Load.FaultCampaign
	for len(c.Choices) < 16 {
		c.Choices = append(c.Choices, c.Choices[0])
	}
	baseline := runCampaignRequirementsTest(t, s, false, 0)
	missing := -1
	for _, choice := range baseline.FaultMatrix.CampaignCoverage.Choices {
		if choice.Planned == 0 {
			missing = choice.Index
			break
		}
	}
	if missing < 0 {
		t.Fatal("fixture must include an unselected choice")
	}
	s.Resilience.CampaignCoverage.MinExercised = map[int]int{missing: 1}
	result := runCampaignRequirementsTest(t, s, false, 0)
	if result.Status != "failed" || result.FaultMatrix.CampaignCoverage.Choices[missing].Planned != 0 || result.FaultMatrix.CampaignCoverage.Choices[missing].RequirementStatus != "failed" {
		t.Fatal("unselected required choice passed")
	}
}

func TestCampaignRequirementsBatchAndLeafReplay(t *testing.T) {
	s := main.CampaignCoverageScenario()
	s.Resilience.CampaignCoverage.MinExercised = map[int]int{0: 1}
	result := runCampaignRequirementsTest(t, s, true, 0)
	selected, err := main.SelectTestReplay([]main.TestRunReceipt{result}, result.RunID)
	if err != nil {
		t.Fatal(err)
	}
	prepared, err := main.PrepareTestReplay(selected, map[string]main.TestScenario{"checkout": s}, ".")
	if err != nil || prepared.Scenario.Resilience == nil || len(prepared.Scenario.Resilience.CampaignCoverage.Seeds) != 3 || prepared.Scenario.Resilience.CampaignCoverage.MinExercised[0] != 1 {
		t.Fatal("batch replay dropped seeds or requirements", err)
	}
	calls := 0
	replayed := main.RunTestFaultMatrix(context.Background(), main.TestRunPlan{Prepared: &prepared, Profile: "cold", ReplayManifest: result.Replay, ReplayOf: result.RunID}, func(ctx context.Context, plan main.TestRunPlan) main.TestRunReceipt {
		if plan.ReplayManifest != nil {
			t.Fatal("batch replay descriptor leaked into leaf")
		}
		return main.RunTestResilience(ctx, plan, func(ctx context.Context, plan main.TestRunPlan) main.TestRunReceipt {
			calls++
			return main.CampaignCoverageReceipt(t, plan, fmt.Sprintf("requirements-replay-%d", calls), plan.CampaignSeed.Seed == 43)
		})
	})
	if calls != 3 || replayed.ReplayOf != result.RunID || replayed.FaultMatrix.CampaignCoverage.RequirementsStatus != "passed" || replayed.Status != "failed" {
		t.Fatal("batch replay did not preserve separate SLO failures")
	}
	leaf := replayed.FaultMatrix.Cases[1].Resilience.History[0]
	if leaf.Replay.Version != 1 || leaf.Replay.CampaignCoverage || leaf.Replay.MatrixCase != "campaign-seed-43" {
		t.Fatal("leaf replay lost isolated scope")
	}
	if _, err := main.PrepareTestReplay(&leaf, map[string]main.TestScenario{"checkout": s}, "."); err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{"minimum", "remove", "seeds", "runs", "version", "marker", "edits", "case"} {
		t.Run(kind, func(t *testing.T) {
			changed := main.CampaignCoverageScenario()
			changed.Resilience.CampaignCoverage.MinExercised = map[int]int{0: 1}
			row := result
			descriptor := *result.Replay
			row.Replay = &descriptor
			switch kind {
			case "minimum":
				changed.Resilience.CampaignCoverage.MinExercised[0] = 2
			case "remove":
				changed.Resilience.CampaignCoverage.MinExercised = nil
			case "seeds":
				changed.Resilience.CampaignCoverage.Seeds[1] = 45
			case "runs":
				changed.Resilience.Runs = 2
				changed.Resilience.CampaignCoverage.MaxRuns = 6
				changed.Resilience.CampaignCoverage.MaxWorkloadMinutes = 24
			case "version":
				descriptor.Version = 9
			case "marker":
				descriptor.CampaignCoverage = false
			case "edits":
				descriptor.RemovedRules = []main.TestFaultRuleRef{{Step: "load", Phase: "fault", Index: 0}}
			case "case":
				descriptor.MatrixCase = "campaign-seed-43"
			}
			if _, err := main.PrepareTestReplay(&row, map[string]main.TestScenario{"checkout": changed}, "."); err == nil {
				t.Fatal("changed or inconsistent batch replay accepted")
			}
			if kind == "minimum" || kind == "remove" {
				if _, err := main.PrepareTestReplay(&leaf, map[string]main.TestScenario{"checkout": changed}, "."); err == nil {
					t.Fatal("leaf replay ignored changed coverage requirements")
				}
			}
		})
	}
}

func TestCampaignRequirementsReportsAndOfflineReplay(t *testing.T) {
	s := main.CampaignCoverageScenario()
	s.Resilience.CampaignCoverage.MinExercised = map[int]int{0: 1, 1: 1}
	result := runCampaignRequirementsTest(t, s, true, 0)
	dir := t.TempDir()
	html, xml, report := filepath.Join(dir, "requirements.html"), filepath.Join(dir, "requirements.xml"), filepath.Join(dir, "requirements.json")
	if err := main.WriteTestHTMLReport(html, "", []main.TestRunReceipt{result}); err != nil {
		t.Fatal(err)
	}
	if err := main.WriteTestJUnit(xml, []main.TestRunReceipt{result}); err != nil {
		t.Fatal(err)
	}
	if err := main.WriteTestReport(report, []main.TestRunReceipt{result}); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{html, xml, report} {
		body, _ := os.ReadFile(path)
		if !strings.Contains(string(body), "campaign coverage requirements failed") || !strings.Contains(string(body), result.RunID) {
			t.Fatal("coverage failure or replay identity missing", path)
		}
	}
	var out bytes.Buffer
	main.PrintTestCampaignCoverage(&out, result.FaultMatrix.CampaignCoverage)
	if !strings.Contains(out.String(), "choice 0: 0 exercised, requires 1 (failed)") || !strings.Contains(out.String(), "choice 1: 0 exercised, requires 1 (failed)") {
		t.Fatal("terminal requirement details missing", out.String())
	}
	for name, body := range map[string]string{"package.json": `{"scripts":{"start":"node server.js"}}`, "server.js": "console.log('fixture');"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
	}
	body, err := yaml.Marshal(main.TestManifest{Version: 1, Scenarios: map[string]main.TestScenario{"checkout": s}})
	if err != nil {
		t.Fatal(err)
	}
	manifest := filepath.Join(dir, "gregale-test.yaml")
	if err := os.WriteFile(manifest, body, 0600); err != nil {
		t.Fatal(err)
	}
	normalized, _, err := main.ReadTestManifestForScenario(manifest, "checkout", nil)
	if err != nil {
		t.Fatal(err)
	}
	result = runCampaignRequirementsTest(t, normalized["checkout"], true, 0)
	if err := main.WriteTestReport(report, []main.TestRunReceipt{result}); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FAAS_TOKEN", "")
	if main.CmdTest([]string{"--manifest", manifest, "--scenario", "checkout", "--validate"}) != 0 || main.CmdTestReplay([]string{"--manifest", manifest, "--from", report, "--run-id", result.RunID, "--validate"}) != 0 {
		t.Fatal("offline requirement validation or batch replay failed")
	}
	encoded, _ := json.Marshal(result.Replay)
	if !strings.Contains(string(encoded), `"campaign_coverage":true`) || strings.Contains(string(encoded), "Choices") {
		t.Fatal("batch replay descriptor copied source payload")
	}
}

func TestCampaignRequirementsDuplicateEvidenceCannotPass(t *testing.T) {
	s := main.CampaignCoverageScenario()
	baseline := runCampaignRequirementsTest(t, s, false, 0)
	s.Resilience.CampaignCoverage.MinExercised = map[int]int{0: baseline.FaultMatrix.CampaignCoverage.Choices[0].Exercised + 1}
	result := main.RunSweepTest(context.Background(), s, func(ctx context.Context, plan main.TestRunPlan) main.TestRunReceipt {
		r := main.CampaignCoverageReceipt(t, plan, fmt.Sprintf("requirements-duplicate-%d", plan.CampaignSeed.Seed), false)
		if plan.CampaignSeed.Seed == 42 {
			p := r.Steps[0].Load.ChaosPhases[3]
			r.Steps[0].Load.ChaosPhases = append(r.Steps[0].Load.ChaosPhases, p)
		}
		return r
	})
	if result.Status != "failed" || result.FaultMatrix.CampaignCoverage.Choices[0].Exercised != baseline.FaultMatrix.CampaignCoverage.Choices[0].Exercised-1 {
		t.Fatal("duplicate event satisfied coverage target")
	}
}

func TestCampaignRequirementsCannotOverrideInterruptedOrFailedCleanup(t *testing.T) {
	for _, kind := range []string{"cancel", "cleanup"} {
		t.Run(kind, func(t *testing.T) {
			s := main.CampaignCoverageScenario()
			s.Resilience.CampaignCoverage.MinExercised = map[int]int{0: 1}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			result := main.RunSweepTest(ctx, s, func(ctx context.Context, plan main.TestRunPlan) main.TestRunReceipt {
				r := main.CampaignCoverageReceipt(t, plan, "requirements-stopped", false)
				if kind == "cancel" {
					cancel()
				} else {
					r.CleanupError = "cleanup failed"
				}
				return r
			})
			if result.Status != "failed" || result.FaultMatrix.Skipped != 2 || result.FaultMatrix.CampaignCoverage.RequirementsStatus != "passed" || result.Error == "" {
				t.Fatal("coverage gate waived interruption or cleanup error")
			}
		})
	}
}

func TestCampaignRequirementsSurviveLeafMinimization(t *testing.T) {
	s := main.CampaignCoverageScenario()
	s.Resilience.CampaignCoverage.MinExercised = map[int]int{0: 1}
	result := runCampaignRequirementsTest(t, s, false, 43)
	original := result.FaultMatrix.Cases[1].Resilience.History[0]
	prepared, err := main.PrepareTestReplay(&original, map[string]main.TestScenario{"checkout": s}, ".")
	if err != nil {
		t.Fatal(err)
	}
	opts := main.MinimizationTestOptions()
	opts.Target = main.TestMinimizationTarget{Step: "load", Phase: "recovery-cycle-2"}
	minimized := main.RunTestFaultMinimization(context.Background(), prepared, &original, opts, func(ctx context.Context, plan main.TestRunPlan) main.TestRunReceipt {
		return main.CampaignCoverageReceipt(t, plan, fmt.Sprintf("requirements-min-%d", plan.Attempt), true)
	})
	if minimized.Status != "passed" || minimized.Minimization.RemainingRules != 1 {
		t.Fatal("leaf minimization failed", minimized.Error)
	}
	selected, err := main.SelectTestReplay([]main.TestRunReceipt{minimized}, minimized.Minimization.BestRunID)
	if err != nil {
		t.Fatal(err)
	}
	best, err := main.PrepareTestReplay(selected, map[string]main.TestScenario{"checkout": s}, ".")
	if err != nil || best.Scenario.Steps[1].Load.FaultCampaign.CoverageRequirements[0] != 1 || len(best.Scenario.Steps[1].Load.ChaosSchedule[1].Rules) != 0 {
		t.Fatal("minimized replay lost requirements or restored removed fault", err)
	}
	if selected.Steps[0].Load.OutageCycles.Campaign.CoverageRequirements[0] != 1 {
		t.Fatal("minimized run evidence lost inherited requirements")
	}
}
