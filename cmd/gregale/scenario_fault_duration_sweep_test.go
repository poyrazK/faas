package main_test

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/onebox-faas/faas/cmd/gregale"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestFaultDurationSweepVariants(t *testing.T) {
	for _, originalDuration := range []string{"1s", "10s"} {
		t.Run(originalDuration, func(t *testing.T) {
			s := main.SweepTestScenario("duration")
			d, _ := time.ParseDuration(originalDuration)
			s.Steps[1].Load.ChaosSchedule[1].Duration = originalDuration
			s.Steps[1].Load.Duration = (d + 2*time.Second).String()
			before, _ := json.Marshal(s)
			expanded, err := main.ExpandTestFaultSweep(s)
			if err != nil {
				t.Fatal(err)
			}
			for _, c := range expanded.Resilience.FaultMatrix {
				variant, err := main.ApplyTestFaultMatrixCase(expanded, c)
				if err != nil {
					t.Fatal(err)
				}
				load := variant.Steps[1].Load
				duration, _ := time.ParseDuration(c.Sweep.Value)
				if load.Duration != (duration+2*time.Second).String() || load.ChaosSchedule[1].Duration != c.Sweep.Value || c.Sweep.Rule.Index != -1 {
					t.Fatal("incorrect phase/load duration")
				}
				if !reflect.DeepEqual(load.ChaosSchedule[1].Rules, s.Steps[1].Load.ChaosSchedule[1].Rules) || !reflect.DeepEqual(load.ChaosSchedule[0], s.Steps[1].Load.ChaosSchedule[0]) || !reflect.DeepEqual(load.ChaosSchedule[2], s.Steps[1].Load.ChaosSchedule[2]) {
					t.Fatal("fixed rules or recovery changed")
				}
				if err := main.ValidateTestScenarioSteps(variant, nil); err != nil {
					t.Fatal(err)
				}
			}
			after, _ := json.Marshal(s)
			if string(before) != string(after) {
				t.Fatal("template mutated")
			}
		})
	}
}

func TestFaultDurationSweepRejectsInvalidRuns(t *testing.T) {
	for _, name := range []string{"index", "duplicate", "short", "long", "fraction", "load-limit", "timeout", "original", "clear", "stages", "requests"} {
		t.Run(name, func(t *testing.T) {
			s := main.SweepTestScenario("duration")
			spec := s.Resilience.FaultSweep
			switch name {
			case "requests":
				s.Timeout = "2m"
				s.Steps[1].Load.Rate = 1000
				s.Steps[1].Load.Iterations = 0
				s.Steps[1].Load.Pacing = ""
				spec.Values[2] = "100s"
			case "index":
				i := 0
				spec.RuleIndex = &i
			case "duplicate":
				spec.Values = []string{"1s", "1000ms"}
			case "short":
				spec.Values[0] = "999ms"
			case "long":
				spec.Values[0] = "301s"
			case "fraction":
				spec.Values[0] = "1.0001s"
			case "load-limit":
				spec.Values[0] = "5m"
			case "timeout":
				s.Timeout = "10s"
			case "original":
				s.Steps[1].Load.ChaosSchedule[1].Duration = "0s"
				s.Steps[1].Load.Duration = "2s"
			case "clear":
				spec.Phase = "recovery"
			case "stages":
				s.Steps[1].Load.Stages = append(s.Steps[1].Load.Stages, main.TestLoadStageSpec{})
			}
			if _, err := main.ExpandTestFaultSweep(s); err == nil {
				t.Fatal("invalid duration sweep accepted")
			}
		})
	}
}

func TestFaultDurationSweepExecutionReplayReports(t *testing.T) {
	s := main.SweepTestScenario("duration")
	calls := 0
	result := main.RunSweepTest(context.Background(), s, func(ctx context.Context, plan main.TestRunPlan) main.TestRunReceipt {
		calls++
		row := main.DeadlineTestReceipt(plan.Prepared.Scenario, fmt.Sprintf("duration-%d", calls), main.TestMinimizationTarget{Step: "load", Phase: "recovery"}, plan.ReplaySweep.Value == "5s")
		row.Replay = main.NewTestReplayManifest("checkout", "cold", plan.ReplayCase, plan.Prepared.Scenario)
		row.Replay.Sweep = plan.ReplaySweep
		row.Replay.Version = main.TestFaultReplayVersion(row.Replay)
		return row
	})
	if calls != 6 || result.FaultMatrix.Passed != 2 || result.FaultMatrix.Failed != 1 {
		t.Fatal("wrong repeated outcomes")
	}
	for _, group := range result.FaultMatrix.Cases {
		for _, leaf := range group.Resilience.History {
			selected, err := main.SelectTestReplay([]main.TestRunReceipt{result}, leaf.RunID)
			if err != nil {
				t.Fatal(err)
			}
			replay, err := main.PrepareTestReplay(selected, map[string]main.TestScenario{"checkout": s}, ".")
			if err != nil {
				t.Fatal(err)
			}
			duration, _ := time.ParseDuration(group.Sweep.Value)
			load := replay.Scenario.Steps[1].Load
			if load.Duration != (duration+2*time.Second).String() || load.ChaosSchedule[1].Duration != group.Sweep.Value || selected.Replay.Version != 8 {
				t.Fatal("replay lost duration")
			}
			changed := main.SweepTestScenario("duration")
			changed.Resilience.FaultSweep.Values[1] = "6s"
			if group.Sweep.Value == "5s" {
				if _, err := main.PrepareTestReplay(selected, map[string]main.TestScenario{"checkout": changed}, "."); err == nil {
					t.Fatal("changed duration replay accepted")
				}
			}
		}
	}
	path := filepath.Join(t.TempDir(), "duration.html")
	if err := main.WriteTestHTMLReport(path, "", []main.TestRunReceipt{result}); err != nil {
		t.Fatal(err)
	}
	body, _ := os.ReadFile(path)
	if !strings.Contains(string(body), "duration = 15s") || !strings.Contains(string(body), "phase load/fault") {
		t.Fatal("duration report missing phase setting")
	}
}

func TestFaultDurationSweepRegression(t *testing.T) {
	plan := sweepRegPlan()
	plan.Prepared.Scenario.Resilience.FaultSweep.Mode = "duration"
	plan.Prepared.Scenario.Resilience.FaultSweep.RuleIndex = nil
	plan.Prepared.Scenario.Resilience.FaultSweep.Values = []string{"1s", "5s", "15s"}
	baseline := sweepRegReceipt(plan, []int{4, 4, 4}, 100, "before-duration")
	plan = sweepRegAttach(t, plan, baseline)
	current := sweepRegReceipt(plan, []int{4, 4, 4}, 100, "now-duration")
	main.ApplyTestSweepBaseline(plan, &current)
	if current.SweepRegression.Status != "passed" {
		t.Fatal(current.SweepRegression)
	}
	lost := sweepRegReceipt(plan, []int{4, 2, 4}, 100, "lost-duration")
	main.ApplyTestSweepBaseline(plan, &lost)
	if lost.SweepRegression.Status != "failed" {
		t.Fatal("lost passing outage accepted")
	}
	altered := sweepRegClone(t, baseline)
	altered.FaultMatrix.Cases[1].RecoveryWorkloadSHA256 = strings.Repeat("0", 64)
	path := filepath.Join(t.TempDir(), "altered.json")
	if err := main.WriteTestReport(path, []main.TestRunReceipt{altered}); err != nil {
		t.Fatal(err)
	}
	if err := main.PrepareTestSweepBaselines(path, []main.TestRunPlan{plan}); err == nil {
		t.Fatal("different duration workload accepted")
	}
}
