package main_test

import (
	"context"
	"github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/chaos"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestHTTPRetryExampleManifest(t *testing.T) {
	manifest, dir, err := main.ReadTestManifestDocument("../../examples/scenario-http-retry/gregale-test.yaml", func(string, main.TestScenario) []string { return nil })
	if err != nil {
		t.Fatal(err)
	}
	if _, err := main.CollectTestValidation(manifest.Scenarios, dir, "retry-after"); err != nil {
		t.Fatal(err)
	}
	plan, err := manifest.Scenarios["retry-after"].Steps[1].Chaos.Plan()
	if err != nil || plan.Rules[0].RetryAfterSeconds != 1 || plan.Rules[0].StatusCode != 429 {
		t.Fatalf("retry fault contract lost: %+v %v", plan, err)
	}
}
func TestHTTPRetryExampleAcceptance(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("Node.js required for HTTP retry example")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, node, "--test", "../../examples/scenario-http-retry/test/retry.test.mjs")
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("HTTP retry acceptance: %v\n%s", err, output)
	}
}

func TestHTTPRetryReplayAndReport(t *testing.T) {
	manifest, dir, err := main.ReadTestManifestDocument("../../examples/scenario-http-retry/gregale-test.yaml", func(string, main.TestScenario) []string { return nil })
	if err != nil {
		t.Fatal(err)
	}
	scenario := manifest.Scenarios["retry-after"]
	replay := main.NewTestReplayManifest("retry-after", "warm", "", scenario)
	receipt := main.TestRunReceipt{Scenario: "retry-after", Engine: "real-vm", Profile: "warm", RunID: "retry-replay", Status: "failed", Replay: replay}
	prepared, err := main.PrepareTestReplay(&receipt, manifest.Scenarios, dir)
	if err != nil {
		t.Fatal(err)
	}
	if prepared.Scenario.Steps[1].Chaos.Rules[0].RetryAfter != "1s" {
		t.Fatal("replay lost Retry-After")
	}
	scenario.Steps[1].Chaos.Rules[0].RetryAfter = "2s"
	if _, err := main.PrepareTestReplay(&receipt, map[string]main.TestScenario{"retry-after": scenario}, dir); err == nil {
		t.Fatal("replay accepted changed delay")
	}
	rule := chaos.Rule{To: "inventory", Kind: "http_status", Percent: 100, StatusCode: 429, RetryAfterSeconds: 1}
	receipt.Chaos = &main.TestChaosEvidence{Rules: []chaos.Rule{rule}, Matches: []main.TestChaosRuleEvidence{{Rule: rule, Matches: 3, MinMatches: 3}}}
	path := filepath.Join(t.TempDir(), "report.html")
	if err := main.WriteTestHTMLReport(path, "", []main.TestRunReceipt{receipt}); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(path)
	if err != nil || !strings.Contains(string(body), "HTTP 429; Retry-After 1s") {
		t.Fatalf("HTML lost retry fault: %v", err)
	}
	path = filepath.Join(t.TempDir(), "report.xml")
	if err := main.WriteTestJUnit(path, []main.TestRunReceipt{receipt}); err != nil {
		t.Fatal(err)
	}
	body, err = os.ReadFile(path)
	if err != nil || !strings.Contains(string(body), "retry_after_seconds") {
		t.Fatalf("JUnit lost retry fault: %v", err)
	}
}

func TestHTTPBodyExampleManifest(t *testing.T) {
	manifest, dir, err := main.ReadTestManifestDocument("../../examples/scenario-http-retry/gregale-body-test.yaml", func(string, main.TestScenario) []string { return nil })
	if err != nil {
		t.Fatal(err)
	}
	if _, err := main.CollectTestValidation(manifest.Scenarios, dir, ""); err != nil {
		t.Fatal(err)
	}
	for name, kind := range map[string]string{"malformed-response": chaos.KindHTTPMalformedJSON, "truncated-response": chaos.KindHTTPTruncatedBody} {
		plan, err := manifest.Scenarios[name].Steps[1].Chaos.Plan()
		if err != nil || plan.Rules[0].Kind != kind {
			t.Fatalf("body fault lost: %+v %v", plan, err)
		}
	}
}

func TestHTTPWriteExampleManifest(t *testing.T) {
	manifest, dir, err := main.ReadTestManifestDocument("../../examples/scenario-http-write/gregale-test.yaml", func(string, main.TestScenario) []string { return nil })
	if err != nil {
		t.Fatal(err)
	}
	if _, err := main.CollectTestValidation(manifest.Scenarios, dir, "committed-response-lost"); err != nil {
		t.Fatal(err)
	}
}
func TestHTTPWriteExampleAcceptance(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("Node.js required for write example")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, node, "--test", "../../examples/scenario-http-write/test/write.test.mjs", "../../examples/scenario-http-write/test/in-flight.test.mjs", "../../examples/scenario-http-write/test/pg-commit-proxy.test.mjs")
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("write response-loss acceptance: %v\n%s", err, output)
	}
}
