package main_test

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/cmd/gregale"
	"gopkg.in/yaml.v3"
)

func campaignGuidanceFixture(t *testing.T) (main.TestScenario, main.TestRunReceipt, string) {
	t.Helper()
	s := main.CampaignCoverageScenario()
	s.Resilience.CampaignCoverage.MinExercised = map[int]int{0: 2, 1: 2}
	// Retain observed matches only for choice 0, creating a real choice-1 deficit.
	result := main.RunSweepTest(context.Background(), s, func(ctx context.Context, p main.TestRunPlan) main.TestRunReceipt {
		r := main.CampaignCoverageReceipt(t, p, "prior-"+p.ReplayCase, false)
		for i := range r.Steps[0].Load.ChaosPhases {
			x := r.Steps[0].Load.ChaosPhases[i].Chaos
			if x != nil && x.Rules[0].Kind == "tcp_reset" {
				x.Matches[0].Matches = 0
			}
		}
		return r
	})
	path := filepath.Join(t.TempDir(), "prior.json")
	if err := main.WriteTestReport(path, []main.TestRunReceipt{result}); err != nil {
		t.Fatal(err)
	}
	next := campaignSeedSearchScenario()
	next.Resilience.CampaignCoverage.SeedSearch.FromReport = path
	next.Resilience.CampaignCoverage.SeedSearch.RunID = result.RunID
	return next, result, path
}

func TestCampaignGuidanceDeficitsExclusionsAndReplay(t *testing.T) {
	s, prior, _ := campaignGuidanceFixture(t)
	before, _ := json.Marshal(s)
	x, err := main.ExpandTestFaultSweep(s)
	if err != nil {
		t.Fatal(err)
	}
	c := x.Resilience.CampaignCoverage
	g := c.Preview.Guidance
	if g == nil || g.RunID != prior.RunID || len(g.ReportSHA256) != 64 || g.Observed[0] < 2 || g.Observed[1] != 0 || !reflect.DeepEqual(c.MinExercised, map[int]int{1: 2}) {
		t.Fatal("incorrect deficit", g, c.MinExercised)
	}
	for _, seed := range c.Seeds {
		for _, old := range g.ExcludedSeeds {
			if seed == old {
				t.Fatal("reused prior seed")
			}
		}
	}
	if c.Preview.Candidates < 5 {
		t.Fatal("excluded seeds not counted within candidate limit")
	}
	y, err := main.ExpandTestFaultSweep(s)
	if err != nil || !reflect.DeepEqual(x, y) {
		t.Fatal("nondeterministic guidance", err)
	}
	after, _ := json.Marshal(s)
	if !bytes.Equal(before, after) {
		t.Fatal("source mutated")
	}
	failed := runCampaignRequirementsTest(t, s, true, 0)
	if failed.Status != "failed" || failed.FaultMatrix.CampaignCoverage.Choices[0].MinExercised != 0 || failed.FaultMatrix.CampaignCoverage.Choices[1].RequirementStatus != "failed" {
		t.Fatal("prior counts passed new gate")
	}
	prepared, err := main.PrepareTestReplay(&failed, map[string]main.TestScenario{"checkout": s}, ".")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(prepared.Scenario.Resilience.CampaignCoverage.Preview, c.Preview) {
		t.Fatal("replay changed batch")
	}
	passed := runCampaignRequirementsTest(t, s, false, 0)
	if passed.Status != "passed" {
		t.Fatal("additional observed coverage failed", passed.Error)
	}
	if err := main.ValidateTestCampaignGuidanceOptions(x, "other", []string{"cold"}, nil); err == nil {
		t.Fatal("scenario mismatch allowed")
	}
	if err := main.ValidateTestCampaignGuidanceOptions(x, "checkout", []string{"warm"}, nil); err == nil {
		t.Fatal("profile mismatch allowed")
	}
	var calls int
	wrong := main.RunTestFaultMatrix(context.Background(), main.TestRunPlan{Prepared: &main.TestPreparedScenario{Name: "checkout", Engine: "real-vm", Scenario: x}, Profile: "warm"}, func(context.Context, main.TestRunPlan) main.TestRunReceipt {
		calls++
		return main.TestRunReceipt{Status: "passed"}
	})
	if wrong.Status != "failed" || calls != 0 {
		t.Fatal("incompatible profile executed")
	}
}

func TestCampaignGuidanceRejectsInvalidEvidenceAndChangedInputs(t *testing.T) {
	for _, kind := range []string{"duplicate-root", "duplicate-leaf", "summary", "choice", "cleanup", "skipped", "missing-history", "workload", "already-met", "missing-id", "excluded-range", "attempt", "seed-metadata", "run-counts", "replay", "seed-summary", "interrupted", "aborted-last-cycle"} {
		t.Run(kind, func(t *testing.T) {
			s, prior, path := campaignGuidanceFixture(t)
			rows := []main.TestRunReceipt{prior}
			switch kind {
			case "aborted-last-cycle":
				rows[0].FaultMatrix.Cases[0].Resilience.History[0].Steps[0].Load.OutageCycles.Completed--
			case "interrupted":
				rows[0].FaultMatrix.Cases[0].Resilience.History[0].Steps[0].Load.ChaosPhases[6].FinishedAt = rows[0].FaultMatrix.Cases[0].Resilience.History[0].Steps[0].Load.ChaosPhases[6].StartedAt
			case "attempt":
				rows[0].FaultMatrix.Cases[0].Resilience.History[0].Attempt = 2
			case "seed-metadata":
				rows[0].FaultMatrix.Cases[0].Resilience.History[0].CampaignSeed = nil
			case "run-counts":
				rows[0].FaultMatrix.Cases[0].Resilience.Passed++
			case "seed-summary":
				rows[0].FaultMatrix.CampaignCoverage.Seeds[0].Passed++
			case "replay":
				rows[0].FaultMatrix.Cases[0].Resilience.History[0].Replay.ConfigurationSHA256 = "changed"
			case "duplicate-root":
				rows = append(rows, prior)
			case "duplicate-leaf":
				rows[0].FaultMatrix.Cases[1].Resilience.History[0].RunID = rows[0].FaultMatrix.Cases[0].Resilience.History[0].RunID
			case "summary":
				rows[0].FaultMatrix.CampaignCoverage.Choices[1].Exercised++
			case "choice":
				rows[0].FaultMatrix.CampaignCoverage.Choices[1].Duration = "3s"
			case "cleanup":
				rows[0].CleanupError = "VM remains"
			case "skipped":
				rows[0].FaultMatrix.Cases[0].Resilience.History[0].Status = "skipped"
			case "missing-history":
				rows[0].FaultMatrix.Cases[0].Resilience.History = nil
			case "workload":
				s.Timeout = "2m"
			case "already-met":
				s.Resilience.CampaignCoverage.MinExercised = map[int]int{0: 1}
			case "missing-id":
				s.Resilience.CampaignCoverage.SeedSearch.RunID = ""
			case "excluded-range":
				s.Resilience.CampaignCoverage.SeedSearch.MaxCandidates = 3
			}
			if err := main.WriteTestReport(path, rows); err != nil {
				t.Fatal(err)
			}
			if _, err := main.ExpandTestFaultSweep(s); err == nil {
				t.Fatal("invalid guidance accepted")
			}
		})
	}
	s, _, path := campaignGuidanceFixture(t)
	x, err := main.ExpandTestFaultSweep(s)
	if err != nil {
		t.Fatal(err)
	}
	if err := main.ValidateTestCampaignGuidanceOptions(x, "checkout", []string{"cold"}, []string{path}); err == nil {
		t.Fatal("input overwrite allowed")
	}
	hardlink := filepath.Join(t.TempDir(), "alias.json")
	if err := os.Link(path, hardlink); err != nil {
		t.Fatal(err)
	}
	if err := main.ValidateTestCampaignGuidanceOptions(x, "checkout", []string{"cold"}, []string{hardlink}); err == nil {
		t.Fatal("hardlink overwrite allowed")
	}
	failed := runCampaignRequirementsTest(t, s, true, 0)
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = f.WriteString("\n")
	_ = f.Close()
	if err := main.ValidateTestCampaignGuidanceOptions(x, "checkout", []string{"cold"}, nil); err == nil {
		t.Fatal("changed report allowed")
	}
	if _, err := main.PrepareTestReplay(&failed, map[string]main.TestScenario{"checkout": s}, "."); err == nil {
		t.Fatal("changed report replay allowed")
	}
}

func TestCampaignGuidanceOfflineRelativePathAndReports(t *testing.T) {
	dir := t.TempDir()
	for name, body := range map[string]string{"package.json": `{"scripts":{"start":"node server.js"}}`, "server.js": "console.log('fixture');"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
	}
	s := main.CampaignCoverageScenario()
	s.Resilience.CampaignCoverage.MinExercised = map[int]int{0: 2, 1: 2}
	save := func(s main.TestScenario) string {
		t.Helper()
		body, err := yaml.Marshal(main.TestManifest{Version: 1, Scenarios: map[string]main.TestScenario{"checkout": s}})
		if err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(dir, "gregale-test.yaml")
		if err := os.WriteFile(path, body, 0600); err != nil {
			t.Fatal(err)
		}
		return path
	}
	manifest := save(s)
	normalized, _, err := main.ReadTestManifestForScenario(manifest, "checkout", nil)
	if err != nil {
		t.Fatal(err)
	}
	prior := runCampaignRequirementsTest(t, normalized["checkout"], true, 0)
	report := filepath.Join(dir, "prior.json")
	if err := main.WriteTestReport(report, []main.TestRunReceipt{prior}); err != nil {
		t.Fatal(err)
	}
	next := campaignSeedSearchScenario()
	next.Resilience.CampaignCoverage.SeedSearch.FromReport = "prior.json"
	next.Resilience.CampaignCoverage.SeedSearch.RunID = prior.RunID
	manifest = save(next)
	normalized, _, err = main.ReadTestManifestForScenario(manifest, "checkout", nil)
	if err != nil {
		t.Fatal(err)
	}
	oldOut, oldJSON := (*main.OsStdout), (*main.JsonOutput)
	defer func() { (*main.OsStdout), (*main.JsonOutput) = oldOut, oldJSON }()
	var out bytes.Buffer
	(*main.OsStdout) = &out
	(*main.JsonOutput) = false
	t.Setenv("FAAS_TOKEN", "")
	if main.CmdTest([]string{"--manifest", manifest, "--scenario", "checkout", "--validate"}) != 0 || !strings.Contains(out.String(), "guided by run") {
		t.Fatal("guidance preview missing", out.String())
	}
	out.Reset()
	(*main.JsonOutput) = true
	if main.CmdTest([]string{"--manifest", manifest, "--scenario", "checkout", "--validate"}) != 0 || !strings.Contains(out.String(), "report_sha256") {
		t.Fatal("JSON guidance missing", out.String())
	}
	result := runCampaignRequirementsTest(t, normalized["checkout"], true, 0)
	if _, err := main.PrepareTestReplay(&result, normalized, dir); err != nil {
		t.Fatal("normalized replay failed", err)
	}
	html := filepath.Join(dir, "report.html")
	if err := main.WriteTestHTMLReport(html, "", []main.TestRunReceipt{result}); err != nil {
		t.Fatal(err)
	}
	body, _ := os.ReadFile(html)
	if !strings.Contains(string(body), prior.RunID) || !strings.Contains(string(body), "Guidance run") {
		t.Fatal("HTML guidance missing")
	}
}

func TestCampaignGuidanceLegacyPassingLeavesAndRepeatedRuns(t *testing.T) {
	s, prior, path := campaignGuidanceFixture(t)
	for i := range prior.FaultMatrix.Cases {
		prior.FaultMatrix.Cases[i].Resilience.History[0].Replay = nil
	}
	if err := main.WriteTestReport(path, []main.TestRunReceipt{prior}); err != nil {
		t.Fatal(err)
	}
	if _, err := main.ExpandTestFaultSweep(s); err != nil {
		t.Fatal("legacy passing leaves rejected", err)
	}
	s.Resilience.Runs = 2
	s.Resilience.CampaignCoverage.MaxRuns = 8
	s.Resilience.CampaignCoverage.MaxWorkloadMinutes = 32
	x, err := main.ExpandTestFaultSweep(s)
	if err != nil {
		t.Fatal(err)
	}
	for _, count := range x.Resilience.CampaignCoverage.Preview.Planned {
		if count%2 != 0 {
			t.Fatal("new batch repeat weights missing")
		}
	}
	if err := main.ValidateTestSuiteOptions([]main.TestPreparedScenario{{Name: "checkout", Engine: "real-vm", Scenario: x, Profiles: []string{"warm"}}}, main.TestSuiteOptions{Repeat: 1}); err == nil {
		t.Fatal("suite mismatched profile allowed")
	}
}
