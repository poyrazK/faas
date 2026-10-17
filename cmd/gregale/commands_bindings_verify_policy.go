// adr: 1347 — policy-driven verification qualifies mixed binding dependencies.
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/bindingcheck"
)

type bindingPolicyProbePlan = api.BindingVerificationPlanStep

type bindingPolicyProbeResult struct {
	ResourceID     string                               `json:"resource_id,omitempty"`
	ResourceKind   string                               `json:"resource_kind,omitempty"`
	Type           string                               `json:"type"`
	Name           string                               `json:"name"`
	Binding        string                               `json:"binding"`
	Scope          string                               `json:"scope"`
	TaskID         string                               `json:"task_id,omitempty"`
	Prefix         string                               `json:"prefix,omitempty"`
	Status         string                               `json:"status"`
	Report         *api.ObjectStorageBindingProbeReport `json:"report,omitempty"`
	ServiceReport  *api.ServiceBindingProbeReport       `json:"service_report,omitempty"`
	PostgresReport *api.PostgresBindingProbeReport      `json:"postgres_report,omitempty"`
	QueueReport    *api.QueueBindingCanary              `json:"queue_report,omitempty"`
	OutboundReport *api.OutboundBindingProbeReport      `json:"outbound_report,omitempty"`
}

type bindingPolicyVerificationReport struct {
	Recovery                    *bindingRecoverySummary                `json:"recovery,omitempty"`
	Diagnostics                 *api.BindingVerificationRunDiagnostics `json:"diagnostics,omitempty"`
	ReviewDigest                string                                 `json:"review_digest,omitempty"`
	RunID                       string                                 `json:"run_id,omitempty"`
	App                         string                                 `json:"app"`
	DeploymentID                string                                 `json:"deployment_id,omitempty"`
	Scope                       string                                 `json:"scope,omitempty"`
	DryRun                      bool                                   `json:"dry_run"`
	PlanReady                   bool                                   `json:"plan_ready"`
	QueuePublishConsentRequired bool                                   `json:"queue_publish_consent_required"`
	WriteConsentRequired        bool                                   `json:"write_consent_required"`
	WriteConsent                bool                                   `json:"write_consent"`
	SnapshotConfirmed           bool                                   `json:"snapshot_confirmed"`
	Passed                      bool                                   `json:"passed"`
	Status                      string                                 `json:"status"`
	ReleasePolicy               *api.BindingReleasePolicy              `json:"release_policy,omitempty"`
	ConfigChangedAt             *time.Time                             `json:"config_changed_at,omitempty"`
	Plan                        []bindingPolicyProbePlan               `json:"plan"`
	PlanBlockers                []bindingcheck.Finding                 `json:"plan_blockers,omitempty"`
	ObservedCheck               *bindingcheck.Report                   `json:"observed_check,omitempty"`
	Probes                      []bindingPolicyProbeResult             `json:"probes"`
	Preflight                   *bindingReleasePolicyCheckReport       `json:"preflight,omitempty"`
	ErrorCode                   string                                 `json:"error_code,omitempty"`
	Error                       string                                 `json:"error,omitempty"`
}

func runBindingPolicyVerification(ctx context.Context, client *api.Client, slug, deployment string, dryRun, writeConsent bool, interval, timeout time.Duration, targets ...map[string]string) int {
	interrupted, stop := signal.NotifyContext(ctx, os.Interrupt)
	defer stop()
	bounded, cancel := context.WithTimeout(interrupted, timeout)
	defer cancel()
	report, code := executeBindingPolicyVerification(bounded, client, slug, deployment, dryRun, writeConsent, interval, timeout, targets...)
	if jsonOutput {
		if outputCode := jsonOut(writeJSON(report)); outputCode != 0 {
			return outputCode
		}
	} else {
		renderBindingPolicyVerification(report)
	}
	return code
}

func executeBindingPolicyVerification(ctx context.Context, client *api.Client, slug, deployment string, dryRun, consent bool, interval, timeout time.Duration, targets ...map[string]string) (bindingPolicyVerificationReport, int) {
	if dryRun {
		return executeServerBindingReview(ctx, client, slug, deployment, consent, targets...)
	}
	r := bindingPolicyVerificationReport{App: slug, DryRun: dryRun, WriteConsent: consent, Status: "blocked",
		Plan: []bindingPolicyProbePlan{}, Probes: []bindingPolicyProbeResult{}}
	snapshot, err := prepareBindingPolicyVerification(ctx, client, slug, deployment, targets...)
	if err != nil {
		return bindingPolicyVerificationFailed(r, "plan_unavailable", err)
	}
	if expected, ok := ctx.Value(bindingPolicyExpectedRevisionKey{}).(int64); ok && expected != snapshot.policy.Revision {
		return bindingPolicyVerificationFailed(r, "release_policy_changed", errors.New("prepared graph policy changed; prepare and qualify again"))
	}
	r.DeploymentID, r.Scope = snapshot.inventory.VerificationDeploymentID, snapshot.policy.Scope
	r.ReleasePolicy, r.ConfigChangedAt = &snapshot.policy, snapshot.inventory.RuntimeFreshness.ConfigChangedAt
	r.Plan, r.PlanBlockers, r.ObservedCheck, err = bindingPolicyVerificationPlan(snapshot.inventory, snapshot.policy, time.Now())
	if err != nil {
		return bindingPolicyVerificationFailed(r, "plan_invalid", err)
	}
	if run, ok := ctx.Value(bindingVerificationRunKey{}).(*api.BindingVerificationRun); ok {
		r.RunID = run.ID
		r.Plan = make([]bindingPolicyProbePlan, len(run.Steps))
		for i, step := range run.Steps {
			r.Plan[i] = step.Plan
		}
	}
	r.PlanReady = true
	for _, step := range r.Plan {
		r.QueuePublishConsentRequired = r.QueuePublishConsentRequired || step.Action == "queue-canary"
		r.WriteConsentRequired = r.WriteConsentRequired || step.Action == "probe" && step.Type == api.BindingTypeObjectStorage && step.ProbeMode != "read"
	}
	if err := snapshot.confirm(ctx); err != nil {
		return bindingPolicyVerificationFailed(r, "snapshot_unconfirmed", err)
	}
	r.SnapshotConfirmed = true
	if r.WriteConsentRequired && !consent {
		return bindingPolicyVerificationFailed(r, "write_consent_required", errors.New("the plan requires upload and cleanup; review --dry-run, then supply --object-storage-write to consent to every planned write and read/write download"))
	}
	queueConsent, _ := ctx.Value(queueCanaryConsentKey{}).(bool)
	for _, step := range r.Plan {
		if step.Action == "queue-canary" && !queueConsent {
			return bindingPolicyVerificationFailed(r, "queue_publish_consent_required", errors.New("supply --queue-publish to consent to the planned safe queue messages"))
		}
	}
	ctx = withBindingVerificationBudget(ctx, bindingVerificationConcurrency(ctx))
	outcomes := make([]bindingVerificationOutcome, len(r.Plan))
	runBindingVerificationJobs(ctx, len(r.Plan), func(jobCtx context.Context, index int) error {
		step := r.Plan[index]
		if run, ok := ctx.Value(bindingVerificationRunKey{}).(*api.BindingVerificationRun); ok {
			jobCtx = context.WithValue(jobCtx, bindingVerificationRunStepKey{}, bindingVerificationRunStepContext{Run: run, Index: index})
		}
		if run, ok := ctx.Value(bindingVerificationRunKey{}).(*api.BindingVerificationRun); ok && run.Status == "passed" {
			return nil
		}
		if step.Action != "probe" && step.Action != "queue-canary" && step.Action != "wait-queue" {
			return nil
		}
		return withBindingVerificationSlot(jobCtx, func() error {
			outcomes[index] = executeBindingPolicyStep(jobCtx, client, snapshot, step, queueConsent, interval, timeout)
			return outcomes[index].err
		})
	})
	var failure *bindingVerificationOutcome
	for index := range outcomes {
		outcome := &outcomes[index]
		if outcome.result != nil {
			r.Probes = append(r.Probes, *outcome.result)
		}
		if outcome.err != nil && (failure == nil || errors.Is(failure.err, context.Canceled) && !errors.Is(outcome.err, context.Canceled)) {
			failure = outcome
		}
	}
	if failure != nil {
		r.SnapshotConfirmed = false
		return bindingPolicyVerificationFailed(r, failure.code, failure.err)
	}
	if err := ctx.Err(); err != nil {
		return bindingPolicyVerificationFailed(r, "verification_cancelled", err)
	}
	return finalizeBindingPolicyVerification(ctx, snapshot, r, interval)
}

func finalizeBindingPolicyVerification(ctx context.Context, snapshot *bindingPolicySnapshotClient, r bindingPolicyVerificationReport, interval time.Duration) (bindingPolicyVerificationReport, int) {
	// Confirm the original snapshot through every fresh preflight read. Do not
	// silently accept a new policy by starting an independent check at the end.
	age, _ := time.ParseDuration(snapshot.policy.MaxVerificationAge)
	policy := bindingcheck.Policy{App: snapshot.slug, Scope: r.Scope, DeploymentID: r.DeploymentID, MaxVerificationAge: age}
	preflight, err := pollBindingCheckReleasePolicy(ctx, snapshot, policy, true, interval)
	r.Preflight = &preflight
	if err != nil {
		r.SnapshotConfirmed = false
		return bindingPolicyVerificationFailed(r, "preflight_incomplete", err)
	}
	if !preflight.Passed || !preflight.ReleasePolicyConfirmed {
		return bindingPolicyVerificationFailed(r, "preflight_blocked", errors.New("binding preflight still has blockers; inspect preflight.blockers"))
	}
	r.Status, r.Passed = "passed", true
	return r, 0
}

func bindingPolicyVerificationFailed(r bindingPolicyVerificationReport, code string, err error) (bindingPolicyVerificationReport, int) {
	var snapshotErr *bindingPolicySnapshotError
	if errors.As(err, &snapshotErr) {
		code = snapshotErr.code
	}
	if errors.Is(err, context.DeadlineExceeded) {
		code = "verification_timeout"
	}
	r.Status, r.Passed, r.ErrorCode, r.Error = "blocked", false, code, err.Error()
	if errors.Is(err, context.Canceled) {
		r.Status = "cancelled"
		return r, 130
	}
	return r, 1
}

func renderBindingPolicyVerification(r bindingPolicyVerificationReport) {
	_, _ = fmt.Fprintf(osStdout, "Binding release verification: %s deployment=%s scope=%s status=%s\n", r.App, r.DeploymentID, r.Scope, r.Status)
	if r.ReleasePolicy != nil {
		_, _ = fmt.Fprintf(osStdout, "Policy revision=%d; write consent required=%t granted=%t\n", r.ReleasePolicy.Revision, r.WriteConsentRequired, r.WriteConsent)
	}
	for _, step := range r.Plan {
		selector := step.Binding
		if selector == "" || step.Type == api.BindingTypeService || step.Type == api.BindingTypeOutbound {
			selector = step.Name
		}
		_, _ = fmt.Fprintf(osStdout, "%s %s: %s (%s)\n", step.Type, selector, step.Action, step.Reason)
		if step.Prefix != "" {
			_, _ = fmt.Fprintf(osStdout, "  storage canary=%s operations=%v\n", step.ProbeMode, step.Operations)
		}
		if step.RequiredEvidence != "" {
			_, _ = fmt.Fprintf(osStdout, "  required=%s; connectivity remains unverified\n", step.RequiredEvidence)
		}
	}
	for _, probe := range r.Probes {
		renderBindingPolicyProbe(r.App, probe)
	}
	for _, blocker := range r.PlanBlockers {
		PrintFail(osStderr, "%s %s: %s", blocker.Type, blocker.Binding, blocker.Message)
	}
	if r.Preflight != nil {
		renderBindingCheck(r.Preflight.Report)
	}
	if r.Diagnostics != nil {
		renderAutomaticDiagnostics(r.Diagnostics.Blockers, r.Diagnostics.Recovery)
	}
	renderBindingRecovery(r.Recovery)
	if r.Error != "" {
		PrintFail(osStderr, "%s: %s", r.ErrorCode, r.Error)
	}
	_, _ = fmt.Fprintln(osStdout, "Snapshot confirmation records observations; promotion rechecks policy and evidence at the traffic write.")
}
