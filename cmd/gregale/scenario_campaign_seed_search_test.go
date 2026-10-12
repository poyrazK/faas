package main

import (
	"bytes"
	"encoding/json"
	"gopkg.in/yaml.v3"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func campaignSeedSearchScenario() testScenario {
	s := campaignCoverageScenario()
	c := s.Resilience.CampaignCoverage
	c.Seeds = nil
	c.MaxRuns, c.MaxWorkloadMinutes = 4, 16
	c.MinExercised = map[int]int{0: 2, 1: 2}
	c.SeedSearch = &testCampaignSeedSearchSpec{StartSeed: 42, MaxCandidates: 100, MaxSeeds: 4}
	return s
}

func TestCampaignSeedSearchDeterministicPreviewReplay(t *testing.T) {
	s := campaignSeedSearchScenario()
	before, _ := json.Marshal(s)
	x, err := expandTestFaultSweep(s)
	if err != nil {
		t.Fatal(err)
	}
	y, err := expandTestFaultSweep(s)
	if err != nil || !reflect.DeepEqual(x, y) {
		t.Fatal("nondeterministic selection", err)
	}
	again, err := expandTestFaultSweep(x)
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
			counts[testFaultCampaignDraw(&state)%uint64(len(counts))]++
			_ = testFaultCampaignDraw(&state)
		}
	}
	if !reflect.DeepEqual(counts, c.Preview.Planned) || counts[0] < 2 || counts[1] < 2 {
		t.Fatal("incorrect preview", c.Preview)
	}
	replay := newTestReplayManifest("checkout", "cold", "", x)
	if replay == nil || !replay.CampaignCoverage || replay.Version != 10 {
		t.Fatal("missing aggregate replay")
	}
	changed := campaignSeedSearchScenario()
	changed.Resilience.CampaignCoverage.SeedSearch.MaxCandidates++
	other, err := expandTestFaultSweep(changed)
	if err != nil {
		t.Fatal(err)
	}
	if newTestReplayManifest("checkout", "cold", "", other).ConfigurationSHA256 == replay.ConfigurationSHA256 {
		t.Fatal("search policy not pinned")
	}
	if err := validateTestCampaignCoverageBatch(x, 3); err == nil {
		t.Fatal("combined cap ignored")
	}
}

func TestCampaignSeedSearchBoundsAndFailure(t *testing.T) {
	for _, modify := range []func(*testCampaignCoverageSpec){
		func(c *testCampaignCoverageSpec) { c.Seeds = []uint64{1, 2} },
		func(c *testCampaignCoverageSpec) { c.SeedSearch.MaxCandidates = 10001 },
		func(c *testCampaignCoverageSpec) { c.SeedSearch.StartSeed = ^uint64(0) },
		func(c *testCampaignCoverageSpec) { c.SeedSearch.MaxSeeds = 11 },
		func(c *testCampaignCoverageSpec) { c.MaxRuns = 3 },
		func(c *testCampaignCoverageSpec) { c.MinExercised = nil },
		func(c *testCampaignCoverageSpec) { c.MinExercised = map[int]int{2: 1} },
		func(c *testCampaignCoverageSpec) { c.MaxWorkloadMinutes = 1 },
	} {
		s := campaignSeedSearchScenario()
		modify(s.Resilience.CampaignCoverage)
		if _, err := expandTestFaultSweep(s); err == nil {
			t.Fatal("invalid search accepted")
		}
	}
	s := campaignSeedSearchScenario()
	c := s.Resilience.CampaignCoverage
	c.MinExercised = map[int]int{0: 6, 1: 6}
	c.SeedSearch.MaxCandidates = 2
	if _, err := expandTestFaultSweep(s); err == nil || !strings.Contains(err.Error(), "could not meet") {
		t.Fatal("impossible bounded search accepted", err)
	}
	s = campaignSeedSearchScenario()
	s.Resilience.Runs = 2
	s.Resilience.CampaignCoverage.MaxRuns = 8
	s.Resilience.CampaignCoverage.MaxWorkloadMinutes = 32
	x, err := expandTestFaultSweep(s)
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
	prepared, err := prepareTestReplay(&result, map[string]testScenario{"checkout": s}, ".")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(prepared.Scenario.Resilience.CampaignCoverage.Preview, result.FaultMatrix.CampaignCoverage.SeedSelection) {
		t.Fatal("replay changed selection")
	}
	changed := campaignSeedSearchScenario()
	changed.Resilience.CampaignCoverage.SeedSearch.MaxCandidates++
	if _, err := prepareTestReplay(&result, map[string]testScenario{"checkout": changed}, "."); err == nil {
		t.Fatal("changed search accepted")
	}
	leaf := result.FaultMatrix.Cases[0].Resilience.History[0]
	if _, err := prepareTestReplay(&leaf, map[string]testScenario{"checkout": s}, "."); err != nil {
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
	body, err := yaml.Marshal(testManifest{Version: 1, Scenarios: map[string]testScenario{"checkout": s}})
	if err != nil {
		t.Fatal(err)
	}
	manifest := filepath.Join(dir, "gregale-test.yaml")
	if err := os.WriteFile(manifest, body, 0600); err != nil {
		t.Fatal(err)
	}
	normalized, _, err := readTestManifestForScenario(manifest, "checkout", nil)
	if err != nil {
		t.Fatal(err)
	}
	oldOut, oldJSON := osStdout, jsonOutput
	defer func() { osStdout, jsonOutput = oldOut, oldJSON }()
	var out bytes.Buffer
	osStdout = &out
	jsonOutput = false
	t.Setenv("FAAS_TOKEN", "")
	if cmdTest([]string{"--manifest", manifest, "--scenario", "checkout", "--validate"}) != 0 || !strings.Contains(out.String(), "selected seeds") {
		t.Fatal("offline preview missing", out.String())
	}
	out.Reset()
	jsonOutput = true
	if cmdTest([]string{"--manifest", manifest, "--scenario", "checkout", "--validate"}) != 0 || !strings.Contains(out.String(), "campaign_seed_selection") {
		t.Fatal("JSON preview missing", out.String())
	}
	result := runCampaignRequirementsTest(t, normalized["checkout"], true, 0)
	if _, err := prepareTestReplay(&result, normalized, dir); err != nil {
		t.Fatal("normalized replay failed", err)
	}
}
