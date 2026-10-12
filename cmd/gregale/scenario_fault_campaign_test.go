package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func faultCampaignScenario() testScenario {
	s := outageCyclesScenario()
	l := s.Steps[1].Load
	l.OutageCycles = nil
	l.ChaosSchedule[1].Rules = l.ChaosSchedule[1].Rules[:1]
	seed := uint64(42)
	rule := l.ChaosSchedule[1].Rules[0]
	reset := rule
	reset.Kind, reset.Latency = "tcp_reset", ""
	l.FaultCampaign = &testFaultCampaignSpec{Seed: &seed, Count: 3, MaxDuration: "20s", Choices: []testFaultCampaignChoice{{Duration: "2s", Rule: rule}, {Duration: "1s", Rule: reset}}}
	return s
}

func TestFaultCampaignDeterminismAndIsolation(t *testing.T) {
	state := uint64(0)
	if testFaultCampaignDraw(&state) != 16294208416658607535 {
		t.Fatal("campaign algorithm changed")
	}
	s := faultCampaignScenario()
	before, _ := json.Marshal(s)
	x, err := expandTestOutageCycles(s)
	if err != nil {
		t.Fatal(err)
	}
	y, err := expandTestOutageCycles(s)
	if err != nil || !reflect.DeepEqual(x, y) {
		t.Fatal("campaign is not deterministic", err)
	}
	y, err = expandTestOutageCycles(x)
	if err != nil || !reflect.DeepEqual(x, y) {
		t.Fatal("campaign redrawn", err)
	}
	l := x.Steps[1].Load
	if len(l.ChaosSchedule) != 7 || l.ChaosSchedule[1].Rules[0].Seed == l.ChaosSchedule[3].Rules[0].Seed {
		t.Fatal("missing independent seeded faults")
	}
	// Golden choices pin draw order as well as the PRNG itself.
	if l.ChaosSchedule[1].Rules[0].Kind != "tcp_reset" || l.ChaosSchedule[3].Rules[0].Kind != "tcp_latency" || l.ChaosSchedule[5].Rules[0].Kind != "tcp_latency" || l.Duration != "9s" {
		t.Fatal("campaign schedule changed", l.ChaosSchedule, l.Duration)
	}
	l.ChaosSchedule[1].Rules[0].Percent = 1
	after, _ := json.Marshal(s)
	if !bytes.Equal(before, after) {
		t.Fatal("source campaign mutated")
	}
}

func TestFaultCampaignValidation(t *testing.T) {
	for _, kind := range []string{"seed", "count", "choices", "cap", "total", "duration", "kind", "target", "port", "invalid-rule", "template", "cycles", "top-level"} {
		t.Run(kind, func(t *testing.T) {
			s := faultCampaignScenario()
			l, c := s.Steps[1].Load, s.Steps[1].Load.FaultCampaign
			switch kind {
			case "seed":
				c.Seed = nil
			case "count":
				c.Count = 10
			case "choices":
				c.Choices = c.Choices[:1]
			case "cap":
				c.MaxDuration = "6m"
			case "total":
				c.MaxDuration = "2s"
			case "duration":
				c.Choices[1].Duration = "1.000001s"
			case "kind":
				c.Choices[1].Rule.Kind = "http_status"
			case "target":
				c.Choices[1].Rule.To = "database"
			case "port":
				c.Choices[1].Rule.Port = 5432
			case "invalid-rule":
				c.Choices[1].Rule.Percent = 101
			case "template":
				l.ChaosSchedule[1].Rules = nil
			case "cycles":
				l.OutageCycles = &testOutageCyclesSpec{Count: 3}
			case "top-level":
				s.Load, s.Steps = l, nil
			}
			if _, err := expandTestOutageCycles(s); err == nil {
				t.Fatal("invalid campaign accepted")
			}
		})
	}
	s := faultCampaignScenario()
	*s.Steps[1].Load.FaultCampaign.Seed = 0
	if err := validateTestScenarioSteps(s, nil); err != nil {
		t.Fatal("explicit zero seed rejected", err)
	}
}

func TestFaultCampaignReplayAndReports(t *testing.T) {
	s := faultCampaignScenario()
	x, err := expandTestOutageCycles(s)
	if err != nil {
		t.Fatal(err)
	}
	cfg := outageCyclesConfig(t, x)
	load := outageCyclesReceipt(t, cfg)
	load.ChaosPhases[4].Recovery.Passed = false
	if err := applyTestOutageCycles(load, cfg, nil); err == nil || load.OutageCycles.FirstFailedCycle != 2 {
		t.Fatal("campaign recovery failure missing", err)
	}
	if load.OutageCycles.Campaign == nil || len(load.OutageCycles.Campaign.Schedule) != 7 || load.OutageCycles.Campaign.Seed != 42 {
		t.Fatal("campaign schedule evidence missing")
	}
	row := testRunReceipt{Scenario: "checkout", Engine: "real-vm", Profile: "cold", RunID: "campaign-failed", Status: "failed", Replay: newTestReplayManifest("checkout", "cold", "", x), Steps: []testScenarioStepEvidence{{Name: "load", Load: load}}}
	replayed, err := prepareTestReplay(&row, map[string]testScenario{"checkout": s}, ".")
	if err != nil || !reflect.DeepEqual(replayed.Scenario.Steps[1].Load.ChaosSchedule, x.Steps[1].Load.ChaosSchedule) {
		t.Fatal("campaign replay differs", err)
	}
	changed := faultCampaignScenario()
	*changed.Steps[1].Load.FaultCampaign.Seed = 43
	if _, err := prepareTestReplay(&row, map[string]testScenario{"checkout": changed}, "."); err == nil {
		t.Fatal("changed seed accepted")
	}
	changed = faultCampaignScenario()
	changed.Steps[1].Load.FaultCampaign.Choices[0].Rule.Latency = "200ms"
	if _, err := prepareTestReplay(&row, map[string]testScenario{"checkout": changed}, "."); err == nil {
		t.Fatal("changed choice accepted")
	}
	dir := t.TempDir()
	html := filepath.Join(dir, "report.html")
	if err := writeTestHTMLReport(html, "campaign", []testRunReceipt{row}); err != nil {
		t.Fatal(err)
	}
	body, _ := os.ReadFile(html)
	if !strings.Contains(string(body), "Fault campaign seed 42") || !strings.Contains(string(body), "splitmix64-v1") {
		t.Fatal("HTML campaign evidence missing")
	}
	var out bytes.Buffer
	printTestLoadSummary(&out, load)
	if !strings.Contains(out.String(), "fault campaign: seed 42") {
		t.Fatal("terminal campaign evidence missing")
	}
}

func TestFaultCampaignSequenceMinimizationAndReplay(t *testing.T) {
	source := faultCampaignScenario()
	x, err := expandTestOutageCycles(source)
	if err != nil {
		t.Fatal(err)
	}
	target := testMinimizationTarget{Step: "load", Phase: "recovery-cycle-3"}
	makeReceipt := func(s testScenario, id string) testRunReceipt {
		cfg := outageCyclesConfig(t, s)
		load := outageCyclesReceipt(t, cfg)
		load.Requests, load.Status = 70, "failed"
		load.ChaosPhases[6].Recovery.Passed = false
		load.ChaosPhases[6].Recovery.WindowsChecked = 5
		load.ChaosPhases[6].Recovery.Error = "no sustained healthy interval within the recovery deadline"
		_ = applyTestOutageCycles(load, cfg, nil)
		return testRunReceipt{Scenario: "checkout", Engine: "real-vm", Profile: "cold", RunID: id, Status: "failed", Replay: newTestReplayManifest("checkout", "cold", "", s), Steps: []testScenarioStepEvidence{{Name: "load", Load: load}}}
	}
	original := makeReceipt(x, "campaign-original")
	opts := minimizationTestOptions()
	opts.Target = target
	result := runTestFaultMinimization(context.Background(), testPreparedScenario{Name: "checkout", Engine: "real-vm", Scenario: x}, &original, opts, func(ctx context.Context, plan testRunPlan) testRunReceipt {
		return makeReceipt(plan.Prepared.Scenario, fmt.Sprintf("campaign-%d", plan.Attempt))
	})
	if result.Status != "passed" || result.Minimization.RemainingRules != 1 || len(result.Minimization.RemovedRules) != 2 {
		t.Fatalf("sequence minimization failed: %+v", result.Minimization)
	}
	selected, err := selectTestReplay([]testRunReceipt{result}, result.Minimization.BestRunID)
	if err != nil {
		t.Fatal(err)
	}
	replayed, err := prepareTestReplay(selected, map[string]testScenario{"checkout": source}, ".")
	if err != nil {
		t.Fatal(err)
	}
	l := replayed.Scenario.Steps[1].Load
	if len(l.ChaosSchedule[1].Rules) != 0 || len(l.ChaosSchedule[3].Rules) != 0 || !reflect.DeepEqual(l.ChaosSchedule[5], x.Steps[1].Load.ChaosSchedule[5]) || l.Duration != x.Steps[1].Load.Duration {
		t.Fatal("replay redrew minimized campaign or changed timing")
	}
}

func TestFaultCampaignOfflineCLI(t *testing.T) {
	s := faultCampaignScenario()
	dir := t.TempDir()
	for name, body := range map[string]string{"package.json": `{"scripts":{"start":"node server.js"}}`, "server.js": "console.log('fixture');"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
	}
	body, err := yaml.Marshal(testManifest{Version: 1, Scenarios: map[string]testScenario{"checkout": s}})
	if err != nil {
		t.Fatal(err)
	}
	manifest := filepath.Join(dir, "gregale-test.yaml")
	if err := os.WriteFile(manifest, body, 0600); err != nil {
		t.Fatal(err)
	}
	scenarios, _, err := readTestManifestForScenario(manifest, "checkout", nil)
	if err != nil {
		t.Fatal(err)
	}
	row := testRunReceipt{Scenario: "checkout", Engine: "real-vm", Profile: "cold", RunID: "campaign-cli", Status: "failed", Replay: newTestReplayManifest("checkout", "cold", "", scenarios["checkout"])}
	report := filepath.Join(dir, "failed.json")
	if err := writeTestReport(report, []testRunReceipt{row}); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FAAS_TOKEN", "")
	if cmdTest([]string{"--manifest", manifest, "--scenario", "checkout", "--validate"}) != 0 || cmdTestReplay([]string{"--manifest", manifest, "--from", report, "--run-id", row.RunID, "--validate"}) != 0 {
		t.Fatal("offline campaign validation/replay failed")
	}
}
