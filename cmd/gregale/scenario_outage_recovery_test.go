package main

import (
	"bytes"
	"context"
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"
)

func outageRecoveryFixture(t *testing.T, endpointOnly bool) (testScenario, *testLoadConfig, *testLoadEvidence) {
	t.Helper()
	s := outageCyclesScenario()
	limit := float64(25)
	s.Steps[1].Load.OutageCycles.MaxRecoveryIncreasePercent = &limit
	recovery := &s.Steps[1].Load.ChaosSchedule[2]
	recovery.StepRecovery = map[string]*testLoadRecoverySpec{"load-probe": recovery.Recovery}
	if endpointOnly {
		recovery.Recovery = nil
	}
	cfg := outageCyclesConfig(t, s)
	report := outageCyclesReceipt(t, cfg)
	for i, p := range cfg.ChaosSchedule {
		stamp := func(r *testLoadRecoveryEvidence, budget *testLoadRecoveryConfig) {
			r.WithinMS = budget.Within.Milliseconds()
			r.SustainMS = budget.Sustain.Milliseconds()
			r.WindowMS = budget.Window.Milliseconds()
			r.WindowsChecked = 5
		}
		if p.Recovery != nil {
			stamp(report.ChaosPhases[i].Recovery, p.Recovery)
		}
		for name, budget := range p.StepRecovery {
			stamp(report.ChaosPhases[i].StepRecovery[name], budget)
		}
	}
	return s, cfg, report
}

func TestOutageRecoveryDegradationThresholds(t *testing.T) {
	for _, endpointOnly := range []bool{false, true} {
		for _, kind := range []string{"equal", "improved", "regressed", "later", "aggregate", "zero-tolerance", "no-option"} {
			t.Run(kind+map[bool]string{false: "-combined", true: "-endpoint"}[endpointOnly], func(t *testing.T) {
				_, cfg, report := outageRecoveryFixture(t, endpointOnly)
				value := float64(125)
				expected := 0
				switch kind {
				case "improved":
					value = 50
				case "regressed":
					value = 125.1
					expected = 2
				case "later":
					v := float64(140)
					report.ChaosPhases[6].StepRecovery["load-probe"].RecoveryMS = &v
					expected = 3
				case "aggregate":
					if !endpointOnly {
						v := float64(126)
						report.ChaosPhases[4].Recovery.RecoveryMS = &v
						expected = 2
					}
				case "zero-tolerance":
					zero := float64(0)
					cfg.OutageCycles.MaxRecoveryIncreasePercent = &zero
					value = 100.1
					expected = 2
				case "no-option":
					cfg.OutageCycles.MaxRecoveryIncreasePercent = nil
					value = 400
				}
				report.ChaosPhases[4].StepRecovery["load-probe"].RecoveryMS = &value
				err := applyTestOutageCycles(report, cfg, nil)
				evidence := report.OutageCycles
				if expected == 0 {
					if err != nil || evidence.FirstRecoveryFailedCycle != 0 || evidence.Status != "passed" {
						t.Fatal(evidence, err)
					}
				} else if err == nil || evidence.FirstRecoveryFailedCycle != expected || evidence.FirstFailedCycle != expected || report.Status != "failed" {
					t.Fatal(evidence, err)
				}
				count := 2
				if endpointOnly {
					count = 1
				}
				if kind == "no-option" {
					count = 0
				}
				for _, cycle := range evidence.Cycles {
					if len(cycle.RecoveryChecks) != count {
						t.Fatal("wrong assertion comparison count")
					}
				}
				if count > 0 {
					check := evidence.Cycles[1].RecoveryChecks[count-1]
					if check.Request != "load-probe" || check.BaselineMS == nil || *check.BaselineMS != 100 || check.CurrentMS == nil || *check.CurrentMS != value {
						t.Fatal("endpoint borrowed aggregate evidence", check)
					}
				}
			})
		}
	}
}

func TestOutageRecoveryDegradationRejectsUnconfirmedEvidence(t *testing.T) {
	for _, kind := range []string{"missing-baseline", "baseline-failed", "current-missing", "current-failed", "nil-time", "zero", "negative", "nan", "infinite", "outside-budget", "no-windows", "wrong-budget", "stability", "interrupted", "different-baselines"} {
		t.Run(kind, func(t *testing.T) {
			_, cfg, r := outageRecoveryFixture(t, false)
			expected := 2
			selected := r.ChaosPhases[4].StepRecovery["load-probe"]
			invalid := float64(0)
			switch kind {
			case "missing-baseline":
				delete(r.ChaosPhases[2].StepRecovery, "load-probe")
				expected = 1
			case "baseline-failed":
				r.ChaosPhases[2].StepRecovery["load-probe"].Passed = false
				expected = 1
			case "current-missing":
				delete(r.ChaosPhases[4].StepRecovery, "load-probe")
			case "current-failed":
				selected.Passed = false
			case "nil-time":
				selected.RecoveryMS = nil
			case "zero":
				selected.RecoveryMS = &invalid
			case "negative":
				invalid = -1
				selected.RecoveryMS = &invalid
			case "nan":
				invalid = math.NaN()
				selected.RecoveryMS = &invalid
			case "infinite":
				invalid = math.Inf(1)
				selected.RecoveryMS = &invalid
			case "outside-budget":
				invalid = 501
				selected.RecoveryMS = &invalid
			case "no-windows":
				selected.WindowsChecked = 0
			case "wrong-budget":
				selected.WithinMS++
			case "stability":
				cfg.ChaosSchedule[4].StepRecovery["load-probe"].Stability = 100 * time.Millisecond
			case "interrupted":
				r.ChaosPhases = r.ChaosPhases[:4]
			case "different-baselines":
				v := float64(200)
				r.ChaosPhases[2].Recovery.RecoveryMS = &v
				now := float64(126)
				selected.RecoveryMS = &now
			}
			var scheduleErr error
			if kind == "interrupted" {
				scheduleErr = context.Canceled
			}
			if err := applyTestOutageCycles(r, cfg, scheduleErr); err == nil || r.OutageCycles.FirstRecoveryFailedCycle != expected {
				t.Fatal(r.OutageCycles, err)
			}
			check := r.OutageCycles.Cycles[expected-1].RecoveryChecks[1]
			if check.Passed || check.Error == "" {
				t.Fatal("unconfirmed recovery accepted", check)
			}
			if _, err := json.Marshal(r.OutageCycles); err != nil {
				t.Fatal("invalid time leaked into comparison JSON", err)
			}
		})
	}
}

func TestOutageRecoveryDegradationValidationReplayAndReports(t *testing.T) {
	for _, value := range []float64{-1, 1001, math.NaN(), math.Inf(1)} {
		s := outageCyclesScenario()
		s.Steps[1].Load.OutageCycles.MaxRecoveryIncreasePercent = &value
		if err := validateTestScenarioSteps(s, nil); err == nil {
			t.Fatal("invalid tolerance accepted")
		}
	}
	for _, value := range []float64{0, 1000} {
		s := outageCyclesScenario()
		s.Steps[1].Load.OutageCycles.MaxRecoveryIncreasePercent = &value
		if err := validateTestScenarioSteps(s, nil); err != nil {
			t.Fatal("valid tolerance rejected", err)
		}
	}
	source, cfg, load := outageRecoveryFixture(t, false)
	v := float64(126)
	load.ChaosPhases[4].StepRecovery["load-probe"].RecoveryMS = &v
	if err := applyTestOutageCycles(load, cfg, nil); err == nil {
		t.Fatal("regression accepted")
	}
	row := testRunReceipt{Scenario: "checkout", Engine: "real-vm", Profile: "cold", RunID: "recovery-regression", Status: "failed", Replay: newTestReplayManifest("checkout", "cold", "", source), Steps: []testScenarioStepEvidence{{Name: "load", Load: load}}}
	replay, err := prepareTestReplay(&row, map[string]testScenario{"checkout": source}, ".")
	if err != nil {
		t.Fatal(err)
	}
	if *replay.Scenario.Steps[1].Load.OutageCycles.MaxRecoveryIncreasePercent != 25 {
		t.Fatal("replay lost tolerance")
	}
	changed := outageCyclesScenario()
	changed.Steps[1].Load.ChaosSchedule[2].StepRecovery = source.Steps[1].Load.ChaosSchedule[2].StepRecovery
	other := float64(26)
	changed.Steps[1].Load.OutageCycles.MaxRecoveryIncreasePercent = &other
	if _, err := prepareTestReplay(&row, map[string]testScenario{"checkout": changed}, "."); err == nil {
		t.Fatal("changed tolerance replay accepted")
	}
	dir := t.TempDir()
	html := filepath.Join(dir, "recovery.html")
	if err := writeTestHTMLReport(html, "", []testRunReceipt{row}); err != nil {
		t.Fatal(err)
	}
	body, _ := os.ReadFile(html)
	if !strings.Contains(string(body), "First failed recovery comparison: 2") || !strings.Contains(string(body), "load-probe recovery: 126ms") || !strings.Contains(string(body), "limit 125ms") {
		t.Fatal("missing recovery comparison HTML")
	}
	var out bytes.Buffer
	printTestLoadSummary(&out, load)
	if !strings.Contains(out.String(), "load-probe recovery: 126.000ms; cycle 1 100.000ms, limit 125.000ms") {
		t.Fatal("missing terminal recovery comparison")
	}
	path := filepath.Join(dir, "recovery.xml")
	if err := writeTestJUnit(path, []testRunReceipt{row}); err != nil {
		t.Fatal(err)
	}
	body, _ = os.ReadFile(path)
	if !strings.Contains(string(body), "recovery_checks") || !strings.Contains(string(body), "first_recovery_failed_cycle") {
		t.Fatal("missing JUnit comparison evidence")
	}
	old := outageCyclesScenario()
	before, _ := json.Marshal(old)
	expanded, err := expandTestOutageCycles(old)
	if err != nil {
		t.Fatal(err)
	}
	after, _ := json.Marshal(old)
	if !reflect.DeepEqual(before, after) || strings.Contains(string(before), "max_recovery_increase_percent") {
		t.Fatal("omitted tolerance changed source")
	}
	if expanded.Steps[1].Load.OutageCycles.MaxRecoveryIncreasePercent != nil {
		t.Fatal("implicit tolerance introduced")
	}
}

func TestOutageRecoveryDegradationStabilityAndFailureOrdering(t *testing.T) {
	_, cfg, r := outageRecoveryFixture(t, false)
	for i := 2; i < len(cfg.ChaosSchedule); i += 2 {
		for name, budget := range map[string]*testLoadRecoveryConfig{"": cfg.ChaosSchedule[i].Recovery, "load-probe": cfg.ChaosSchedule[i].StepRecovery["load-probe"]} {
			budget.Stability = 100 * time.Millisecond
			observed := testPhaseRecoveryEvidence(r.ChaosPhases[i])[name]
			start := *observed.RecoveryMS + float64(observed.SustainMS)
			observed.Stability = &testLoadRecoveryStabilityEvidence{Passed: true, DurationMS: 100, ObservedMS: 100, WindowsChecked: 1, StartedMS: &start}
		}
	}
	if err := applyTestOutageCycles(r, cfg, nil); err != nil {
		t.Fatal("confirmed stable recovery rejected", err)
	}
	r.ChaosPhases[4].StepRecovery["load-probe"].Stability.StartedMS = nil
	if err := applyTestOutageCycles(r, cfg, nil); err == nil || r.OutageCycles.FirstRecoveryFailedCycle != 2 {
		t.Fatal("incomplete stability accepted", err)
	}
	_, cfg, r = outageRecoveryFixture(t, false)
	r.ChaosPhases[3].Thresholds = []testLoadThresholdEvidence{{Passed: false}}
	v := float64(130)
	r.ChaosPhases[6].StepRecovery["load-probe"].RecoveryMS = &v
	if err := applyTestOutageCycles(r, cfg, nil); err == nil || r.OutageCycles.FirstFailedCycle != 2 || r.OutageCycles.FirstRecoveryFailedCycle != 3 {
		t.Fatal("unrelated SLO hid recovery regression", err)
	}
}

func TestOutageRecoveryDegradationOfflineCLI(t *testing.T) {
	source, _, _ := outageRecoveryFixture(t, false)
	dir := t.TempDir()
	for name, body := range map[string]string{"package.json": `{"scripts":{"start":"node server.js"}}`, "server.js": "console.log('fixture');"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
	}
	body, err := yaml.Marshal(testManifest{Version: 1, Scenarios: map[string]testScenario{"checkout": source}})
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
	if *normalized.Steps[1].Load.OutageCycles.MaxRecoveryIncreasePercent != 25 {
		t.Fatal("YAML tolerance lost")
	}
	row := testRunReceipt{Scenario: "checkout", Engine: "real-vm", Profile: "cold", RunID: "cycle-recovery-cli", Status: "failed", Replay: newTestReplayManifest("checkout", "cold", "", normalized)}
	report := filepath.Join(dir, "failed.json")
	if err := writeTestReport(report, []testRunReceipt{row}); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FAAS_TOKEN", "")
	if cmdTest([]string{"--manifest", manifest, "--scenario", "checkout", "--validate"}) != 0 {
		t.Fatal("offline recovery tolerance validation failed")
	}
	if cmdTestReplay([]string{"--manifest", manifest, "--from", report, "--run-id", row.RunID, "--validate"}) != 0 {
		t.Fatal("offline recovery tolerance replay failed")
	}
}
