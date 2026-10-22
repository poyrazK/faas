package main_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/chaos"
)

func TestScenarioChaosPlanValidationAndEngineLabel(t *testing.T) {
	scenario := main.TestScenario{
		Project:  "export-api",
		Services: map[string]main.TestService{"inventory": {Fixture: main.TestDeliverySinkFixture}},
		Chaos: &main.TestChaosSpec{Duration: "5m", Rules: []main.TestChaosRule{{
			From: "export-api", To: "inventory", Kind: chaos.KindLatency, Percent: 20, Latency: "1500ms", Seed: 4,
		}}},
	}
	plan, err := scenario.Chaos.Plan()
	if err != nil || plan.DurationMS != 300_000 || plan.Rules[0].LatencyMS != 1_500 {
		t.Fatalf("chaos plan = (%+v, %v)", plan, err)
	}
	if err := main.ValidateScenarioChaos(scenario); err != nil {
		t.Fatalf("valid workload scope rejected: %v", err)
	}
	if err := main.ValidateScenarioChaos(main.TestScenario{
		Project: "export-api", Services: scenario.Services,
		Chaos: &main.TestChaosSpec{Duration: "5m", Rules: []main.TestChaosRule{{
			To: "production", Kind: chaos.KindHTTPStatus, Percent: 100, StatusCode: 503,
		}}},
	}); err == nil || !strings.Contains(err.Error(), "not a workload") {
		t.Fatalf("unknown workload validation = %v", err)
	}
	if _, err := main.CollectTestValidationForEngine(map[string]main.TestScenario{"export": scenario}, t.TempDir(), "export", "local"); err == nil || !strings.Contains(err.Error(), "require the real-vm engine") {
		t.Fatalf("local engine accepted proxy chaos: %v", err)
	}
}

func TestScenarioDNSChaosDoesNotRequireTCPListener(t *testing.T) {
	scenario := main.TestScenario{
		Project:  "checkout",
		Services: map[string]main.TestService{"cache": {Fixture: main.TestDeliverySinkFixture}},
		Chaos: &main.TestChaosSpec{Duration: "30s", Rules: []main.TestChaosRule{{
			From: "checkout", To: "cache", Kind: chaos.KindDNSNXDomain, Percent: 100, MinMatches: 1,
		}}},
	}
	if err := main.ValidateScenarioChaos(scenario); err != nil {
		t.Fatalf("private DNS rule rejected: %v", err)
	}
	plan, err := scenario.Chaos.Plan()
	if err != nil || len(plan.Rules) != 1 || plan.Rules[0].Kind != chaos.KindDNSNXDomain {
		t.Fatalf("DNS plan = (%+v, %v)", plan, err)
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
	manifest, _, err := main.ReadTestManifestDocument(manifestPath, func(string, main.TestScenario) []string { return nil })
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
	if _, _, err := main.ReadTestManifestDocument(manifestPath, func(string, main.TestScenario) []string { return nil }); err == nil || !strings.Contains(err.Error(), "duration_ms") {
		t.Fatalf("overlong chaos lease validation = %v", err)
	}
}

func TestChaosInjectCommandRejectsAmbiguousFaults(t *testing.T) {
	if code := main.CmdChaos([]string{"inject", "--scenario", "customer-export", "--target", "inventory"}); code == 0 {
		t.Fatal("chaos injection accepted no fault")
	}
	if code := main.CmdChaos([]string{"inject", "--scenario", "customer-export", "--target", "inventory", "--latency", "1s", "--error", "503"}); code == 0 {
		t.Fatal("chaos injection accepted two fault kinds")
	}
}

func TestChaosRetryAfterPlan(t *testing.T) {
	for _, tc := range []struct {
		delay  string
		status int
		valid  bool
	}{
		{"1s", 429, true}, {"30s", 503, true}, {"", 429, true},
		{"0s", 429, false}, {"-1s", 429, false}, {"1500ms", 429, false}, {"31s", 429, false}, {"invalid", 429, false}, {"1s", 500, false},
	} {
		spec := &main.TestChaosSpec{Duration: "10s", Rules: []main.TestChaosRule{{To: "inventory", Kind: "http_status", Percent: 100, StatusCode: tc.status, RetryAfter: tc.delay}}}
		plan, err := spec.Plan()
		if (err == nil) != tc.valid {
			t.Fatalf("%+v: %v", tc, err)
		}
		if tc.valid && tc.delay == "1s" && plan.Rules[0].RetryAfterSeconds != 1 {
			t.Fatal("retry header lost during conversion")
		}
	}
}

func TestChaosHTTPBodyFlagsRejectInvalidCombinations(t *testing.T) {
	for _, flags := range [][]string{
		{"--malformed-json", "--truncated-body"},
		{"--response-lost", "--malformed-json"},
		{"--response-lost", "--retry-after", "1s"},
		{"--malformed-json", "--error", "503"},
		{"--truncated-body", "--retry-after", "1s"},
		{"--malformed-json", "--tcp-port", "6379"},
	} {
		args := append([]string{"inject", "--scenario", "body", "--target", "inventory"}, flags...)
		if main.CmdChaos(args) == 0 {
			t.Fatalf("accepted invalid body flags: %v", flags)
		}
	}
}
