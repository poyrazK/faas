package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/chaos"
	"gopkg.in/yaml.v3"
)

func outageCyclesScenario() testScenario {
	s := minimizationTestScenario()
	s.Steps[1].Load.OutageCycles = &testOutageCyclesSpec{Count: 3}
	return s
}
func outageCyclesConfig(t *testing.T, s testScenario) *testLoadConfig {
	t.Helper()
	cfg, err := resolveTestLoadConfig(testScenario{Requests: s.Steps[1].Requests, Load: s.Steps[1].Load, Timeout: s.Timeout}, testLoadOverrides{})
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}
func outageCyclesReceipt(t *testing.T, cfg *testLoadConfig) *testLoadEvidence {
	t.Helper()
	now := time.Now()
	report := &testLoadEvidence{Status: "passed", StopReason: "duration"}
	for _, p := range cfg.ChaosSchedule {
		e := testLoadChaosPhaseEvidence{Name: p.Name, Cycle: p.Cycle, StartedAt: now, FinishedAt: now.Add(p.Duration), testLoadMetrics: testLoadMetrics{Requests: 10, Latency: testLoadLatency{Samples: 10, P95MS: 100}}}
		now = e.FinishedAt
		if len(p.Rules) > 0 {
			e.Chaos = &testChaosEvidence{RulesInstalled: len(p.Rules), Matches: make([]testChaosRuleEvidence, len(p.Rules))}
			for i := range e.Chaos.Matches {
				e.Chaos.Matches[i].Matches = 1
			}
		}
		if p.Recovery != nil {
			ms := float64(100)
			e.Recovery = &testLoadRecoveryEvidence{Passed: true, RecoveryMS: &ms}
		}
		if len(p.StepRecovery) > 0 {
			e.StepRecovery = map[string]*testLoadRecoveryEvidence{}
			for name := range p.StepRecovery {
				ms := float64(100)
				e.StepRecovery[name] = &testLoadRecoveryEvidence{Passed: true, RecoveryMS: &ms}
			}
		}
		report.ChaosPhases = append(report.ChaosPhases, e)
	}
	return report
}

func TestOutageCyclesExpansionLimitsAndIsolation(t *testing.T) {
	s := outageCyclesScenario()
	before, _ := json.Marshal(s)
	expanded, err := expandTestOutageCycles(s)
	if err != nil {
		t.Fatal(err)
	}
	load := expanded.Steps[1].Load
	if load.Duration != "7s" || len(load.ChaosSchedule) != 7 {
		t.Fatal("wrong repeated duration")
	}
	if err := validateTestScenarioSteps(s, nil); err != nil {
		t.Fatal(err)
	}
	again, err := expandTestOutageCycles(expanded)
	if err != nil || !reflect.DeepEqual(again, expanded) {
		t.Fatal("expansion not idempotent")
	}
	for i, p := range load.ChaosSchedule[1:] {
		if p.Cycle != i/2+1 {
			t.Fatal("missing cycle identity")
		}
		if i%2 == 0 && !reflect.DeepEqual(p.Rules, s.Steps[1].Load.ChaosSchedule[1].Rules) {
			t.Fatal("fault severity changed")
		}
		if i%2 == 1 && !reflect.DeepEqual(p.Recovery, s.Steps[1].Load.ChaosSchedule[2].Recovery) {
			t.Fatal("recovery budget changed")
		}
	}
	load.ChaosSchedule[3].Rules[0].Latency = "1ms"
	after, _ := json.Marshal(s)
	if string(before) != string(after) || load.ChaosSchedule[1].Rules[0].Latency != "100ms" {
		t.Fatal("cycle/template alias")
	}
	for _, kind := range []string{"one", "ten", "stages", "duration", "template", "no-fault", "no-recovery", "too-long", "timeout", "requests", "p95", "error-rate", "nan", "name", "top-level"} {
		t.Run(kind, func(t *testing.T) {
			bad := outageCyclesScenario()
			l := bad.Steps[1].Load
			v := float64(0)
			switch kind {
			case "one":
				l.OutageCycles.Count = 1
			case "ten":
				l.OutageCycles.Count = 10
			case "stages":
				l.Stages = []testLoadStageSpec{{}}
			case "duration":
				l.Duration = ""
			case "template":
				l.ChaosSchedule = l.ChaosSchedule[:2]
			case "no-fault":
				l.ChaosSchedule[1].Rules = nil
			case "no-recovery":
				l.ChaosSchedule[2].Recovery = nil
			case "too-long":
				l.ChaosSchedule[1].Duration = "2m"
				l.Duration = "122s"
			case "timeout":
				bad.Timeout = "5s"
			case "requests":
				l.Rate = 1000
				l.Iterations = 0
				l.Pacing = ""
				l.ChaosSchedule[1].Duration = "40s"
				l.Duration = "42s"
				bad.Timeout = "3m"
			case "p95":
				v = 1001
				l.OutageCycles.MaxP95IncreasePercent = &v
			case "error-rate":
				v = 1.1
				l.OutageCycles.MaxErrorRateIncrease = &v
			case "nan":
				v = math.NaN()
				l.OutageCycles.MaxP95IncreasePercent = &v
			case "name":
				l.ChaosSchedule[1].Name = strings.Repeat("a", 64)
			case "top-level":
				bad.Load = l
				bad.Steps = nil
			}
			if err := validateTestScenarioSteps(bad, nil); err == nil {
				t.Fatal("invalid outage cycles accepted")
			}
		})
	}
}

func TestOutageCyclesEvidenceFailuresAndTrendLimits(t *testing.T) {
	for _, kind := range []string{"exact", "p95", "errors", "missing-samples", "missing-latency", "recovery", "endpoint", "matches", "connection", "retry", "threshold", "interrupted"} {
		t.Run(kind, func(t *testing.T) {
			s := outageCyclesScenario()
			p95 := float64(20)
			rate := float64(.1)
			s.Steps[1].Load.OutageCycles.MaxP95IncreasePercent = &p95
			s.Steps[1].Load.OutageCycles.MaxErrorRateIncrease = &rate
			s.Steps[1].Load.ChaosSchedule[1].Rules[0].MinMatches = 1
			cfg := outageCyclesConfig(t, s)
			r := outageCyclesReceipt(t, cfg)
			r.ChaosPhases[3].Latency.P95MS = 120
			r.ChaosPhases[4].ErrorRate = .1
			var scheduleErr error
			switch kind {
			case "p95":
				r.ChaosPhases[3].Latency.P95MS = 121
			case "errors":
				r.ChaosPhases[4].ErrorRate = .11
			case "missing-samples":
				r.ChaosPhases[3].Requests = 0
			case "missing-latency":
				r.ChaosPhases[3].Latency.Samples = 0
			case "recovery":
				r.ChaosPhases[4].Recovery.Passed = false
			case "endpoint":
				cfg.ChaosSchedule[4].StepRecovery = map[string]*testLoadRecoveryConfig{"probe": {}}
				r.ChaosPhases[4].StepRecovery = map[string]*testLoadRecoveryEvidence{"probe": {Passed: false}}
			case "matches":
				r.ChaosPhases[3].Chaos.Matches[0].Matches = 0
			case "connection":
				cfg.ChaosSchedule[3].ConnectionAttempts = &testRetryAmplificationSpec{}
				r.ChaosPhases[3].ConnectionAttempts = &testRetryAmplificationEvidence{Passed: false}
			case "retry":
				cfg.ChaosSchedule[3].RetryAmplification = &testRetryAmplificationSpec{}
				r.ChaosPhases[3].RetryAmplification = &testRetryAmplificationEvidence{Passed: false}
			case "threshold":
				r.ChaosPhases[4].Thresholds = []testLoadThresholdEvidence{{Passed: false}}
			case "interrupted":
				r.ChaosPhases = r.ChaosPhases[:4]
				scheduleErr = context.Canceled
			}
			err := applyTestOutageCycles(r, cfg, scheduleErr)
			if kind == "exact" {
				if err != nil || r.OutageCycles.Status != "passed" || r.OutageCycles.Completed != 3 || len(r.OutageCycles.Cycles[1].Checks) != 4 {
					t.Fatal(r.OutageCycles, err)
				}
			} else if err == nil || r.OutageCycles.Status != "failed" || r.OutageCycles.FirstFailedCycle != 2 {
				t.Fatal(r.OutageCycles, err)
			}
			if kind == "interrupted" && (r.OutageCycles.Completed != 1 || r.OutageCycles.Cycles[2].Status != "incomplete") {
				t.Fatal("interruption masked")
			}
		})
	}
}

func TestOutageCyclesOfflineReportsReplay(t *testing.T) {
	dir := t.TempDir()
	s := outageCyclesScenario()
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
	normalized := scenarios["checkout"]
	t.Setenv("FAAS_TOKEN", "")
	if cmdTest([]string{"--manifest", manifest, "--scenario", "checkout", "--validate"}) != 0 {
		t.Fatal("offline cycle validation failed")
	}
	cfg := outageCyclesConfig(t, normalized)
	load := outageCyclesReceipt(t, cfg)
	load.ChaosPhases[4].Recovery.Passed = false
	if err := applyTestOutageCycles(load, cfg, nil); err == nil {
		t.Fatal("missing cycle failure")
	}
	row := testRunReceipt{Scenario: "checkout", Engine: "real-vm", Profile: "cold", RunID: "cycle-2-failed", Status: "failed", Replay: newTestReplayManifest("checkout", "cold", "", normalized), Steps: []testScenarioStepEvidence{{Name: "load", Load: load}}}
	selected, err := selectTestReplay([]testRunReceipt{row}, row.RunID)
	if err != nil {
		t.Fatal(err)
	}
	replay, err := prepareTestReplay(selected, map[string]testScenario{"checkout": normalized}, dir)
	if err != nil || len(replay.Scenario.Steps[1].Load.ChaosSchedule) != 7 {
		t.Fatal("cycle replay failed", err)
	}
	normalized.Steps[1].Load.OutageCycles.Count = 2
	if _, err := prepareTestReplay(selected, map[string]testScenario{"checkout": normalized}, dir); err == nil {
		t.Fatal("changed cycle count replay accepted")
	}
	normalized.Steps[1].Load.OutageCycles.Count = 3
	var terminal bytes.Buffer
	printTestLoadSummary(&terminal, load)
	if !strings.Contains(terminal.String(), "cycle 2 (fault-cycle-2 → recovery-cycle-2): failed") {
		t.Fatal("missing cycle terminal summary")
	}
	path := filepath.Join(dir, "cycles.html")
	if err := writeTestHTMLReport(path, "", []testRunReceipt{row}); err != nil {
		t.Fatal(err)
	}
	html, _ := os.ReadFile(path)
	if !strings.Contains(string(html), "Outage cycles: failed") || !strings.Contains(string(html), "First failed or incomplete cycle: 2") {
		t.Fatal("missing HTML cycle evidence")
	}
	path = filepath.Join(dir, "cycles.xml")
	if err := writeTestJUnit(path, []testRunReceipt{row}); err != nil {
		t.Fatal(err)
	}
	xml, _ := os.ReadFile(path)
	if !strings.Contains(string(xml), "first_failed_cycle") || !strings.Contains(string(xml), "recovery-cycle-2") {
		t.Fatal("missing JUnit cycle evidence")
	}
}

func TestOutageCyclesScheduledExecutionAndCancellation(t *testing.T) {
	for _, mode := range []string{"healthy", "recovery-failure", "cancel", "campaign-healthy", "campaign-recovery-failure", "campaign-cancel"} {
		t.Run(mode, func(t *testing.T) {
			kind := strings.TrimPrefix(mode, "campaign-")
			var mu sync.Mutex
			active := false
			generation := 0
			probesInSecondFault := 0
			ruleID := ""
			clears := 0
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				mu.Lock()
				defer mu.Unlock()
				w.Header().Set("Content-Type", "application/json")
				if r.URL.Path == "/probe" {
					// Cancel inside cycle 2's fault, a probe after its install has
					// answered. Cancelling on the first probe raced the install PUT
					// itself: the runner saw its own install cancelled at the cycle
					// boundary and cycle 1's recovery phase ended unrecorded. The
					// campaign variant cancels inside the install request instead.
					if mode == "cancel" && generation == 2 {
						probesInSecondFault++
						if probesInSecondFault == 2 {
							cancel()
						}
					}
					if active || (kind == "recovery-failure" && generation >= 2) {
						w.WriteHeader(http.StatusServiceUnavailable)
					} else {
						w.WriteHeader(http.StatusOK)
					}
					return
				}
				switch r.Method {
				case http.MethodPut:
					var request api.InjectScenarioTestChaosRequest
					if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
						t.Error(err)
						w.WriteHeader(400)
						return
					}
					generation++
					active = true
					rule := request.Rules[0]
					ruleID = chaos.RuleID(chaos.Rule{To: rule.To, Kind: rule.Kind, Percent: rule.Percent, StatusCode: rule.StatusCode, Seed: rule.Seed, Port: rule.Port, LatencyMS: rule.LatencyMS, Direction: rule.Direction, RateKiBPerSecond: rule.RateKiBPerSecond, ResetAfterMS: rule.ResetAfterMS})
					if mode == "campaign-cancel" && generation == 2 {
						// The server has accepted the plan, but cancellation wins
						// before the client receives its response. Cleanup must still
						// clear this uncertain installation.
						cancel()
					}
					_ = json.NewEncoder(w).Encode(api.InjectScenarioTestChaosResponse{ExpiresAt: time.Now().Add(time.Minute), RulesInstalled: 1, Generation: fmt.Sprint(generation)})
				case http.MethodDelete:
					active = false
					clears++
					w.WriteHeader(http.StatusNoContent)
				case http.MethodGet:
					_ = json.NewEncoder(w).Encode(api.ScenarioTestChaosMatchesResponse{Generation: fmt.Sprint(generation), Matches: []api.ScenarioTestChaosMatch{{RuleID: ruleID, Count: 1}}})
				default:
					w.WriteHeader(400)
				}
			}))
			defer server.Close()
			s := outageCyclesScenario()
			l := s.Steps[1].Load
			l.OutageCycles.Count = 3
			recoveryTolerance := float64(1000)
			l.OutageCycles.MaxRecoveryIncreasePercent = &recoveryTolerance
			l.Pacing = "50ms"
			one := float64(1)
			l.Thresholds.ErrorRate = &one
			l.ChaosSchedule[1].Rules = []testChaosRule{{To: "cache", Kind: "http_status", StatusCode: 503, Percent: 100, MinMatches: 1}}
			l.ChaosSchedule[2].Recovery = &testLoadRecoverySpec{Within: "500ms", Sustain: "200ms", Window: "200ms", Thresholds: testLoadStepThresholdSpec{ErrorRate: func() *float64 { v := float64(0); return &v }(), MinSamples: 1}}
			if strings.HasPrefix(mode, "campaign-") {
				seed := uint64(42)
				l.OutageCycles = nil
				rule := testChaosRule{To: "cache", Kind: "tcp_latency", Port: 6379, Percent: 100, Latency: "100ms", MinMatches: 1}
				reset := rule
				reset.Kind, reset.Latency = "tcp_reset", ""
				l.ChaosSchedule[1].Rules = []testChaosRule{rule}
				l.FaultCampaign = &testFaultCampaignSpec{Seed: &seed, Count: 3, MaxDuration: "10s", Choices: []testFaultCampaignChoice{{Duration: "1s", Rule: rule}, {Duration: "1s", Rule: reset}}}
			}
			steps := []testHTTPRequest{{Name: "load-probe", Method: "GET", Path: "/probe", Expect: testHTTPExpect{Status: 200}}}
			cfg, err := resolveTestLoadConfig(testScenario{Requests: steps, Load: l}, testLoadOverrides{})
			if err != nil {
				t.Fatal(err)
			}
			report, err := runTestScenarioLoadOnce(ctx, NewClient(server.URL, "test-token"), server.URL, "0123456789abcdef0123456789abcdef", nil, steps, nil, cfg)
			if kind == "healthy" {
				if err != nil || report == nil || report.OutageCycles == nil || report.OutageCycles.Status != "passed" || report.OutageCycles.Completed != 3 {
					t.Fatal("healthy cycles failed", err)
				}
			} else if err == nil || report == nil || report.OutageCycles == nil || report.OutageCycles.FirstFailedCycle != 2 {
				t.Fatalf("cycle failure: %+v, %v", report, err)
			}
			if strings.HasPrefix(mode, "campaign-") && (report.OutageCycles.Campaign == nil || report.OutageCycles.Campaign.Seed != 42 || len(report.OutageCycles.Campaign.Schedule) != 7) {
				t.Fatal("scheduled campaign evidence missing")
			}
			mu.Lock()
			defer mu.Unlock()
			if active || clears < 2 {
				t.Fatal("fault plan not cleared")
			}
			if kind == "healthy" {
				if generation != 3 || report.OutageCycles.FirstFailedCycle != 0 {
					t.Fatal("healthy cycles incomplete")
				}
			} else if kind == "recovery-failure" {
				if generation != 3 || report.OutageCycles.Completed != 3 || report.ChaosPhases[2].Recovery == nil || !report.ChaosPhases[2].Recovery.Passed || report.ChaosPhases[4].Recovery.Passed || report.ChaosPhases[6].Recovery.Passed {
					t.Fatal("per-cycle recovery or continued execution missing", report.OutageCycles)
				}
			} else if generation != 2 || report.OutageCycles.Completed >= 3 || report.OutageCycles.Cycles[2].Status != "incomplete" {
				t.Fatal("cancelled cycles were treated as completed")
			}
		})
	}
}

func TestOutageCyclesLaterCycleMinimizationKeepsEarlierCycles(t *testing.T) {
	source := outageCyclesScenario()
	source.Steps[1].Load.ChaosSchedule[1].Duration = "4s"
	source.Steps[1].Load.Duration = "6s"
	expanded, err := expandTestOutageCycles(source)
	if err != nil {
		t.Fatal(err)
	}
	target := testMinimizationTarget{Step: "load", Phase: "recovery-cycle-2"}
	makeReceipt := func(s testScenario, id string) testRunReceipt {
		cfg := outageCyclesConfig(t, s)
		load := outageCyclesReceipt(t, cfg)
		for i, p := range load.ChaosPhases {
			load.Requests += p.Requests
			var offset int64
			for j := 0; j < i; j++ {
				offset += cfg.ChaosSchedule[j].Duration.Milliseconds()
			}
			load.ChaosPhases[i].PlannedStartMS = offset
			load.ChaosPhases[i].PlannedEndMS = offset + cfg.ChaosSchedule[i].Duration.Milliseconds()
			if p.Name == target.Phase {
				load.ChaosPhases[i].Recovery = &testLoadRecoveryEvidence{Passed: false, WithinMS: 500, SustainMS: 200, WindowMS: 200, WindowsChecked: 5, Error: "no sustained healthy interval within the recovery deadline"}
			}
		}
		load.Status = "failed"
		_ = applyTestOutageCycles(load, cfg, nil)
		return testRunReceipt{Scenario: "checkout", Engine: "real-vm", Profile: "cold", RunID: id, Status: "failed", Replay: newTestReplayManifest("checkout", "cold", "", s), Steps: []testScenarioStepEvidence{{Name: "load", Load: load}}}
	}
	original := makeReceipt(expanded, "cycle-min-original")
	options := durationMinOptions()
	options.Target = target
	result := runTestFaultMinimization(context.Background(), testPreparedScenario{Name: "checkout", Engine: "real-vm", Scenario: expanded}, &original, options, func(ctx context.Context, plan testRunPlan) testRunReceipt {
		return makeReceipt(plan.Prepared.Scenario, fmt.Sprintf("cycle-min-%d", plan.Attempt))
	})
	if result.Status != "passed" || result.Minimization.Duration.BestMS != 1000 || result.Minimization.Duration.Phase != "fault-cycle-2" {
		t.Fatalf("later cycle minimization: %+v", result.Minimization)
	}
	selected, err := selectTestReplay([]testRunReceipt{result}, result.Minimization.BestRunID)
	if err != nil {
		t.Fatal(err)
	}
	replay, err := prepareTestReplay(selected, map[string]testScenario{"checkout": source}, ".")
	if err != nil {
		t.Fatal(err)
	}
	l := replay.Scenario.Steps[1].Load
	if l.Duration != "13s" || l.ChaosSchedule[1].Duration != "4s" || l.ChaosSchedule[3].Duration != "1s" || l.ChaosSchedule[5].Duration != "4s" || l.OutageCycles.Count != 3 {
		t.Fatal("minimization changed the earlier or later cycles")
	}
}

func TestOutageCyclesOmittedPreservesReplayFingerprint(t *testing.T) {
	s := minimizationTestScenario()
	before, _ := json.Marshal(s)
	expanded, err := expandTestOutageCycles(s)
	if err != nil {
		t.Fatal(err)
	}
	after, _ := json.Marshal(expanded)
	if string(before) != string(after) {
		t.Fatal("omitted outage_cycles changed configuration")
	}
	s.Steps = []testScenarioStep{}
	before, _ = json.Marshal(s)
	expanded, err = expandTestOutageCycles(s)
	if err != nil {
		t.Fatal(err)
	}
	after, _ = json.Marshal(expanded)
	if string(before) != string(after) {
		t.Fatal("omitted outage_cycles changed empty steps")
	}
}
