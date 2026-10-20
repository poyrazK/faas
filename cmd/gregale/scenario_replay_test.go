package main_test

import (
	"context"
	"encoding/json"
	"github.com/onebox-faas/faas/cmd/gregale"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReplayMatrixConfigurationAndSecrets(t *testing.T) {
	scenario := main.FaultMatrixTestScenario()
	scenario.Secrets = map[string]string{"TOKEN": "env:REPLAY_TOKEN"}
	variant, err := main.ApplyTestFaultMatrixCase(scenario, scenario.Resilience.FaultMatrix[1])
	if err != nil {
		t.Fatal(err)
	}
	replay := main.NewTestReplayManifest("checkout", "cold", "reset", variant)
	original := main.TestRunReceipt{Scenario: "checkout", Engine: "real-vm", Profile: "cold", RunID: "original", Status: "failed", Replay: replay}
	prepared, err := main.PrepareTestReplay(&original, map[string]main.TestScenario{"checkout": scenario}, t.TempDir())
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
	if _, err := main.PrepareTestReplay(&original, map[string]main.TestScenario{"checkout": scenario}, ""); err == nil {
		t.Fatal("changed configuration accepted")
	}
	original.Replay.Version = 2
	if _, err := main.PrepareTestReplay(&original, map[string]main.TestScenario{"checkout": scenario}, ""); err == nil {
		t.Fatal("unsupported version accepted")
	}
}

func TestReplayNestedSelectionAndDuplicateIDs(t *testing.T) {
	leaf := main.TestRunReceipt{RunID: "failure", Status: "failed", Replay: &main.TestReplayManifest{Version: 1}}
	rows := []main.TestRunReceipt{{FaultMatrix: &main.TestFaultMatrixEvidence{Cases: []main.TestRunReceipt{{Resilience: &main.TestResilienceEvidence{History: []main.TestRunReceipt{leaf}}}}}}}
	selected, err := main.SelectTestReplay(rows, "failure")
	if err != nil || selected.RunID != "failure" {
		t.Fatalf("nested selection: %v", err)
	}
	rows = append(rows, leaf)
	if _, err := main.SelectTestReplay(rows, "failure"); err == nil {
		t.Fatal("duplicate IDs accepted")
	}
	if _, err := main.SelectTestReplay(rows, ""); err == nil {
		t.Fatal("empty selector accepted")
	}
	if _, err := main.SelectTestReplay(rows, "missing"); err == nil {
		t.Fatal("missing run accepted")
	}
}

func TestReplayMatrixCasePropagation(t *testing.T) {
	p := main.TestPreparedScenario{Name: "checkout", Engine: "real-vm", Scenario: main.FaultMatrixTestScenario()}
	calls := 0
	main.RunTestFaultMatrix(context.Background(), main.TestRunPlan{Prepared: &p, Profile: "warm"}, func(ctx context.Context, plan main.TestRunPlan) main.TestRunReceipt {
		calls++
		if plan.ReplayCase != p.Scenario.Resilience.FaultMatrix[calls-1].Name {
			t.Fatal("matrix selector not propagated to isolated run")
		}
		return main.TestRunReceipt{Status: "passed"}
	})
	if calls != 2 {
		t.Fatal("matrix did not execute both cases")
	}
}

func TestReplayReportRoundTripAndInputProtection(t *testing.T) {
	scenario := main.FaultMatrixTestScenario()
	scenario.Resilience = nil
	original := main.TestRunReceipt{Scenario: "checkout", Engine: "real-vm", Profile: "warm", RunID: "failed-run", Status: "failed", Replay: main.NewTestReplayManifest("checkout", "warm", "", scenario), ReplayOf: "prior-run"}
	dir := t.TempDir()
	report := filepath.Join(dir, "failed.json")
	if err := main.WriteTestReport(report, []main.TestRunReceipt{original}); err != nil {
		t.Fatal(err)
	}
	rows, _, err := main.ReadTestBaselineReport(report, nil)
	if err != nil {
		t.Fatal(err)
	}
	selected, err := main.SelectTestReplay(rows, "failed-run")
	if err != nil || selected.ReplayOf != "prior-run" || selected.Replay.ConfigurationSHA256 != original.Replay.ConfigurationSHA256 {
		t.Fatalf("lost replay metadata: %v", err)
	}
	alias := filepath.Join(dir, "output.json")
	if err := os.Link(report, alias); err != nil {
		t.Fatal(err)
	}
	if main.CmdTestReplay([]string{"--from", report, "--run-id", "failed-run", "--report", alias}) == 0 {
		t.Fatal("input overwrite accepted")
	}
	after, _, err := main.ReadTestBaselineReport(report, nil)
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
	scenarios, _, err := main.ReadTestManifestForScenario(manifest, "replay-test", nil)
	if err != nil {
		t.Fatal(err)
	}
	receipt := main.TestRunReceipt{Scenario: "replay-test", Engine: "real-vm", Profile: "cold", Status: "failed", RunID: "offline-run", Replay: main.NewTestReplayManifest("replay-test", "cold", "", scenarios["replay-test"])}
	report := filepath.Join(dir, "failed.json")
	if err := main.WriteTestReport(report, []main.TestRunReceipt{receipt}); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FAAS_TOKEN", "")
	args := []string{"replay", "--from", report, "--run-id", "offline-run", "--manifest", manifest, "--validate"}
	if code := main.CmdTest(args); code != 0 {
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
	if code := main.CmdTest(args); code == 0 {
		t.Fatal("CLI accepted changed configuration")
	}
}
