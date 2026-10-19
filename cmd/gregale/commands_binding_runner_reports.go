package main

import (
	"context"
	"errors"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/bindingrun"
)

// Durable qualification is confirmed by the server. Reports expose receipt
// status and resource IDs; they never fabricate guest probe stage results.
func reportFromDurableRun(run api.BindingVerificationRun, err error) (bindingPolicyVerificationReport, int) {
	r := bindingPolicyVerificationReport{ReviewDigest: run.ReviewDigest, RunID: run.ID, App: run.App, DeploymentID: run.DeploymentID, Scope: run.Scope, Status: run.Status, Passed: err == nil && run.Status == "passed", PlanReady: run.ReviewDigest != "", SnapshotConfirmed: err == nil, WriteConsent: run.ObjectStorageWrite, ReleasePolicy: &run.Policy, Plan: []bindingPolicyProbePlan{}, Probes: []bindingPolicyProbeResult{}}
	if run.Policy.AppID == "" {
		r.ReleasePolicy = nil
	}
	if run.Inventory.RuntimeFreshness != nil {
		r.ConfigChangedAt = run.Inventory.RuntimeFreshness.ConfigChangedAt
	}
	for _, step := range run.Steps {
		r.Plan = append(r.Plan, step.Plan)
		r.WriteConsentRequired = r.WriteConsentRequired || step.Plan.Action == "probe" && step.Plan.Type == api.BindingTypeObjectStorage && step.Plan.ProbeMode != "read"
		r.QueuePublishConsentRequired = r.QueuePublishConsentRequired || step.Plan.Action == "queue-canary"
		if step.ResourceID == "" {
			continue
		}
		status := step.Status
		if status == "succeeded" {
			status = "passed"
		}
		probe := bindingPolicyProbeResult{Type: step.Plan.Type, Name: step.Plan.Name, Binding: step.Plan.Binding, Scope: step.Plan.Scope, Prefix: step.Plan.Prefix, Status: status, ResourceID: step.ResourceID, ResourceKind: step.ResourceKind}
		if step.ResourceKind == "task" {
			probe.TaskID = step.ResourceID
		}
		r.Probes = append(r.Probes, probe)
	}
	if err == nil {
		return r, 0
	}
	var recovery *bindingrun.RecoveryError
	if errors.As(err, &recovery) && recovery.Diagnostics != nil && recovery.Diagnostics.RunID == run.ID && recovery.Diagnostics.App == run.App {
		r.Diagnostics = recovery.Diagnostics
	}
	r.Passed = false
	r.Status = "blocked"
	r.ErrorCode = "verification_run_stopped"
	r.Error = err.Error()
	if errors.Is(err, context.Canceled) {
		r.Status = "cancelled"
		r.ErrorCode = "verification_cancelled"
		return r, 130
	}
	if errors.Is(err, context.DeadlineExceeded) {
		r.ErrorCode = "verification_timeout"
	}
	return r, 1
}

func renderAutomaticDiagnostics(blockers []api.BindingVerificationDiagnosticBlocker, recovery []api.BindingVerificationRecoveryAction) {
	for _, blocker := range blockers[:min(len(blockers), 3)] {
		label := blocker.Binding
		if blocker.App != "" {
			label = blocker.App + "/" + label
		}
		PrintWarn(osStderr, "%s %s: %s", label, blocker.Code, blocker.Message)
	}
	if len(blockers) > 3 {
		PrintWarn(osStderr, "%d more blockers are available in the diagnostics report.", len(blockers)-3)
	}
	for _, action := range recovery[:min(len(recovery), 2)] {
		PrintWarn(osStderr, "%s: %s", action.Action, action.Message)
	}
}
