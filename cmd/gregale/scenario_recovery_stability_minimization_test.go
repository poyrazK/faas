package main_test

import (
	"context"
	"fmt"
	"github.com/onebox-faas/faas/cmd/gregale"
	"gopkg.in/yaml.v3"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func stabilityMinTestScenario() main.TestScenario {
	s := main.MinimizationTestScenario()
	s.Steps[1].Load.ChaosSchedule[2].Duration = "2s"
	s.Steps[1].Load.Duration = "4s"
	s.Steps[1].Load.ChaosSchedule[2].Recovery.Stability = "300ms"
	return s
}
func stabilityMinTestOptions() main.TestFaultMinimizationOptions {
	o := main.MinimizationTestOptions()
	o.Mode = "stability"
	o.StabilityResolution = time.Millisecond
	return o
}
func stabilityMinTestReceipt(s main.TestScenario, id string, target main.TestMinimizationTarget, failed bool) main.TestRunReceipt {
	r := main.DeadlineTestReceipt(s, id, target, failed)
	phase := &r.Steps[0].Load.ChaosPhases[2]
	phase.FinishedAt = phase.StartedAt.Add(2 * time.Second)
	recovery := main.TestPhaseRecoveryEvidence(*phase)[target.Request]
	spec, _, _ := main.TestRecoveryDeadlineSpec(s, target)
	cfg := main.ResolveTestLoadRecovery(spec)
	recovered, started := float64(200), float64(400)
	recovery.RecoveryMS = &recovered
	recovery.Stability = &main.TestLoadRecoveryStabilityEvidence{DurationMS: cfg.Stability.Milliseconds(), StartedMS: &started, ObservedMS: cfg.Stability.Milliseconds(), WindowsChecked: 3, Passed: !failed}
	if failed {
		observed := cfg.Stability.Milliseconds()
		if observed > 200 {
			observed = 200
		}
		recovery.Stability.ObservedMS = observed
		relapse := started + float64(observed)
		recovery.Stability.RelapseMS = &relapse
		recovery.Error = "recovery stability relapsed into an unhealthy window"
	}
	return r
}
func TestStabilityMinimizationThresholdReplay(t *testing.T) {
	for _, mode := range []string{"aggregate", "endpoint", "combined", "floor", "coarse", "unchanged", "matrix"} {
		t.Run(mode, func(t *testing.T) {
			base := stabilityMinTestScenario()
			scenario := base
			o := stabilityMinTestOptions()
			if mode == "endpoint" {
				spec := *base.Steps[1].Load.ChaosSchedule[2].Recovery
				base.Steps[1].Load.ChaosSchedule[2].StepRecovery = map[string]*main.TestLoadRecoverySpec{"load-probe": &spec}
				o.Target.Request = "load-probe"
			}
			scenario = base
			original := stabilityMinTestReceipt(scenario, "original", o.Target, true)
			if mode == "combined" {
				edits := main.TestFaultReplayOverrides{Latency: []main.TestFaultLatencyOverride{{Rule: main.TestFaultRuleRef{"load", "fault", 2}, Latency: "50ms"}}, Percent: []main.TestFaultPercentOverride{{Rule: main.TestFaultRuleRef{"load", "fault", 1}, Percent: 38}}, Recovery: []main.TestRecoveryDeadlineOverride{{Target: o.Target, Within: "600ms"}}}
				removed := []main.TestFaultRuleRef{{"load", "fault", 0}}
				var err error
				scenario, err = main.ApplyTestFaultReplayOverrides(base, removed, edits)
				if err != nil {
					t.Fatal(err)
				}
				original = stabilityMinTestReceipt(scenario, "original", o.Target, true)
				original.Replay = main.MinimizedTestReplay(main.NewTestReplayManifest("checkout", "cold", "", base), scenario, removed, edits)
			}
			if mode == "coarse" {
				o.StabilityResolution = 25 * time.Millisecond
			}
			if mode == "floor" {
				original = stabilityMinTestReceipt(scenario, "original", o.Target, true)
			}
			if mode == "matrix" {
				rules := base.Steps[1].Load.ChaosSchedule[1].Rules
				base.Resilience = &main.TestResilienceSpec{Runs: 2, MinPassRate: 1, FaultMatrix: []main.TestFaultMatrixCaseSpec{{Name: "composite", Step: "load", Phase: "fault", Rules: rules}, {Name: "single", Step: "load", Phase: "fault", Rules: rules[:1]}}}
				var err error
				scenario, err = main.ApplyTestFaultMatrixCase(base, base.Resilience.FaultMatrix[0])
				if err != nil {
					t.Fatal(err)
				}
				scenario.Resilience = nil
				original = stabilityMinTestReceipt(scenario, "original", o.Target, true)
				original.Replay = main.NewTestReplayManifest("checkout", "cold", "composite", scenario)
			}
			prepared, err := main.PrepareTestReplay(&original, map[string]main.TestScenario{"checkout": base}, ".")
			if err != nil {
				t.Fatal(err)
			}
			fingerprint := main.NewTestReplayManifest("checkout", "cold", "", base).ConfigurationSHA256
			calls := 0
			result := main.RunTestFaultMinimization(context.Background(), prepared, &original, o, func(ctx context.Context, plan main.TestRunPlan) main.TestRunReceipt {
				calls++
				spec, _, _ := main.TestRecoveryDeadlineSpec(plan.Prepared.Scenario, o.Target)
				duration, _ := time.ParseDuration(spec.Stability)
				threshold := 200 * time.Millisecond
				if mode == "floor" {
					threshold = 100 * time.Millisecond
				}
				if mode == "unchanged" {
					threshold = 300 * time.Millisecond
				}
				return stabilityMinTestReceipt(plan.Prepared.Scenario, fmt.Sprintf("trial-%d", calls), o.Target, duration >= threshold)
			})
			e := result.Minimization
			expected := int64(200)
			if mode == "floor" {
				expected = 100
			}
			if mode == "coarse" {
				expected = 200
			}
			if mode == "unchanged" {
				expected = 300
			}
			if result.Status != "passed" || e.Stability == nil || e.Stability.BestMS != expected || e.Replay.Version != 7 {
				t.Fatalf("result: status=%s reason=%s detail=%+v", e.Status, e.Reason, e.Stability)
			}
			if mode == "floor" && e.Stability.PassedBelowMS != nil {
				t.Fatal("passing bound invented")
			}
			if mode != "floor" && (e.Stability.PassedBelowMS == nil || e.Stability.BestMS-*e.Stability.PassedBelowMS > o.StabilityResolution.Milliseconds()) {
				t.Fatal("boundary not resolved")
			}
			best, err := main.SelectTestReplay([]main.TestRunReceipt{result}, e.BestRunID)
			if err != nil {
				t.Fatal(err)
			}
			replay, err := main.PrepareTestReplay(best, map[string]main.TestScenario{"checkout": base}, ".")
			if err != nil {
				t.Fatal(err)
			}
			spec, _, _ := main.TestRecoveryDeadlineSpec(replay.Scenario, o.Target)
			duration, _ := time.ParseDuration(spec.Stability)
			if duration.Milliseconds() != expected {
				t.Fatal("wrong replay interval")
			}
			for _, corrupt := range []string{"version", "digest", "duration"} {
				altered := *best
				descriptor := *best.Replay
				descriptor.RecoveryOverrides = append([]main.TestRecoveryDeadlineOverride(nil), best.Replay.RecoveryOverrides...)
				altered.Replay = &descriptor
				switch corrupt {
				case "version":
					descriptor.Version = 6
				case "digest":
					descriptor.ReducedConfigurationSHA256 = strings.Repeat("0", 64)
				case "duration":
					descriptor.RecoveryOverrides[len(descriptor.RecoveryOverrides)-1].Stability = "601ms"
				}
				if _, err := main.PrepareTestReplay(&altered, map[string]main.TestScenario{"checkout": base}, "."); err == nil {
					t.Fatal("tampered replay accepted", corrupt)
				}
			}
			if mode == "combined" && (len(best.Replay.RemovedRules) != 1 || best.Replay.RecoveryOverrides[0].Within != "600ms" || len(best.Replay.LatencyOverrides) != 1 || len(best.Replay.PercentOverrides) != 1) {
				t.Fatal("inherited edits lost")
			}
			if main.NewTestReplayManifest("checkout", "cold", "", base).ConfigurationSHA256 != fingerprint {
				t.Fatal("source mutated")
			}
			for _, trial := range e.Trials {
				for _, row := range trial.Runs {
					if row.Status == "passed" {
						if _, err := main.SelectTestReplay([]main.TestRunReceipt{result}, row.RunID); err != nil {
							t.Fatal("passing trial not replayable", err)
						}
					}
				}
			}
		})
	}
}

func TestStabilityMinimizationInconclusive(t *testing.T) {
	for _, mode := range []string{"budget", "partial", "flaky", "cleanup", "cancel", "missing", "duration", "initial-deadline", "unrelated", "no-matches"} {
		t.Run(mode, func(t *testing.T) {
			scenario := stabilityMinTestScenario()
			o := stabilityMinTestOptions()
			original := stabilityMinTestReceipt(scenario, "original", o.Target, true)
			if mode == "budget" {
				o.MaxRuns = 2
			}
			if mode == "partial" {
				o.MaxRuns = 6
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			calls := 0
			result := main.RunTestFaultMinimization(ctx, main.TestPreparedScenario{Name: "checkout", Engine: "real-vm", Scenario: scenario}, &original, o, func(ctx context.Context, plan main.TestRunPlan) main.TestRunReceipt {
				calls++
				spec, _, _ := main.TestRecoveryDeadlineSpec(plan.Prepared.Scenario, o.Target)
				duration, _ := time.ParseDuration(spec.Stability)
				row := stabilityMinTestReceipt(plan.Prepared.Scenario, fmt.Sprintf("run-%d", calls), o.Target, duration >= 200*time.Millisecond)
				if calls >= 3 {
					r := row.Steps[0].Load.ChaosPhases[2].Recovery
					switch mode {
					case "flaky":
						if calls == 4 {
							row = stabilityMinTestReceipt(plan.Prepared.Scenario, "mixed", o.Target, true)
						}
					case "cleanup":
						row.CleanupError = "cleanup failed"
					case "cancel":
						cancel()
					case "missing":
						r.Stability = nil
					case "duration":
						r.Stability.DurationMS++
					case "initial-deadline":
						r.Passed = false
						r.Error = "no sustained healthy interval within the recovery deadline"
						row.Status = "failed"
					case "unrelated":
						row.Status = "failed"
					case "no-matches":
						for i := range row.Steps[0].Load.ChaosPhases[1].Chaos.Matches {
							row.Steps[0].Load.ChaosPhases[1].Chaos.Matches[i].Matches = 0
						}
					}
				}
				return row
			})
			if result.Status != "failed" || result.Minimization.Status != "inconclusive" || result.Minimization.BestRunID == "" || result.Minimization.Replay == nil {
				t.Fatalf("lost confirmed original or accepted invalid trial: %+v", result.Minimization)
			}
			if mode == "cleanup" && (calls != 3 || result.CleanupError == "") {
				t.Fatal("cleanup did not stop")
			}
		})
	}
}

func TestStabilityMinimizationValidationReportsCLI(t *testing.T) {
	scenario := stabilityMinTestScenario()
	o := stabilityMinTestOptions()
	original := stabilityMinTestReceipt(scenario, "original", o.Target, true)
	prepared := main.TestPreparedScenario{Name: "checkout", Engine: "real-vm", Scenario: scenario}
	for _, bad := range []time.Duration{0, 500 * time.Microsecond, 6 * time.Minute} {
		changed := o
		changed.StabilityResolution = bad
		if _, err := main.ValidateTestMinimization(prepared, &original, changed); err == nil {
			t.Fatal("invalid resolution accepted")
		}
	}
	changed := original
	changed.Steps = append([]main.TestScenarioStepEvidence(nil), original.Steps...)
	changed = main.DeadlineTestReceipt(scenario, "deadline", o.Target, true)
	if _, err := main.ValidateTestMinimization(prepared, &changed, o); err == nil {
		t.Fatal("deadline failure accepted")
	}
	for _, duration := range []string{"0ms", "99ms", "1.1ms", "6m", "invalid", "1400ms"} {
		if _, err := main.ApplyTestRecoveryDeadlineOverrides(scenario, []main.TestRecoveryDeadlineOverride{{Target: o.Target, Stability: duration}}); err == nil {
			t.Fatalf("invalid override accepted %s", duration)
		}
	}
	if _, err := main.ApplyTestRecoveryDeadlineOverrides(scenario, []main.TestRecoveryDeadlineOverride{{Target: o.Target}}); err == nil {
		t.Fatal("empty override accepted")
	}
	for _, args := range [][]string{{"--mode", "stability", "--rule-index", "0", "--validate"}, {"--mode", "stability", "--stability-resolution", "0ms", "--validate"}, {"--mode", "recovery", "--stability-resolution", "25ms", "--validate"}} {
		if main.CmdTestMinimize(args) == 0 {
			t.Fatal("invalid flags accepted")
		}
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "package.json"), []byte(`{"scripts":{"start":"node server.js"}}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "server.js"), []byte("console.log('fixture');"), 0600); err != nil {
		t.Fatal(err)
	}
	body, err := yaml.Marshal(main.TestManifest{Version: 1, Scenarios: map[string]main.TestScenario{"checkout": scenario}})
	if err != nil {
		t.Fatal(err)
	}
	manifest := filepath.Join(dir, "gregale-test.yaml")
	if err := os.WriteFile(manifest, body, 0600); err != nil {
		t.Fatal(err)
	}
	scenarios, _, err := main.ReadTestManifestForScenario(manifest, "checkout", nil)
	if err != nil {
		t.Fatal(err)
	}
	scenario = scenarios["checkout"]
	original = stabilityMinTestReceipt(scenario, "original", o.Target, true)
	from := filepath.Join(dir, "failed.json")
	if err := main.WriteTestReport(from, []main.TestRunReceipt{original}); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FAAS_TOKEN", "")
	if main.CmdTestMinimize([]string{"--from", from, "--run-id", "original", "--manifest", manifest, "--assertion", "load/recovery", "--mode", "stability", "--max-workload-minutes", "40", "--validate"}) != 0 {
		t.Fatal("offline validation failed")
	}
	calls := 0
	result := main.RunTestFaultMinimization(context.Background(), main.TestPreparedScenario{Name: "checkout", Engine: "real-vm", Scenario: scenario}, &original, o, func(ctx context.Context, plan main.TestRunPlan) main.TestRunReceipt {
		calls++
		spec, _, _ := main.TestRecoveryDeadlineSpec(plan.Prepared.Scenario, o.Target)
		d, _ := time.ParseDuration(spec.Stability)
		return stabilityMinTestReceipt(plan.Prepared.Scenario, fmt.Sprintf("report-%d", calls), o.Target, d >= 200*time.Millisecond)
	})
	out := filepath.Join(dir, "minimized.json")
	if err := main.WriteTestReport(out, []main.TestRunReceipt{result}); err != nil {
		t.Fatal(err)
	}
	if main.CmdTestReplay([]string{"--from", out, "--run-id", result.Minimization.BestRunID, "--manifest", manifest, "--validate"}) != 0 {
		t.Fatal("offline replay failed")
	}
	html := filepath.Join(dir, "report.html")
	if err := main.WriteTestHTMLReport(html, "", []main.TestRunReceipt{result}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(html)
	if err != nil || !strings.Contains(string(data), "Stability observation:") {
		t.Fatal("HTML missing", err)
	}
	xml := filepath.Join(dir, "report.xml")
	if err := main.WriteTestJUnit(xml, []main.TestRunReceipt{result}); err != nil {
		t.Fatal(err)
	}
	data, err = os.ReadFile(xml)
	if err != nil || !strings.Contains(string(data), "best_ms") {
		t.Fatal("JUnit missing", err)
	}
}

func TestStabilityMinimizationReplayCapacity(t *testing.T) {
	scenario := stabilityMinTestScenario()
	o := stabilityMinTestOptions()
	manifest := main.NewTestReplayManifest("checkout", "cold", "", scenario)
	for i := 0; i < 20; i++ {
		manifest.RecoveryOverrides = append(manifest.RecoveryOverrides, main.TestRecoveryDeadlineOverride{Target: main.TestMinimizationTarget{Step: "other", Phase: "recovery", Request: fmt.Sprintf("probe-%d", i)}, Stability: "100ms"})
	}
	if _, err := main.TestStabilitySelection(scenario, manifest, o); err == nil {
		t.Fatal("override capacity ignored")
	}
	manifest.RecoveryOverrides[0].Target = o.Target
	if _, err := main.TestStabilitySelection(scenario, manifest, o); err != nil {
		t.Fatal("existing target cannot be replaced", err)
	}
	scenario.Steps[1].Load.ChaosSchedule[2].Recovery.Stability = ""
	if _, err := main.TestStabilitySelection(scenario, manifest, o); err == nil {
		t.Fatal("absent stability accepted")
	}
}

func TestStabilityMinimizationFractionalCadence(t *testing.T) {
	scenario := stabilityMinTestScenario()
	o := stabilityMinTestOptions()
	receipt := stabilityMinTestReceipt(scenario, "fractional", o.Target, true)
	r := receipt.Steps[0].Load.ChaosPhases[2].Recovery
	started, relapse := 400.1, 600.9
	r.Stability.StartedMS = &started
	r.Stability.RelapseMS = &relapse
	r.Stability.ObservedMS = 200
	if outcome := main.TestStabilityMinimizationOutcome(receipt, o, receipt.Replay); outcome != "reproduced" {
		t.Fatalf("fractional check cadence rejected: %s", outcome)
	}
	relapse = 601.2
	if main.TestStabilityMinimizationOutcome(receipt, o, receipt.Replay) != "inconclusive" {
		t.Fatal("inconsistent elapsed evidence accepted")
	}
}
