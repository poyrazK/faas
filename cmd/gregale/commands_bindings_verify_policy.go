// adr: 971 — policy-driven storage verification preserves explicit write consent.
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"sort"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/bindingcheck"
)

type storagePolicyProbePlan struct {
	Prefix       string   `json:"prefix"`
	RequiredMode string   `json:"required_mode"`
	ProbeMode    string   `json:"probe_mode"`
	Operations   []string `json:"operations"`
}

type storagePolicyProbeResult struct {
	Prefix string                              `json:"prefix"`
	Status string                              `json:"status"`
	Report api.ObjectStorageBindingProbeReport `json:"report"`
}

type storagePolicyVerificationReport struct {
	App                  string                           `json:"app"`
	DeploymentID         string                           `json:"deployment_id,omitempty"`
	Scope                string                           `json:"scope,omitempty"`
	DryRun               bool                             `json:"dry_run"`
	PlanReady            bool                             `json:"plan_ready"`
	WriteConsentRequired bool                             `json:"write_consent_required"`
	WriteConsent         bool                             `json:"write_consent"`
	SnapshotConfirmed    bool                             `json:"snapshot_confirmed"`
	Passed               bool                             `json:"passed"`
	Status               string                           `json:"status"`
	ReleasePolicy        *api.BindingReleasePolicy        `json:"release_policy,omitempty"`
	ConfigChangedAt      *time.Time                       `json:"config_changed_at,omitempty"`
	Plan                 []storagePolicyProbePlan         `json:"plan"`
	Probes               []storagePolicyProbeResult       `json:"probes"`
	Preflight            *bindingReleasePolicyCheckReport `json:"preflight,omitempty"`
	ErrorCode            string                           `json:"error_code,omitempty"`
	Error                string                           `json:"error,omitempty"`
}

func runStoragePolicyVerification(ctx context.Context, client *api.Client, slug, deployment string, dryRun, writeConsent bool, interval, timeout time.Duration) int {
	interrupted, stop := signal.NotifyContext(ctx, os.Interrupt)
	defer stop()
	bounded, cancel := context.WithTimeout(interrupted, timeout)
	defer cancel()
	report, code := executeStoragePolicyVerification(bounded, client, slug, deployment, dryRun, writeConsent, interval, timeout)
	if jsonOutput {
		if outputCode := jsonOut(writeJSON(report)); outputCode != 0 {
			return outputCode
		}
	} else {
		renderStoragePolicyVerification(report)
	}
	return code
}

func executeStoragePolicyVerification(ctx context.Context, client *api.Client, slug, deployment string, dryRun, consent bool, interval, timeout time.Duration) (storagePolicyVerificationReport, int) {
	r := storagePolicyVerificationReport{App: slug, DryRun: dryRun, WriteConsent: consent, Status: "blocked",
		Plan: []storagePolicyProbePlan{}, Probes: []storagePolicyProbeResult{}}
	snapshot, err := prepareStoragePolicyVerification(ctx, client, slug, deployment)
	if err != nil {
		return storagePolicyVerificationFailed(r, "plan_unavailable", err)
	}
	r.DeploymentID, r.Scope = snapshot.inventory.VerificationDeploymentID, snapshot.policy.Scope
	r.ReleasePolicy, r.ConfigChangedAt = &snapshot.policy, snapshot.inventory.RuntimeFreshness.ConfigChangedAt
	r.Plan, err = storagePolicyVerificationPlan(snapshot.inventory, snapshot.policy)
	if err != nil {
		return storagePolicyVerificationFailed(r, "plan_invalid", err)
	}
	r.PlanReady = true
	for _, step := range r.Plan {
		r.WriteConsentRequired = r.WriteConsentRequired || step.ProbeMode != "read"
	}
	if err := snapshot.confirm(ctx); err != nil {
		return storagePolicyVerificationFailed(r, "snapshot_unconfirmed", err)
	}
	r.SnapshotConfirmed = true
	if dryRun {
		r.Status = "planned" // A preview is never a verification pass.
		return r, 0
	}
	if r.WriteConsentRequired && !consent {
		return storagePolicyVerificationFailed(r, "write_consent_required", errors.New("the plan requires upload and cleanup; review --dry-run, then supply --object-storage-write to consent to every planned write and read/write download"))
	}
	probeClient := &storagePolicyProbeClient{deploymentBindingProbeClient: &deploymentBindingProbeClient{Client: client, slug: slug, inventory: snapshot.inventory}, snapshot: snapshot}
	for _, step := range r.Plan {
		r.SnapshotConfirmed = false
		mode := step.ProbeMode
		if mode == "read" {
			mode = "" // The existing listing command uses the legacy empty mode.
		}
		probe, _, title, code, err := executeObjectStorageBindingProbeMode(ctx, probeClient, slug, step.Prefix, mode, interval, timeout)
		status := "passed"
		if code != 0 {
			status = "failed"
		}
		r.Probes = append(r.Probes, storagePolicyProbeResult{Prefix: step.Prefix, Status: status, Report: probe})
		if code != 0 {
			if err == nil {
				if code == 130 {
					err = errors.New("verification interrupted; cancellation requested for the admitted canary task")
				} else {
					err = fmt.Errorf("%s: %s", step.Prefix, probe.Error)
				}
			}
			if title != "" {
				err = fmt.Errorf("%s: %w", title, err)
			}
			r, failedCode := storagePolicyVerificationFailed(r, "probe_failed", err)
			if code == 130 {
				r.Status = "cancelled"
				failedCode = 130
			}
			return r, failedCode
		}
		if err := snapshot.confirm(ctx); err != nil {
			return storagePolicyVerificationFailed(r, "snapshot_unconfirmed", err)
		}
		r.SnapshotConfirmed = true
	}
	// Confirm the original snapshot through every fresh preflight read. Do not
	// silently accept a new policy by starting an independent check at the end.
	age, _ := time.ParseDuration(snapshot.policy.MaxVerificationAge)
	policy := bindingcheck.Policy{App: slug, Scope: r.Scope, DeploymentID: r.DeploymentID, MaxVerificationAge: age}
	preflight, err := pollBindingCheckReleasePolicy(ctx, snapshot, policy, false, interval)
	r.Preflight = &preflight
	if err != nil {
		r.SnapshotConfirmed = false
		return storagePolicyVerificationFailed(r, "preflight_incomplete", err)
	}
	if !preflight.Passed || !preflight.ReleasePolicyConfirmed {
		return storagePolicyVerificationFailed(r, "preflight_blocked", errors.New("required storage canaries completed, but binding preflight still has blockers; inspect preflight.blockers"))
	}
	r.Status, r.Passed = "passed", true
	return r, 0
}

func storagePolicyVerificationPlan(inventory api.AppBindingInventory, policy api.BindingReleasePolicy) ([]storagePolicyProbePlan, error) {
	plan := []storagePolicyProbePlan{}
	if policy.Mode != "enforce" || len(policy.ObjectStorageRequirements) == 0 {
		return plan, errors.New("an enforced release policy with explicit object-storage requirements is required")
	}
	prefixes := make([]string, 0, len(policy.ObjectStorageRequirements))
	for prefix := range policy.ObjectStorageRequirements {
		prefixes = append(prefixes, prefix)
	}
	sort.Strings(prefixes)
	for _, prefix := range prefixes {
		required := policy.ObjectStorageRequirements[prefix]
		var selected *api.AppBindingInventoryItem
		for index := range inventory.Bindings {
			item := &inventory.Bindings[index]
			if item.Type != api.BindingTypeObjectStorage || item.Binding != prefix || item.Scope != policy.Scope {
				continue
			}
			if selected != nil {
				return plan, fmt.Errorf("required prefix %s is ambiguous in scope %s", prefix, policy.Scope)
			}
			selected = item
		}
		if selected == nil || selected.State != "active" || selected.RotationPending == nil || *selected.RotationPending {
			return plan, fmt.Errorf("required prefix %s must have one active binding with confirmed settled rotation", prefix)
		}
		if selected.Access != required && selected.Access != "read_write" {
			return plan, fmt.Errorf("required prefix %s permission does not cover %s", prefix, required)
		}
		step := storagePolicyProbePlan{Prefix: prefix, RequiredMode: required, ProbeMode: "read", Operations: []string{"list"}}
		if required != "read" {
			// Admission requires the actual binding permission. A read/write
			// binding therefore also tests download when policy only asks for write.
			step.ProbeMode = selected.Access
			step.Operations = []string{"upload", "cleanup"}
			if selected.Access == "read_write" {
				step.Operations = []string{"upload", "download", "cleanup"}
			}
		}
		plan = append(plan, step)
	}
	sort.Slice(plan, func(i, j int) bool { return plan[i].Prefix < plan[j].Prefix })
	return plan, nil
}

func storagePolicyVerificationFailed(r storagePolicyVerificationReport, code string, err error) (storagePolicyVerificationReport, int) {
	var snapshotErr *storagePolicySnapshotError
	if errors.As(err, &snapshotErr) {
		code = snapshotErr.code
	}
	r.Status, r.Passed, r.ErrorCode, r.Error = "blocked", false, code, err.Error()
	if errors.Is(err, context.Canceled) {
		r.Status = "cancelled"
		return r, 130
	}
	return r, 1
}

func renderStoragePolicyVerification(r storagePolicyVerificationReport) {
	_, _ = fmt.Fprintf(osStdout, "Storage release verification: %s deployment=%s scope=%s status=%s\n", r.App, r.DeploymentID, r.Scope, r.Status)
	if r.ReleasePolicy != nil {
		_, _ = fmt.Fprintf(osStdout, "Policy revision=%d; write consent required=%t granted=%t\n", r.ReleasePolicy.Revision, r.WriteConsentRequired, r.WriteConsent)
	}
	for _, step := range r.Plan {
		_, _ = fmt.Fprintf(osStdout, "%s: required=%s probe=%s operations=%v\n", step.Prefix, step.RequiredMode, step.ProbeMode, step.Operations)
	}
	for _, probe := range r.Probes {
		renderObjectStorageBindingProbeReport(r.App, probe.Report, api.AppTaskResponse{ID: probe.Report.TaskID, DeploymentID: probe.Report.DeploymentID})
	}
	if r.Preflight != nil {
		renderBindingCheck(r.Preflight.Report)
	}
	if r.Error != "" {
		PrintFail(osStderr, "%s: %s", r.ErrorCode, r.Error)
	}
	_, _ = fmt.Fprintln(osStdout, "Snapshot confirmation records observations; promotion rechecks policy and evidence at the traffic write.")
}
