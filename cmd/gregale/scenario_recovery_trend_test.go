package main

import (
	"bytes"
	"context"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func recoveryTrendReceipt(t *testing.T, s testScenario, prefix string, recoveryMS, p95 float64, failures int) testRunReceipt {
	t.Helper()
	return runSweepTest(context.Background(), s, func(ctx context.Context, p testRunPlan) testRunReceipt {
		receipt := campaignCoverageReceipt(t, p, prefix+p.ReplayCase, false)
		cfg := outageCyclesConfig(t, p.Prepared.Scenario)
		for i, phase := range cfg.ChaosSchedule {
			if i == 0 || i%2 == 1 {
				continue
			}
			actual := &receipt.Steps[0].Load.ChaosPhases[i]
			actual.Failures = failures
			actual.ErrorRate = float64(failures) / float64(actual.Requests)
			actual.Latency.P95MS = p95
			fill := func(r *testLoadRecoveryEvidence, b *testLoadRecoveryConfig) {
				r.RecoveryMS = &recoveryMS
				r.WithinMS = b.Within.Milliseconds()
				r.SustainMS = b.Sustain.Milliseconds()
				r.WindowMS = b.Window.Milliseconds()
				r.WindowsChecked = 3
			}
			if phase.Recovery != nil {
				fill(actual.Recovery, phase.Recovery)
			}
			for name, budget := range phase.StepRecovery {
				fill(actual.StepRecovery[name], budget)
				actual.Steps = append(actual.Steps, testLoadStepEvidence{Name: name, testLoadMetrics: actual.testLoadMetrics})
			}
		}
		return receipt
	})
}

func TestRecoveryTrendOfflineChartsAndTolerances(t *testing.T) {
	t.Setenv("FAAS_TOKEN", "")
	dir := t.TempDir()
	for name, body := range map[string]string{"package.json": `{"scripts":{"start":"node server.js"}}`, "server.js": "console.log('fixture');"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
	}
	source := campaignCoverageScenario()
	source.Resilience.CampaignCoverage.MinExercised = map[int]int{0: 1, 1: 1}
	manifest := filepath.Join(dir, "gregale-test.yaml")
	body, err := yaml.Marshal(testManifest{Version: 1, Scenarios: map[string]testScenario{"checkout": source}})
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(manifest, body, 0600); err != nil {
		t.Fatal(err)
	}
	scenarios, _, err := readTestManifestForScenario(manifest, "checkout", nil)
	if err != nil {
		t.Fatal(err)
	}
	s := scenarios["checkout"]
	baseline := recoveryTrendReceipt(t, s, "before-", 100, 100, 0)
	current := recoveryTrendReceipt(t, s, "after-", 150, 160, 2)
	path := filepath.Join(dir, "runs.json")
	if err = writeTestReport(path, []testRunReceipt{baseline, current}); err != nil {
		t.Fatal(err)
	}
	fifty, half, tenth := float64(50), float64(50), float64(.1)
	cfg := testCampaignTrendConfig{Reports: []testCampaignTrendInput{{FromReport: "runs.json", RunID: baseline.RunID, Label: "v1"}, {FromReport: "runs.json", RunID: current.RunID, Label: "<script>v2</script>"}}, Recovery: &testRecoveryTrendConfig{MinSamples: 5, MaxRecoveryIncreasePercent: &fifty, MaxP95IncreasePercent: &half, MaxErrorRateIncrease: &tenth}}
	coverage, err := buildTestCampaignTrend(s, "checkout", dir, cfg, "")
	if err != nil {
		t.Fatal(err)
	}
	trend, err := buildTestRecoveryTrend(s, coverage, dir, cfg, "")
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range trend.Points[1].Values {
		if v.Status != "failed" || *v.RecoveryMS != 150 || *v.P95MS != 160 || *v.ErrorRate != .2 || !strings.Contains(v.Error, "p95 latency") || strings.Contains(v.Error, "recovery time") || !strings.Contains(v.Error, "error rate") || len(v.References) != v.Events {
			t.Fatal("wrong trend aggregation or boundaries", v)
		}
	}
	if len(trend.Charts) != 6 {
		t.Fatal("missing per-choice charts")
	}
	var html bytes.Buffer
	if err = recoveryTrendTemplate.Execute(&html, trend); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(html.String(), "<script>") || !strings.Contains(html.String(), "after-campaign-seed") || !strings.Contains(html.String(), "href=\"#point-2\"") {
		t.Fatal("missing escaped labels or provenance")
	}
	config := filepath.Join(dir, "trend.yaml")
	body, err = yaml.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(config, body, 0600); err != nil {
		t.Fatal(err)
	}
	output := filepath.Join(dir, "trend.html")
	args := []string{"recovery-trend", "--manifest", manifest, "--scenario", "checkout", "--config", config, "--html", output}
	if cmdTest(args) != 0 {
		t.Fatal("offline rendering failed")
	}
	alias := filepath.Join(dir, "alias.html")
	if err = os.Link(path, alias); err != nil {
		t.Fatal(err)
	}
	args[len(args)-1] = alias
	if cmdTest(args) == 0 {
		t.Fatal("input overwritten")
	}
	// Root summaries are never sufficient evidence.
	current.FaultMatrix.CampaignCoverage.Choices[0].Exercised++
	if err = writeTestReport(path, []testRunReceipt{baseline, current}); err != nil {
		t.Fatal(err)
	}
	args[len(args)-1] = output
	if cmdTest(args) == 0 {
		t.Fatal("corrupted raw history accepted")
	}
}

func TestRecoveryTrendMissingEvidenceAndEndpointIsolation(t *testing.T) {
	source := campaignCoverageScenario()
	// An endpoint must supply its own metrics and confirmation.
	name := source.Steps[1].Requests[0].Name
	source.Steps[1].Load.ChaosSchedule[2].StepRecovery = map[string]*testLoadRecoverySpec{name: source.Steps[1].Load.ChaosSchedule[2].Recovery}
	s, err := expandTestFaultSweep(source)
	if err != nil {
		t.Fatal(err)
	}
	root := recoveryTrendReceipt(t, s, "leaf-", 100, 100, 0)
	values, err := collectTestRecoveryTrendValues(s, &root, 5)
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range values {
		if v.RecoveryMS == nil || v.P95MS == nil || v.ErrorRate == nil {
			t.Fatal("valid confirmation missing", v)
		}
	}
	phase := &root.FaultMatrix.Cases[0].Resilience.History[0].Steps[0].Load.ChaosPhases[2]
	phase.StepRecovery[name].Passed = false
	phase.Steps = nil
	values, err = collectTestRecoveryTrendValues(s, &root, 5)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, v := range values {
		if v.Target == name && v.Status == "failed" {
			found = true
			if v.RecoveryMS != nil || v.P95MS != nil || v.ErrorRate != nil {
				t.Fatal("endpoint borrowed aggregate metrics", v)
			}
		}
	}
	if !found {
		t.Fatal("missing endpoint not reported")
	}
	phase.Recovery.RecoveryMS = nil
	phase.Latency.P95MS = math.NaN()
	values, err = collectTestRecoveryTrendValues(s, &root, 5)
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range values {
		if v.Target == "aggregate" && v.Status == "failed" && v.P95MS != nil {
			t.Fatal("nonfinite p95 retained")
		}
	}
}

func TestRecoveryTrendMissingExerciseAndSampleMinimum(t *testing.T) {
	s, err := expandTestFaultSweep(campaignCoverageScenario())
	if err != nil {
		t.Fatal(err)
	}
	root := recoveryTrendReceipt(t, s, "leaf-", 100, 100, 0)
	values, err := collectTestRecoveryTrendValues(s, &root, 11)
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range values {
		if v.P95MS != nil || v.ErrorRate != nil || v.RecoveryMS == nil || v.Status != "failed" {
			t.Fatal("insufficient samples silently accepted", v)
		}
	}
	root.FaultMatrix.CampaignCoverage.Choices[0].Exercised--
	values, err = collectTestRecoveryTrendValues(s, &root, 1)
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range values {
		if v.Choice == 0 && (v.RecoveryMS != nil || v.P95MS != nil || v.ErrorRate != nil || v.Status != "failed") {
			t.Fatal("partial exercise concealed", v)
		}
	}
	// Missing confirmation stays visible even when no tolerance is configured.
	phase := &root.FaultMatrix.Cases[0].Resilience.History[0].Steps[0].Load.ChaosPhases[2]
	phase.Recovery.Passed = false
	values, err = collectTestRecoveryTrendValues(s, &root, 1)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, v := range values {
		if strings.Contains(v.Error, "unconfirmed") {
			found = true
		}
	}
	if !found {
		t.Fatal("unconfirmed recovery hidden")
	}
}

func TestRecoveryTrendPolicyValidation(t *testing.T) {
	bad := math.NaN()
	negative := -1.
	high := 1001.
	rate := 1.1
	for _, p := range []testRecoveryTrendConfig{{}, {MinSamples: 5, MaxRecoveryIncreasePercent: &bad}, {MinSamples: 5, MaxP95IncreasePercent: &negative}, {MinSamples: 5, MaxRecoveryIncreasePercent: &high}, {MinSamples: 5, MaxErrorRateIncrease: &rate}} {
		if validateTestRecoveryTrendConfig(p) == nil {
			t.Fatal("invalid policy accepted")
		}
	}
	zero := 0.
	if err := validateTestRecoveryTrendConfig(testRecoveryTrendConfig{MinSamples: 1, MaxRecoveryIncreasePercent: &zero, MaxP95IncreasePercent: &zero, MaxErrorRateIncrease: &zero}); err != nil {
		t.Fatal(err)
	}
	baseline := testRecoveryTrendValue{RecoveryMS: &zero, P95MS: &zero, ErrorRate: &zero}
	one := 1.
	current := testRecoveryTrendValue{RecoveryMS: &one, P95MS: &one, ErrorRate: &zero, Status: "passed"}
	compareTestRecoveryTrendValue(&current, baseline, testRecoveryTrendConfig{MaxRecoveryIncreasePercent: &zero, MaxP95IncreasePercent: &zero, MaxErrorRateIncrease: &zero})
	if current.Status != "failed" || strings.Contains(current.Error, "error rate") {
		t.Fatal("zero baseline comparison wrong", current)
	}
}
