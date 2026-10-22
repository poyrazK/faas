package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func declaredCampaignScenario() testScenario {
	s := faultCampaignScenario()
	c := s.Steps[1].Load.FaultCampaign
	c.Order, c.Count = "declared", 2
	return s
}

func TestDeclaredCampaignOrderReplayAndValidation(t *testing.T) {
	s := declaredCampaignScenario()
	x, err := expandTestOutageCycles(s)
	if err != nil {
		t.Fatal(err)
	}
	for i, choice := range s.Steps[1].Load.FaultCampaign.Choices {
		if got := x.Steps[1].Load.ChaosSchedule[2*i+1]; got.Rules[0].Kind != choice.Rule.Kind || got.Duration != choice.Duration {
			t.Fatalf("choice %d: %+v", i, got)
		}
	}
	cfg := outageCyclesConfig(t, x)
	if testFaultCampaignReport(cfg).Algorithm != testDeclaredFaultCampaignAlgorithm {
		t.Fatal("order missing from evidence")
	}
	row := testRunReceipt{Scenario: "checkout", Engine: "real-vm", Status: "failed", RunID: "declared-campaign", Profile: "cold", Replay: newTestReplayManifest("checkout", "cold", "", x)}
	replay, err := prepareTestReplay(&row, map[string]testScenario{"checkout": s}, ".")
	if err != nil || !reflect.DeepEqual(replay.Scenario.Steps[1].Load.ChaosSchedule, x.Steps[1].Load.ChaosSchedule) {
		t.Fatalf("replay: %v", err)
	}
	changed := declaredCampaignScenario()
	changed.Steps[1].Load.FaultCampaign.Order = ""
	if _, err := prepareTestReplay(&row, map[string]testScenario{"checkout": changed}, "."); err == nil {
		t.Fatal("order change escaped fingerprint")
	}
	for _, change := range []func(*testFaultCampaignSpec){func(c *testFaultCampaignSpec) { c.Order = "random" }, func(c *testFaultCampaignSpec) { c.Count = 3 }} {
		invalid := declaredCampaignScenario()
		change(invalid.Steps[1].Load.FaultCampaign)
		if _, err := expandTestOutageCycles(invalid); err == nil {
			t.Fatal("invalid ordered campaign accepted")
		}
	}
	// A minimized expanded replay retains its recorded phases instead of redrawing.
	x.Steps[1].Load.ChaosSchedule[1].Rules = nil
	again, err := expandTestOutageCycles(x)
	if err != nil || !reflect.DeepEqual(again, x) {
		t.Fatalf("minimized replay changed: %v", err)
	}
}

func TestDeclaredTCPCampaignExampleBudgetsAndFailureReports(t *testing.T) {
	prepared, err := prepareTestSuite("../../examples/scenario-checkout/gregale-tcp-campaign-test.yaml", "tcp-campaign", testSuiteOptions{Engine: "real-vm", Validate: true, Repeat: 1, MaxWorkloadMinutes: 40})
	if err != nil || len(prepared) != 1 {
		t.Fatalf("example: %v", err)
	}
	p := prepared[0]
	if _, err := collectTestValidationForEngine(map[string]testScenario{p.Name: p.Scenario}, p.ManifestDir, p.Name, p.Engine); err != nil {
		t.Fatal(err)
	}
	cfg := outageCyclesConfig(t, p.Scenario)
	if cfg.Duration.String() != "26s" || len(cfg.ChaosSchedule) != 9 {
		t.Fatal("wrong campaign duration or phases")
	}
	kinds := []string{"tcp_latency", "tcp_bandwidth", "tcp_timeout", "tcp_reset"}
	for i, kind := range kinds {
		fault, recovery := cfg.ChaosSchedule[2*i+1], cfg.ChaosSchedule[2*i+2]
		if fault.Rules[0].Kind != kind || fault.ConnectionAttempts == nil || len(fault.StepThresholds) != 1 || len(recovery.StepRecovery) != 1 {
			t.Fatalf("missing budgets for %s", kind)
		}
	}
	for _, tc := range []struct {
		name    string
		cycle   int
		missing bool
	}{{"bounded", 0, false}, {"retry storm", 3, false}, {"missing counters", 2, true}} {
		t.Run(tc.name, func(t *testing.T) {
			report := outageCyclesReceipt(t, cfg)
			for i := 1; i < len(cfg.ChaosSchedule); i += 2 {
				report.ChaosPhases[i+1].ConnectionLeaks = &testConnectionLeakEvidence{Passed: true}
				phase := &report.ChaosPhases[i]
				phase.Steps = []testLoadStepEvidence{{Name: "dependency-probe", testLoadMetrics: testLoadMetrics{Requests: 10}}}
				count := int64(10)
				if (i+1)/2 == tc.cycle {
					count = 30
				}
				phase.Chaos.Matches[0].TCPAttempts = &count
				if tc.missing && (i+1)/2 == tc.cycle {
					phase.Chaos.Matches[0].TCPAttempts = nil
				}
			}
			err := applyTestConnectionAttempts(report, cfg, nil)
			if (err == nil) != (tc.cycle == 0) {
				t.Fatalf("attempt verdict: %v", err)
			}
			cycleErr := applyTestOutageCycles(report, cfg, nil)
			if (cycleErr == nil) != (tc.cycle == 0) || report.OutageCycles.FirstFailedCycle != tc.cycle {
				t.Fatalf("cycle verdict: %+v %v", report.OutageCycles, cycleErr)
			}
			for i, row := range report.OutageCycles.Cycles {
				if row.FaultKind != kinds[i] {
					t.Fatalf("fault identity lost: %+v", row)
				}
			}
			receipt := testRunReceipt{Scenario: "tcp-campaign", Status: report.Status, Steps: []testScenarioStepEvidence{{Name: "fault-campaign", Load: report}}}
			var terminal bytes.Buffer
			printTestLoadSummary(&terminal, report)
			html := filepath.Join(t.TempDir(), "report.html")
			if err := writeTestHTMLReport(html, "", []testRunReceipt{receipt}); err != nil {
				t.Fatal(err)
			}
			body, _ := os.ReadFile(html)
			if !strings.Contains(string(body), testDeclaredFaultCampaignAlgorithm) || !strings.Contains(terminal.String(), testDeclaredFaultCampaignAlgorithm) {
				t.Fatal("order omitted from reports")
			}
			encoded, _ := json.Marshal(receipt)
			if !bytes.Contains(encoded, []byte(`"fault_kind":"tcp_timeout"`)) {
				t.Fatal("fault identity omitted from JSON")
			}
			if tc.cycle > 0 && !strings.Contains(report.OutageCycles.Cycles[tc.cycle-1].Error, "connection attempts") {
				t.Fatal("failure cause omitted")
			}
		})
	}
}

func TestDeclaredCampaignCoverageUsesEveryChoiceForEverySeed(t *testing.T) {
	s := campaignCoverageScenario()
	c := s.Steps[1].Load.FaultCampaign
	c.Order, c.Count = "declared", 2
	expanded, err := expandTestFaultSweep(s)
	if err != nil {
		t.Fatal(err)
	}
	for _, setting := range expanded.Resilience.FaultMatrix {
		variant, err := applyTestFaultMatrixCase(expanded, setting)
		if err != nil {
			t.Fatal(err)
		}
		for i, choice := range c.Choices {
			if variant.Steps[1].Load.ChaosSchedule[2*i+1].Rules[0].Kind != choice.Rule.Kind {
				t.Fatal("coverage redrew declared order")
			}
		}
	}
}
