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

func campaignCumulativeFixture(t *testing.T) (main.TestScenario, []main.TestRunReceipt, []string) {
	t.Helper()
	s, first, firstPath := campaignGuidanceFixture(t)
	prior := main.CampaignCoverageScenario()
	prior.Resilience.CampaignCoverage.Seeds = []uint64{45, 46, 47}
	prior.Resilience.CampaignCoverage.MinExercised = map[int]int{0: 2, 1: 2}
	second := main.RunSweepTest(context.Background(), prior, func(ctx context.Context, p main.TestRunPlan) main.TestRunReceipt {
		r := main.CampaignCoverageReceipt(t, p, "second-"+p.ReplayCase, false)
		for i := range r.Steps[0].Load.ChaosPhases {
			x := r.Steps[0].Load.ChaosPhases[i].Chaos
			if x != nil && x.Rules[0].Kind == "tcp_reset" {
				x.Matches[0].Matches = 0
			}
		}
		return r
	})
	secondPath := filepath.Join(t.TempDir(), "second.json")
	if err := main.WriteTestReport(secondPath, []main.TestRunReceipt{second}); err != nil {
		t.Fatal(err)
	}
	c := s.Resilience.CampaignCoverage
	c.MinExercised[0] = first.FaultMatrix.CampaignCoverage.Choices[0].Exercised + second.FaultMatrix.CampaignCoverage.Choices[0].Exercised
	c.SeedSearch.FromReport, c.SeedSearch.RunID = "", ""
	c.SeedSearch.Reports = []main.TestCampaignGuidanceInput{{FromReport: firstPath, RunID: first.RunID}, {FromReport: secondPath, RunID: second.RunID}}
	return s, []main.TestRunReceipt{first, second}, []string{firstPath, secondPath}
}

func TestCampaignCumulativeCoveragePreviewAndReplay(t *testing.T) {
	s, prior, paths := campaignCumulativeFixture(t)
	before, _ := json.Marshal(s)
	x, err := main.ExpandTestFaultSweep(s)
	if err != nil {
		t.Fatal(err)
	}
	c := x.Resilience.CampaignCoverage
	g := c.Preview.Guidance
	if len(g.Contributions) != 2 || g.Observed[0] != s.Resilience.CampaignCoverage.MinExercised[0] || g.Observed[1] != 0 || !reflect.DeepEqual(c.MinExercised, map[int]int{1: 2}) || !reflect.DeepEqual(g.ExcludedSeeds, []uint64{42, 43, 44, 45, 46, 47}) {
		t.Fatal("incorrect cumulative coverage", g)
	}
	for i, batch := range g.Contributions {
		if batch.RunID != prior[i].RunID || len(batch.ReportSHA256) != 64 || batch.Observed[0] != prior[i].FaultMatrix.CampaignCoverage.Choices[0].Exercised {
			t.Fatal("missing batch provenance", batch)
		}
	}
	for _, seed := range c.Seeds {
		if seed < 48 {
			t.Fatal("prior seed reused")
		}
	}
	if c.Preview.Candidates < 8 {
		t.Fatal("excluded candidates not bounded")
	}
	again, err := main.ExpandTestFaultSweep(s)
	if err != nil || !reflect.DeepEqual(again, x) {
		t.Fatal("cumulative selection changed", err)
	}
	after, _ := json.Marshal(s)
	if !bytes.Equal(before, after) {
		t.Fatal("source mutated")
	}
	result := runCampaignRequirementsTest(t, s, true, 0)
	if result.Status != "failed" || result.FaultMatrix.CampaignCoverage.Choices[1].Exercised != 0 {
		t.Fatal("prior coverage bypassed new gate")
	}
	prepared, err := main.PrepareTestReplay(&result, map[string]main.TestScenario{"checkout": s}, ".")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(prepared.Scenario.Resilience.CampaignCoverage.Preview, c.Preview) {
		t.Fatal("replay changed cumulative selection")
	}
	var out bytes.Buffer
	main.PrintTestCampaignCoverage(&out, result.FaultMatrix.CampaignCoverage)
	for _, row := range prior {
		if !strings.Contains(out.String(), row.RunID) {
			t.Fatal("terminal provenance missing")
		}
	}
	html := filepath.Join(t.TempDir(), "coverage.html")
	if err := main.WriteTestHTMLReport(html, "", []main.TestRunReceipt{result}); err != nil {
		t.Fatal(err)
	}
	body, _ := os.ReadFile(html)
	for _, row := range prior {
		if !strings.Contains(string(body), row.RunID) {
			t.Fatal("HTML provenance missing")
		}
	}
	junit := filepath.Join(t.TempDir(), "coverage.xml")
	if err := main.WriteTestJUnit(junit, []main.TestRunReceipt{result}); err != nil {
		t.Fatal(err)
	}
	body, _ = os.ReadFile(junit)
	if !strings.Contains(string(body), "contributions") {
		t.Fatal("JUnit cumulative evidence missing")
	}
	for _, path := range paths {
		if err := main.ValidateTestCampaignGuidanceOptions(x, "checkout", []string{"cold"}, []string{path}); err == nil {
			t.Fatal("contributing input overwrite allowed")
		}
	}
	// Every report's exact bytes are pinned, including a non-first input.
	f, err := os.OpenFile(paths[1], os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = f.WriteString("\n")
	_ = f.Close()
	if err := main.ValidateTestCampaignGuidanceOptions(x, "checkout", []string{"cold"}, nil); err == nil {
		t.Fatal("changed second input allowed")
	}
	if _, err := main.PrepareTestReplay(&result, map[string]main.TestScenario{"checkout": s}, "."); err == nil {
		t.Fatal("changed second report replay allowed")
	}
}

func TestCampaignCumulativeRejectsDuplicatesAndScope(t *testing.T) {
	for _, kind := range []string{"root", "leaf", "cross-root-leaf", "profile", "scenario", "single-list", "empty-list", "too-many", "legacy-mixed", "missing-path", "missing-id", "summary", "workload", "already-met", "excluded-range"} {
		t.Run(kind, func(t *testing.T) {
			s, rows, paths := campaignCumulativeFixture(t)
			search := s.Resilience.CampaignCoverage.SeedSearch
			leaf := &rows[1].FaultMatrix.Cases[0].Resilience.History[0]
			switch kind {
			case "root":
				search.Reports[1] = search.Reports[0]
			case "leaf":
				leaf.RunID = rows[0].FaultMatrix.Cases[0].Resilience.History[0].RunID
				rows[1].FaultMatrix.CampaignCoverage.Seeds[0].Runs[0].RunID = leaf.RunID
			case "cross-root-leaf":
				leaf.RunID = rows[0].RunID
				rows[1].FaultMatrix.CampaignCoverage.Seeds[0].Runs[0].RunID = leaf.RunID
			case "profile":
				rows[1].Profile = "warm"
				for i := range rows[1].FaultMatrix.Cases {
					group := &rows[1].FaultMatrix.Cases[i]
					group.Profile = "warm"
					for j := range group.Resilience.History {
						leaf := &group.Resilience.History[j]
						leaf.Profile = "warm"
						leaf.Replay.Profile = "warm"
					}
				}
			case "scenario":
				rows[1].Scenario = "other"
				for i := range rows[1].FaultMatrix.Cases {
					group := &rows[1].FaultMatrix.Cases[i]
					group.Scenario = "other"
					for j := range group.Resilience.History {
						leaf := &group.Resilience.History[j]
						leaf.Scenario = "other"
						leaf.Replay.Scenario = "other"
					}
				}
			case "empty-list":
				search.Reports = []main.TestCampaignGuidanceInput{}
			case "single-list":
				search.Reports = search.Reports[:1]
			case "too-many":
				search.Reports = make([]main.TestCampaignGuidanceInput, 11)
			case "legacy-mixed":
				search.FromReport = paths[0]
				search.RunID = rows[0].RunID
			case "missing-path":
				search.Reports[1].FromReport = ""
			case "missing-id":
				search.Reports[1].RunID = ""
			case "summary":
				rows[1].FaultMatrix.CampaignCoverage.Choices[1].Exercised++
			case "workload":
				s.Timeout = "2m"
			case "already-met":
				s.Resilience.CampaignCoverage.MinExercised = map[int]int{0: 1}
			case "excluded-range":
				search.MaxCandidates = 6
			}
			if err := main.WriteTestReport(paths[1], []main.TestRunReceipt{rows[1]}); err != nil {
				t.Fatal(err)
			}
			if _, err := main.ExpandTestFaultSweep(s); err == nil {
				t.Fatal("invalid cumulative guidance accepted")
			}
		})
	}
}

func TestCampaignCumulativeDoesNotCountNestedGuidance(t *testing.T) {
	s, first, path := campaignGuidanceFixture(t)
	second := runCampaignRequirementsTest(t, s, true, 0)
	secondPath := filepath.Join(t.TempDir(), "second.json")
	if err := main.WriteTestReport(secondPath, []main.TestRunReceipt{second}); err != nil {
		t.Fatal(err)
	}
	next := campaignSeedSearchScenario()
	next.Resilience.CampaignCoverage.SeedSearch.Reports = []main.TestCampaignGuidanceInput{{FromReport: path, RunID: first.RunID}, {FromReport: secondPath, RunID: second.RunID}}
	x, err := main.ExpandTestFaultSweep(next)
	if err != nil {
		t.Fatal(err)
	}
	g := x.Resilience.CampaignCoverage.Preview.Guidance
	if g.Observed[0] != first.FaultMatrix.CampaignCoverage.Choices[0].Exercised || g.Contributions[1].Observed[0] != 0 {
		t.Fatal("nested provenance double-counted", g)
	}
}

func TestCampaignCumulativeSameFileAndOverlappingSeeds(t *testing.T) {
	s, rows, paths := campaignCumulativeFixture(t)
	combined := filepath.Join(t.TempDir(), "combined.json")
	if err := main.WriteTestReport(combined, rows); err != nil {
		t.Fatal(err)
	}
	for i := range s.Resilience.CampaignCoverage.SeedSearch.Reports {
		s.Resilience.CampaignCoverage.SeedSearch.Reports[i].FromReport = combined
	}
	if _, err := main.ExpandTestFaultSweep(s); err != nil {
		t.Fatal("distinct roots in same report rejected", err)
	}
	// Two genuine executions of the same seeds are independent observations.
	prior := main.CampaignCoverageScenario()
	prior.Resilience.CampaignCoverage.MinExercised = map[int]int{0: 2, 1: 2}
	second := main.RunSweepTest(context.Background(), prior, func(ctx context.Context, p main.TestRunPlan) main.TestRunReceipt {
		return main.CampaignCoverageReceipt(t, p, "repeat-"+p.ReplayCase, false)
	})
	if err := main.WriteTestReport(paths[1], []main.TestRunReceipt{second}); err != nil {
		t.Fatal(err)
	}
	s.Resilience.CampaignCoverage.SeedSearch.Reports = []main.TestCampaignGuidanceInput{{FromReport: paths[0], RunID: rows[0].RunID}, {FromReport: paths[1], RunID: second.RunID}}
	expected := rows[0].FaultMatrix.CampaignCoverage.Choices[0].Exercised + second.FaultMatrix.CampaignCoverage.Choices[0].Exercised
	s.Resilience.CampaignCoverage.MinExercised = map[int]int{0: expected + 2}
	x, err := main.ExpandTestFaultSweep(s)
	if err != nil {
		t.Fatal(err)
	}
	g := x.Resilience.CampaignCoverage.Preview.Guidance
	if g.Observed[0] != expected || !reflect.DeepEqual(g.ExcludedSeeds, []uint64{42, 43, 44}) || g.Remaining[0] != 2 {
		t.Fatal("independent repeated seeds miscounted", g)
	}
}

func TestCampaignCumulativeOfflineRelativePaths(t *testing.T) {
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
	prior.Resilience.CampaignCoverage.MinExercised = map[int]int{0: 2, 1: 2}
	first := runCampaignRequirementsTest(t, save(prior)["checkout"], true, 0)
	prior.Resilience.CampaignCoverage.Seeds = []uint64{45, 46, 47}
	second := runCampaignRequirementsTest(t, save(prior)["checkout"], true, 0)
	for i := range second.FaultMatrix.Cases {
		group := &second.FaultMatrix.Cases[i]
		for j := range group.Resilience.History {
			group.Resilience.History[j].RunID = "second-" + group.Resilience.History[j].RunID
			second.FaultMatrix.CampaignCoverage.Seeds[i].Runs[j].RunID = group.Resilience.History[j].RunID
		}
	}
	for name, row := range map[string]main.TestRunReceipt{"first.json": first, "second.json": second} {
		if err := main.WriteTestReport(filepath.Join(dir, name), []main.TestRunReceipt{row}); err != nil {
			t.Fatal(err)
		}
	}
	next := campaignSeedSearchScenario()
	next.Resilience.CampaignCoverage.SeedSearch.Reports = []main.TestCampaignGuidanceInput{{FromReport: "first.json", RunID: first.RunID}, {FromReport: "second.json", RunID: second.RunID}}
	normalized := save(next)
	oldOut, oldJSON := (*main.OsStdout), (*main.JsonOutput)
	defer func() { (*main.OsStdout), (*main.JsonOutput) = oldOut, oldJSON }()
	var out bytes.Buffer
	(*main.OsStdout) = &out
	(*main.JsonOutput) = false
	t.Setenv("FAAS_TOKEN", "")
	if main.CmdTest([]string{"--manifest", manifest, "--scenario", "checkout", "--validate"}) != 0 || !strings.Contains(out.String(), "guided by 2 campaign batches") || !strings.Contains(out.String(), second.RunID) {
		t.Fatal("cumulative offline preview missing", out.String())
	}
	out.Reset()
	(*main.JsonOutput) = true
	if main.CmdTest([]string{"--manifest", manifest, "--scenario", "checkout", "--validate"}) != 0 || !strings.Contains(out.String(), "contributions") {
		t.Fatal("cumulative JSON preview missing", out.String())
	}
	result := runCampaignRequirementsTest(t, normalized["checkout"], true, 0)
	if _, err := main.PrepareTestReplay(&result, normalized, dir); err != nil {
		t.Fatal("cumulative normalized replay failed", err)
	}
}
