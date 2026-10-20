package main_test

import (
	"bytes"
	"encoding/json"
	"github.com/onebox-faas/faas/cmd/gregale"
	"gopkg.in/yaml.v3"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func campaignSeedSearchScenario() main.TestScenario {
	s := main.CampaignCoverageScenario()
	c := s.Resilience.CampaignCoverage
	c.Seeds = nil
	c.MaxRuns, c.MaxWorkloadMinutes = 4, 16
	c.MinExercised = map[int]int{0: 2, 1: 2}
	c.SeedSearch = &main.TestCampaignSeedSearchSpec{StartSeed: 42, MaxCandidates: 100, MaxSeeds: 4}
	return s
}

func TestCampaignSeedSearchDeterministicPreviewReplay(t *testing.T) {
	s := campaignSeedSearchScenario()
	before, _ := json.Marshal(s)
	x, err := main.ExpandTestFaultSweep(s)
	if err != nil {
		t.Fatal(err)
	}
	y, err := main.ExpandTestFaultSweep(s)
	if err != nil || !reflect.DeepEqual(x, y) {
		t.Fatal("nondeterministic selection", err)
	}
	again, err := main.ExpandTestFaultSweep(x)
	if err != nil || !reflect.DeepEqual(x, again) {
		t.Fatal("expansion not idempotent", err)
	}
	after, _ := json.Marshal(s)
	if string(before) != string(after) {
		t.Fatal("source mutated")
	}
	c := x.Resilience.CampaignCoverage
	if len(c.Seeds) < 2 || len(c.Seeds) > 4 || c.Preview.Candidates > 100 {
		t.Fatal("unbounded search", c)
	}
	counts := make([]int, len(c.Preview.Planned))
	for _, seed := range c.Seeds {
		state := seed
		for i := 0; i < 3; i++ {
			counts[main.TestFaultCampaignDraw(&state)%uint64(len(counts))]++
			_ = main.TestFaultCampaignDraw(&state)
		}
	}
	if !reflect.DeepEqual(counts, c.Preview.Planned) || counts[0] < 2 || counts[1] < 2 {
		t.Fatal("incorrect preview", c.Preview)
	}
	replay := main.NewTestReplayManifest("checkout", "cold", "", x)
	if replay == nil || !replay.CampaignCoverage || replay.Version != 10 {
		t.Fatal("missing aggregate replay")
	}
	changed := campaignSeedSearchScenario()
	changed.Resilience.CampaignCoverage.SeedSearch.MaxCandidates++
	other, err := main.ExpandTestFaultSweep(changed)
	if err != nil {
		t.Fatal(err)
	}
	if main.NewTestReplayManifest("checkout", "cold", "", other).ConfigurationSHA256 == replay.ConfigurationSHA256 {
		t.Fatal("search policy not pinned")
	}
	if err := main.ValidateTestCampaignCoverageBatch(x, 3); err == nil {
		t.Fatal("combined cap ignored")
	}
}

func TestCampaignSeedSearchBoundsAndFailure(t *testing.T) {
	for _, modify := range []func(*main.TestCampaignCoverageSpec){
		func(c *main.TestCampaignCoverageSpec) { c.Seeds = []uint64{1, 2} },
		func(c *main.TestCampaignCoverageSpec) { c.SeedSearch.MaxCandidates = 10001 },
		func(c *main.TestCampaignCoverageSpec) { c.SeedSearch.StartSeed = ^uint64(0) },
		func(c *main.TestCampaignCoverageSpec) { c.SeedSearch.MaxSeeds = 11 },
		func(c *main.TestCampaignCoverageSpec) { c.MaxRuns = 3 },
		func(c *main.TestCampaignCoverageSpec) { c.MinExercised = nil },
		func(c *main.TestCampaignCoverageSpec) { c.MinExercised = map[int]int{2: 1} },
		func(c *main.TestCampaignCoverageSpec) { c.MaxWorkloadMinutes = 1 },
	} {
		s := campaignSeedSearchScenario()
		modify(s.Resilience.CampaignCoverage)
		if _, err := main.ExpandTestFaultSweep(s); err == nil {
			t.Fatal("invalid search accepted")
		}
	}
	s := campaignSeedSearchScenario()
	c := s.Resilience.CampaignCoverage
	c.MinExercised = map[int]int{0: 6, 1: 6}
	c.SeedSearch.MaxCandidates = 2
	if _, err := main.ExpandTestFaultSweep(s); err == nil || !strings.Contains(err.Error(), "could not meet") {
		t.Fatal("impossible bounded search accepted", err)
	}
	s = campaignSeedSearchScenario()
	s.Resilience.Runs = 2
	s.Resilience.CampaignCoverage.MaxRuns = 8
	s.Resilience.CampaignCoverage.MaxWorkloadMinutes = 32
	x, err := main.ExpandTestFaultSweep(s)
	if err != nil {
		t.Fatal(err)
	}
	for _, count := range x.Resilience.CampaignCoverage.Preview.Planned {
		if count%2 != 0 {
			t.Fatal("repeat count missing")
		}
	}
}

func TestCampaignSeedSearchActualCoverageAndReplay(t *testing.T) {
	s := campaignSeedSearchScenario()
	result := runCampaignRequirementsTest(t, s, true, 0)
	if result.Status != "failed" || result.FaultMatrix.CampaignCoverage.SeedSelection == nil {
		t.Fatal("planned coverage bypassed observed gate")
	}
	prepared, err := main.PrepareTestReplay(&result, map[string]main.TestScenario{"checkout": s}, ".")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(prepared.Scenario.Resilience.CampaignCoverage.Preview, result.FaultMatrix.CampaignCoverage.SeedSelection) {
		t.Fatal("replay changed selection")
	}
	changed := campaignSeedSearchScenario()
	changed.Resilience.CampaignCoverage.SeedSearch.MaxCandidates++
	if _, err := main.PrepareTestReplay(&result, map[string]main.TestScenario{"checkout": changed}, "."); err == nil {
		t.Fatal("changed search accepted")
	}
	leaf := result.FaultMatrix.Cases[0].Resilience.History[0]
	if _, err := main.PrepareTestReplay(&leaf, map[string]main.TestScenario{"checkout": s}, "."); err != nil {
		t.Fatal("selected seed replay failed", err)
	}
}

func TestCampaignSeedSearchOfflineValidationPreview(t *testing.T) {
	dir := t.TempDir()
	for name, body := range map[string]string{"package.json": `{"scripts":{"start":"node server.js"}}`, "server.js": "console.log('fixture');"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
	}
	s := campaignSeedSearchScenario()
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
	oldOut, oldJSON := (*main.OsStdout), (*main.JsonOutput)
	defer func() { (*main.OsStdout), (*main.JsonOutput) = oldOut, oldJSON }()
	var out bytes.Buffer
	(*main.OsStdout) = &out
	(*main.JsonOutput) = false
	t.Setenv("FAAS_TOKEN", "")
	if main.CmdTest([]string{"--manifest", manifest, "--scenario", "checkout", "--validate"}) != 0 || !strings.Contains(out.String(), "selected seeds") {
		t.Fatal("offline preview missing", out.String())
	}
	out.Reset()
	(*main.JsonOutput) = true
	if main.CmdTest([]string{"--manifest", manifest, "--scenario", "checkout", "--validate"}) != 0 || !strings.Contains(out.String(), "campaign_seed_selection") {
		t.Fatal("JSON preview missing", out.String())
	}
	result := runCampaignRequirementsTest(t, normalized["checkout"], true, 0)
	if _, err := main.PrepareTestReplay(&result, normalized, dir); err != nil {
		t.Fatal("normalized replay failed", err)
	}
}
