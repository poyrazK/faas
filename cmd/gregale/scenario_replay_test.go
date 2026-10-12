package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReplayMatrixConfigurationAndSecrets(t *testing.T) {
	scenario := faultMatrixTestScenario()
	scenario.Secrets = map[string]string{"TOKEN": "env:REPLAY_TOKEN"}
	variant, err := applyTestFaultMatrixCase(scenario, scenario.Resilience.FaultMatrix[1])
	if err != nil {
		t.Fatal(err)
	}
	replay := newTestReplayManifest("checkout", "cold", "reset", variant)
	original := testRunReceipt{Scenario: "checkout", Engine: "real-vm", Profile: "cold", RunID: "original", Status: "failed", Replay: replay}
	prepared, err := prepareTestReplay(&original, map[string]testScenario{"checkout": scenario}, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if prepared.Scenario.Resilience != nil || prepared.Scenario.Steps[1].Load.ChaosSchedule[1].Rules[0].Kind != "tcp_reset" {
		t.Fatal("replay repeated matrix or lost selected fault")
	}
	data, _ := json.Marshal(replay)
	if strings.Contains(string(data), "REPLAY_TOKEN") || strings.Contains(string(data), "Secrets") {
		t.Fatal("replay copied secrets")
	}
	scenario.Timeout = "2m"
	if _, err := prepareTestReplay(&original, map[string]testScenario{"checkout": scenario}, ""); err == nil {
		t.Fatal("changed configuration accepted")
	}
	original.Replay.Version = 2
	if _, err := prepareTestReplay(&original, map[string]testScenario{"checkout": scenario}, ""); err == nil {
		t.Fatal("unsupported version accepted")
	}
}

func TestReplayNestedSelectionAndDuplicateIDs(t *testing.T) {
	leaf := testRunReceipt{RunID: "failure", Status: "failed", Replay: &testReplayManifest{Version: 1}}
	rows := []testRunReceipt{{FaultMatrix: &testFaultMatrixEvidence{Cases: []testRunReceipt{{Resilience: &testResilienceEvidence{History: []testRunReceipt{leaf}}}}}}}
	selected, err := selectTestReplay(rows, "failure")
	if err != nil || selected.RunID != "failure" {
		t.Fatalf("nested selection: %v", err)
	}
	rows = append(rows, leaf)
	if _, err := selectTestReplay(rows, "failure"); err == nil {
		t.Fatal("duplicate IDs accepted")
	}
	if _, err := selectTestReplay(rows, ""); err == nil {
		t.Fatal("empty selector accepted")
	}
	if _, err := selectTestReplay(rows, "missing"); err == nil {
		t.Fatal("missing run accepted")
	}
}

func TestReplayMatrixCasePropagation(t *testing.T) {
	p := testPreparedScenario{Name: "checkout", Engine: "real-vm", Scenario: faultMatrixTestScenario()}
	calls := 0
	runTestFaultMatrix(context.Background(), testRunPlan{Prepared: &p, Profile: "warm"}, func(ctx context.Context, plan testRunPlan) testRunReceipt {
		calls++
		if plan.ReplayCase != p.Scenario.Resilience.FaultMatrix[calls-1].Name {
			t.Fatal("matrix selector not propagated to isolated run")
		}
		return testRunReceipt{Status: "passed"}
	})
	if calls != 2 {
		t.Fatal("matrix did not execute both cases")
	}
}

func TestReplayReportRoundTripAndInputProtection(t *testing.T) {
	scenario := faultMatrixTestScenario()
	scenario.Resilience = nil
	original := testRunReceipt{Scenario: "checkout", Engine: "real-vm", Profile: "warm", RunID: "failed-run", Status: "failed", Replay: newTestReplayManifest("checkout", "warm", "", scenario), ReplayOf: "prior-run"}
	dir := t.TempDir()
	report := filepath.Join(dir, "failed.json")
	if err := writeTestReport(report, []testRunReceipt{original}); err != nil {
		t.Fatal(err)
	}
	rows, _, err := readTestBaselineReport(report, nil)
	if err != nil {
		t.Fatal(err)
	}
	selected, err := selectTestReplay(rows, "failed-run")
	if err != nil || selected.ReplayOf != "prior-run" || selected.Replay.ConfigurationSHA256 != original.Replay.ConfigurationSHA256 {
		t.Fatalf("lost replay metadata: %v", err)
	}
	alias := filepath.Join(dir, "output.json")
	if err := os.Link(report, alias); err != nil {
		t.Fatal(err)
	}
	if cmdTestReplay([]string{"--from", report, "--run-id", "failed-run", "--report", alias}) == 0 {
		t.Fatal("input overwrite accepted")
	}
	after, _, err := readTestBaselineReport(report, nil)
	if err != nil || len(after) != 1 {
		t.Fatal("input report damaged")
	}
}

func TestReplayCLIValidationWithoutPlatform(t *testing.T) {
	dir := t.TempDir()
	for name, body := range map[string]string{"package.json": `{"name":"replay-test","version":"1.0.0","scripts":{"start":"node server.js"}}`, "server.js": `console.log("ready")`, "gregale-test.yaml": "version: 1\nscenarios:\n  replay-test:\n    project: replay-test\n    source: .\n    command: [node, test.js]\n"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
	}
	manifest := filepath.Join(dir, "gregale-test.yaml")
	scenarios, _, err := readTestManifestForScenario(manifest, "replay-test", nil)
	if err != nil {
		t.Fatal(err)
	}
	receipt := testRunReceipt{Scenario: "replay-test", Engine: "real-vm", Profile: "cold", Status: "failed", RunID: "offline-run", Replay: newTestReplayManifest("replay-test", "cold", "", scenarios["replay-test"])}
	report := filepath.Join(dir, "failed.json")
	if err := writeTestReport(report, []testRunReceipt{receipt}); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FAAS_TOKEN", "")
	args := []string{"replay", "--from", report, "--run-id", "offline-run", "--manifest", manifest, "--validate"}
	if code := cmdTest(args); code != 0 {
		t.Fatalf("offline replay validation exit=%d", code)
	}
	body, err := os.ReadFile(manifest)
	if err != nil {
		t.Fatal(err)
	}
	body = append(body, []byte("    timeout: 2m\n")...)
	if err := os.WriteFile(manifest, body, 0600); err != nil {
		t.Fatal(err)
	}
	if code := cmdTest(args); code == 0 {
		t.Fatal("CLI accepted changed configuration")
	}
}
