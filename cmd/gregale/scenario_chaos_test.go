package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/chaos"
)

func TestScenarioChaosPlanValidationAndEngineLabel(t *testing.T) {
	scenario := testScenario{
		Project:  "export-api",
		Services: map[string]testService{"inventory": {Fixture: testDeliverySinkFixture}},
		Chaos: &testChaosSpec{Duration: "5m", Rules: []testChaosRule{{
			From: "export-api", To: "inventory", Kind: chaos.KindLatency, Percent: 20, Latency: "1500ms", Seed: 4,
		}}},
	}
	plan, err := scenario.Chaos.plan()
	if err != nil || plan.DurationMS != 300_000 || plan.Rules[0].LatencyMS != 1_500 {
		t.Fatalf("chaos plan = (%+v, %v)", plan, err)
	}
	if err := validateScenarioChaos(scenario); err != nil {
		t.Fatalf("valid workload scope rejected: %v", err)
	}
	if err := validateScenarioChaos(testScenario{
		Project: "export-api", Services: scenario.Services,
		Chaos: &testChaosSpec{Duration: "5m", Rules: []testChaosRule{{
			To: "production", Kind: chaos.KindHTTPStatus, Percent: 100, StatusCode: 503,
		}}},
	}); err == nil || !strings.Contains(err.Error(), "not a workload") {
		t.Fatalf("unknown workload validation = %v", err)
	}
	if _, err := collectTestValidationForEngine(map[string]testScenario{"export": scenario}, t.TempDir(), "export", "local"); err == nil || !strings.Contains(err.Error(), "require the real-vm engine") {
		t.Fatalf("local engine accepted proxy chaos: %v", err)
	}
}

func TestScenarioChaosManifestFieldsAndBounds(t *testing.T) {
	dir := t.TempDir()
	manifestPath := filepath.Join(dir, "gregale-test.yaml")
	contents := `version: 1
scenarios:
  customer-export:
    project: export-api
    source: .
    command: [true]
    services:
      inventory:
        fixture: delivery-sink
    chaos:
      duration: 5m
      rules:
        - from: export-api
          to: inventory
          kind: http_status
          status_code: 503
          percent: 10
          seed: 17
`
	if err := os.WriteFile(manifestPath, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	manifest, _, err := readTestManifestDocument(manifestPath, func(string, testScenario) []string { return nil })
	if err != nil {
		t.Fatalf("read valid chaos manifest: %v", err)
	}
	if got := manifest.Scenarios["customer-export"].Chaos.Rules[0].StatusCode; got != 503 {
		t.Fatalf("parsed chaos status = %d", got)
	}
	contents = strings.Replace(contents, "duration: 5m", "duration: 30m", 1)
	if err := os.WriteFile(manifestPath, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := readTestManifestDocument(manifestPath, func(string, testScenario) []string { return nil }); err == nil || !strings.Contains(err.Error(), "duration_ms") {
		t.Fatalf("overlong chaos lease validation = %v", err)
	}
}

func TestChaosInjectCommandRejectsAmbiguousFaults(t *testing.T) {
	if code := cmdChaos([]string{"inject", "--scenario", "customer-export", "--target", "inventory"}); code == 0 {
		t.Fatal("chaos injection accepted no fault")
	}
	if code := cmdChaos([]string{"inject", "--scenario", "customer-export", "--target", "inventory", "--latency", "1s", "--error", "503"}); code == 0 {
		t.Fatal("chaos injection accepted two fault kinds")
	}
}
