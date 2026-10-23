package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/actioncompare"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/rehearsalpg"
	"github.com/onebox-faas/faas/pkg/rehearsalresume"
)

type testActionSpec struct {
	Simulation *api.ScenarioSimulationPlan `yaml:"simulation,omitempty" json:"simulation,omitempty"`
	Network    string                      `yaml:"network,omitempty" json:"network,omitempty"`
	Webhooks   string                      `yaml:"webhooks" json:"webhooks"`
	// QueueDelivery same_run delivers captured queue sends addressed to a
	// member of the run as real invocations (ADR-20261023111825).
	QueueDelivery string `yaml:"queue_delivery,omitempty" json:"queue_delivery,omitempty"`
}

type testActionEvidence struct {
	PostgresFaultSHA256 string                  `json:"postgres_fault_sha256,omitempty"`
	SuspendResumeSHA256 string                  `json:"suspend_resume_sha256,omitempty"`
	ColdStartSHA256     string                  `json:"cold_start_sha256,omitempty"`
	ManagedWorkSHA256   string                  `json:"managed_work_sha256,omitempty"`
	SimulationSHA256    string                  `json:"simulation_sha256,omitempty"`
	Network             string                  `json:"network,omitempty"`
	Webhooks            string                  `json:"webhooks"`
	PolicyVersion       int                     `json:"policy_version"`
	Status              string                  `json:"status"`
	Evidence            *actioncompare.Evidence `json:"evidence,omitempty"`
}

type testActionSnapshotClient interface {
	SealScenarioTestActions(context.Context, string) (api.ScenarioActionSnapshot, error)
}

// The closure is idempotent: normal completion calls it before fixture cleanup;
// a deferred fallback collects failures that happened before setup was ready.
func newTestActionCollector(ctx context.Context, client testActionSnapshotClient, receipt *testRunReceipt, workloads []string) func() {
	done := false
	return func() {
		if done || receipt.Actions == nil {
			return
		}
		done = true
		evidence := &actioncompare.Evidence{Version: actioncompare.Version, Provenance: actioncompare.Provenance, Status: "incomplete"}
		receipt.Actions.Evidence = evidence
		if receipt.Behavior != nil {
			evidence.ContractSHA256, evidence.FixtureSHA256 = receipt.Behavior.ContractSHA256, receipt.Behavior.FixtureSHA256
		}
		captureWithin := 10 * time.Second
		if receipt.SuspendResume != nil && receipt.SuspendResume.Spec.ManagedJob() {
			captureWithin = 170 * time.Second
		}
		if receipt.Actions.PostgresFaultSHA256 != "" {
			captureWithin = 170 * time.Second
		}
		captureCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), captureWithin)
		defer cancel()
		for {
			snapshot, err := client.SealScenarioTestActions(captureCtx, receipt.RunID)
			if err != nil {
				evidence.Error = "could not seal platform action evidence"
				break
			}
			evidence.Snapshot = &snapshot
			if receipt.Actions.PostgresFaultSHA256 != "" && (snapshot.ManagedWork == nil || snapshot.ManagedWork.Plan.PostgresFault == nil || rehearsalpg.Hash(*snapshot.ManagedWork.Plan.PostgresFault) != receipt.Actions.PostgresFaultSHA256) {
				evidence.Error = "PostgreSQL fault coverage differs from enrollment"
				break
			}
			if receipt.Actions.SimulationSHA256 != "" {
				evidence.Provenance = actioncompare.SimulationProvenance
				if snapshot.Version != api.ScenarioSimulationSnapshotVersion || snapshot.SimulationSHA256 != receipt.Actions.SimulationSHA256 || snapshot.RecorderVersion != 1 {
					evidence.Error = "platform did not return the requested simulation coverage"
					break
				}
			} else if snapshot.Version != api.ScenarioActionSnapshotVersion {
				evidence.Error = "platform action coverage differs from enrollment"
				break
			}
			if snapshot.RunID != receipt.RunID || !sameTestActionWorkloads(workloads, snapshot.Workloads) {
				evidence.Error = "platform action workload inventory did not match this run"
				break
			}
			if (snapshot.ManagedWork == nil && receipt.Actions.ManagedWorkSHA256 != "") || (snapshot.ManagedWork != nil && snapshot.ManagedWork.PlanSHA256 != receipt.Actions.ManagedWorkSHA256) {
				evidence.Error = "platform managed-work coverage differs from enrollment"
				break
			}
			if (snapshot.SuspendResume == nil && receipt.Actions.SuspendResumeSHA256 != "") || (snapshot.SuspendResume != nil && snapshot.SuspendResume.PlanSHA256 != receipt.Actions.SuspendResumeSHA256) {
				evidence.Error = "suspend/resume enrollment differs from captured evidence"
				break
			}
			if (snapshot.ColdStart == nil && receipt.Actions.ColdStartSHA256 != "") || (snapshot.ColdStart != nil && snapshot.ColdStart.PlanSHA256 != receipt.Actions.ColdStartSHA256) {
				evidence.Error = "platform cold-start coverage differs from enrollment"
				break
			}
			if snapshot.SuspendResume != nil && snapshot.SuspendResume.Spec.ManagedJob() {
				observer, ok := client.(interface {
					GetScenarioSuspendResume(context.Context, string) (rehearsalresume.Evidence, error)
				})
				if !ok {
					evidence.Error = "running-job lifecycle observation unavailable"
					break
				}
				observed, readErr := observer.GetScenarioSuspendResume(captureCtx, receipt.RunID)
				if readErr != nil || observed.RunID != receipt.RunID || observed.PlanSHA256 != receipt.Actions.SuspendResumeSHA256 {
					evidence.Error = "running-job lifecycle observation differs from enrollment"
					break
				}
				receipt.SuspendResume = &observed
			}
			if snapshot.Status == "complete" {
				if err := validateTestSuspendBindings(*receipt, snapshot.SuspendResume); err != nil {
					evidence.Error = err.Error()
					break
				}
				if err := validateTestColdBindings(*receipt, snapshot.ColdStart); err != nil {
					evidence.Error = err.Error()
					break
				}
				if err := validateTestManagedWorkBindings(*receipt, snapshot.ManagedWork); err != nil {
					evidence.Error = err.Error()
					break
				}
				if _, err := actioncompare.ValidateSnapshot(&snapshot); err != nil {
					evidence.Error = "platform returned invalid action evidence"
					break
				}
				evidence.Status = "complete"
				if budgetError := testPostgresFaultBudgetError(snapshot.ManagedWork); budgetError != "" {
					if receipt.Error != "" {
						receipt.Error += "; "
					}
					receipt.Error += budgetError
					receipt.Status = "failed"
				}
				if budgetError := testSuspendResumeBudgetError(snapshot.SuspendResume); budgetError != "" {
					if receipt.Error != "" {
						receipt.Error += "; "
					}
					receipt.Error += budgetError
					receipt.Status = "failed"
				}
				if budgetError := testColdStartBudgetError(snapshot.ColdStart); budgetError != "" {
					if receipt.Error != "" {
						receipt.Error += "; "
					}
					receipt.Error += budgetError
					receipt.Status = "failed"
				}
				return
			}
			if snapshot.Status != "pending" {
				evidence.Error = "platform action snapshot is incomplete: " + snapshot.Reason
				break
			}
			timer := time.NewTimer(250 * time.Millisecond)
			select {
			case <-captureCtx.Done():
				timer.Stop()
				evidence.Error = "managed work did not settle before the action evidence deadline"
			case <-timer.C:
				continue
			}
			break
		}
		if receipt.Error != "" {
			receipt.Error += "; "
		}
		receipt.Error += "action evidence: " + evidence.Error
		receipt.Status = "failed"
	}
}

func sameTestActionWorkloads(expected, actual []string) bool {
	if len(expected) != len(actual) {
		return false
	}
	left, right := append([]string{}, expected...), append([]string{}, actual...)
	sort.Strings(left)
	sort.Strings(right)
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}

func compareTestActions(before, after testRunReceipt) *actioncompare.Comparison {
	toEvidence := func(run testRunReceipt) *actioncompare.Evidence {
		if run.Actions == nil {
			return nil
		}
		if (run.Actions.Network != "" && run.Actions.Network != "contained") || run.Actions.Webhooks != "capture" || run.Actions.PolicyVersion != 1 || run.Actions.Status != "applied" || run.Actions.Evidence == nil {
			return &actioncompare.Evidence{}
		}
		faultHash := ""
		if snapshot := run.Actions.Evidence.Snapshot; snapshot != nil && snapshot.ManagedWork != nil && snapshot.ManagedWork.Plan.PostgresFault != nil {
			faultHash = rehearsalpg.Hash(*snapshot.ManagedWork.Plan.PostgresFault)
		}
		if faultHash != run.Actions.PostgresFaultSHA256 {
			return &actioncompare.Evidence{}
		}
		resumeDigest := ""
		if snapshot := run.Actions.Evidence.Snapshot; snapshot != nil && snapshot.SuspendResume != nil {
			resumeDigest = snapshot.SuspendResume.PlanSHA256
			if validateTestSuspendBindings(run, snapshot.SuspendResume) != nil {
				return &actioncompare.Evidence{}
			}
		}
		if resumeDigest != run.Actions.SuspendResumeSHA256 {
			return &actioncompare.Evidence{}
		}
		coldDigest := ""
		if snapshot := run.Actions.Evidence.Snapshot; snapshot != nil && snapshot.ColdStart != nil {
			coldDigest = snapshot.ColdStart.PlanSHA256
		}
		if coldDigest != run.Actions.ColdStartSHA256 {
			return &actioncompare.Evidence{}
		}
		return run.Actions.Evidence
	}
	comparison := actioncompare.Compare(toEvidence(before), toEvidence(after))
	if comparison != nil && before.Actions != nil && after.Actions != nil && (before.Actions.SuspendResumeSHA256 != after.Actions.SuspendResumeSHA256 || before.Actions.ColdStartSHA256 != after.Actions.ColdStartSHA256 || before.Actions.ManagedWorkSHA256 != after.Actions.ManagedWorkSHA256 || before.Actions.Network != after.Actions.Network || before.Actions.SimulationSHA256 != after.Actions.SimulationSHA256) {
		comparison.Status, comparison.Reason = "inconclusive", "rehearsal execution or simulation policies differ"
	}
	if comparison != nil && (!testExecutionCompleteForEvidence(before) || !testExecutionCompleteForEvidence(after)) {
		comparison.Status, comparison.Reason = "inconclusive", "one or both executions or cleanup steps did not pass"
	}
	return comparison
}

func testActionComparisonStatus(runs []testRunComparison) string {
	status := ""
	for _, run := range runs {
		if run.Actions == nil {
			continue
		}
		if run.Actions.Status == "inconclusive" {
			return "inconclusive"
		}
		if status == "" || run.Actions.Status == "failed" {
			status = run.Actions.Status
		}
	}
	return status
}

func formatTestActionValues(values []actioncompare.Value) string {
	body, _ := json.Marshal(values)
	return formatTestBehaviorValue(body)
}

func printTestActionComparison(out io.Writer, comparison *actioncompare.Comparison) {
	if comparison == nil {
		return
	}
	_, _ = fmt.Fprintf(out, "  %s: %s (%s → %s %s)\n", strings.ToLower(testActionLabel(comparison)), comparison.Status, testActionCountText(comparison.BeforeCount), testActionCountText(comparison.AfterCount), testActionUnits(comparison))
	if comparison.Reason != "" {
		_, _ = fmt.Fprintf(out, "    %s\n", comparison.Reason)
	}
	for _, change := range comparison.Changes {
		_, _ = fmt.Fprintf(out, "    %s/%s/%s: %s (%d → %d)\n", change.Workload, change.Source, change.Name, change.Status, len(change.Before), len(change.After))
		if change.Status != "unchanged" {
			_, _ = fmt.Fprintf(out, "      %s → %s\n", formatTestActionValues(change.Before), formatTestActionValues(change.After))
		}
	}
}

func validateTestActions(spec *testActionSpec) error {
	if spec != nil && spec.Simulation != nil {
		if spec.Network != "contained" || spec.Webhooks != "capture" {
			return errors.New("actions.simulation requires contained networking and webhook capture")
		}
		names := make([]string, 0, len(spec.Simulation.Fixtures))
		for _, f := range spec.Simulation.Fixtures {
			names = append(names, f.Workload)
		}
		if _, _, err := api.NormalizeScenarioSimulationPlan(*spec.Simulation, names); err != nil {
			return err
		}
	}
	if spec != nil && spec.Network != "" && spec.Network != "contained" {
		return errors.New("actions.network must be contained when present")
	}
	if spec != nil && spec.Webhooks != "capture" {
		return errors.New("actions.webhooks must be capture")
	}
	if spec != nil && spec.QueueDelivery != "" && (spec.QueueDelivery != api.ScenarioQueueDeliverySameRun || spec.Network != "contained") {
		return errors.New("actions.queue_delivery must be same_run and requires contained networking")
	}
	return nil
}

func testActionCountText(count *int) string {
	if count == nil {
		return "unavailable"
	}
	return fmt.Sprint(*count)
}

func testSimulationPlanDigest(plan *api.ScenarioSimulationPlan, members []api.ScenarioTestWorkload) string {
	if plan == nil {
		return ""
	}
	names := make([]string, 0, len(members))
	for _, m := range members {
		names = append(names, m.Workload)
	}
	_, digest, _ := api.NormalizeScenarioSimulationPlan(*plan, names)
	return digest
}

func testActionLabel(c *actioncompare.Comparison) string {
	if c != nil && c.Scope == api.ScenarioSimulationScope {
		return "Rehearsal actions"
	}
	return "Managed webhook actions"
}
func testActionUnits(c *actioncompare.Comparison) string {
	if c != nil {
		for _, v := range c.Changes {
			if v.Source == "async" {
				return "captured actions and managed jobs"
			}
		}
	}
	if c != nil && c.Scope == api.ScenarioSimulationScope {
		return "captured intents and simulated attempts"
	}
	return "captured intents"
}
func testReportActionLabel(runs []testRunComparison) string {
	for _, r := range runs {
		if r.Actions != nil && r.Actions.Scope == api.ScenarioSimulationScope {
			return "Rehearsal actions"
		}
	}
	return "Managed webhook actions"
}
