package bindings

import (
	"context"
	"errors"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func (b VerificationRunBackend) Diagnose(ctx context.Context, id string) (api.BindingVerificationRunDiagnostics, error) {
	parsed, err := uuid.Parse(id)
	if err != nil || parsed == uuid.Nil {
		return api.BindingVerificationRunDiagnostics{}, state.ErrInvalidArgument
	}
	r, err := b.Read(ctx, parsed.String())
	if err != nil {
		return api.BindingVerificationRunDiagnostics{}, err
	}
	return b.diagnoseRun(ctx, r)
}
func (b VerificationRunBackend) diagnoseRun(ctx context.Context, r api.BindingVerificationRun) (api.BindingVerificationRunDiagnostics, error) {
	return b.diagnoseRunWithBudget(ctx, r, nil)
}
func (b VerificationRunBackend) diagnoseRunWithBudget(ctx context.Context, r api.BindingVerificationRun, shared *api.BindingVerificationBudget) (api.BindingVerificationRunDiagnostics, error) {
	if b.DiagnosticsAccess != nil {
		if err := b.DiagnosticsAccess(r); err != nil {
			return api.BindingVerificationRunDiagnostics{}, err
		}
	}
	out := diagnosticBase(r)
	var budget api.BindingVerificationBudget
	var budgetErr error
	if shared != nil {
		budget = *shared
	} else {
		budget, budgetErr = b.Store.BindingVerificationRunBudget(ctx, b.AccountID, b.AppID, r.ID)
	}
	if budgetErr != nil {
		out.Complete = false
		out.Blockers = append(out.Blockers, diagnosticBlocker("budget_unavailable", "The shared admission budget could not be read.", "retry_diagnostics"))
	} else {
		out.Budget = &budget
		if budget.Full && budget.WaitingSteps > 0 {
			out.Blockers = append(out.Blockers, diagnosticBlocker("binding_verification_budget_full", "The shared budget is occupied by unfinished resources; observe them before admitting another step.", "resume"))
		}
	}
	i, p, err := b.inventory(ctx, r.DeploymentID, r.Inventory.ServiceTargets)
	if err != nil {
		if diagnosticForbidden(err) {
			return out, err
		}
		out.Complete = false
		code, recovery := "inventory_unavailable", "retry_diagnostics"
		if diagnosticChanged(err) {
			code, recovery = "deployment_or_target_unavailable", "review_new_run"
		}
		out.Blockers = append(out.Blockers, diagnosticBlocker(code, "The original deployment or public binding selection could not be read.", recovery))
	} else {
		applyPublicDiagnostics(&out, r, i, p)
	}
	// This check is read-only and uses stored fences. It does not recapture any
	// private task command or claim that future admission will succeed.
	if r.Status == "pending" {
		if err = b.Store.CheckBindingVerificationRun(ctx, b.AccountID, b.AppID, r.ID); err != nil {
			if diagnosticChanged(err) {
				out.Blockers = append(out.Blockers, diagnosticBlocker("admission_snapshot_changed", "A stored admission fence or artifact/configuration snapshot is no longer current.", "review_new_run"))
			} else {
				out.Complete = false
				out.Blockers = append(out.Blockers, diagnosticBlocker("admission_check_unavailable", "The stored admission fences could not be checked.", "retry_diagnostics"))
			}
		}
	}
	finishDiagnostics(&out, r)
	return out, nil
}
func diagnosticBase(r api.BindingVerificationRun) api.BindingVerificationRunDiagnostics {
	out := api.BindingVerificationRunDiagnostics{RunID: r.ID, GraphRunID: r.GraphRunID, App: r.App, DeploymentID: r.DeploymentID, Scope: r.Scope, Status: r.Status, CheckedAt: time.Now().UTC(), ExpiresAt: r.ExpiresAt, Complete: true, Blockers: []api.BindingVerificationDiagnosticBlocker{}, Changes: []api.BindingVerificationDifference{}, Recovery: []api.BindingVerificationRecoveryAction{}}
	if r.Status == "cancelled" || r.Status == "expired" {
		out.Blockers = append(out.Blockers, diagnosticBlocker("run_"+r.Status, "The original receipt is terminal; its deadline and consent cannot be renewed.", "review_new_run"))
	}
	for index, step := range r.Steps {
		if step.ResourceID == "" {
			continue
		}
		code, recovery := "resource_failed", "review_new_run"
		switch step.Status {
		case "succeeded":
			continue
		case "queued", "restoring", "running", "published", "dispatching":
			code, recovery = "resource_pending", "resume"
		case "missing":
			code = "resource_missing"
		}
		out.Blockers = append(out.Blockers, api.BindingVerificationDiagnosticBlocker{App: r.App, Code: code, Message: "The reviewed resource is " + diagnosticResourceStatus(step.Status) + ".", Recovery: recovery, Type: step.Plan.Type, Name: step.Plan.Name, Binding: step.Plan.Binding, Scope: step.Plan.Scope, StepIndex: new(index), ResourceID: step.ResourceID, ResourceKind: step.ResourceKind, Status: diagnosticResourceStatus(step.Status)})
	}
	return out
}
func diagnosticResourceStatus(s string) string {
	switch s {
	case "queued", "restoring", "running", "published", "dispatching", "succeeded", "failed", "timed_out", "cancelled", "expired", "missing":
		return s
	default:
		return "unknown"
	}
}
func diagnosticBlocker(code, message, recovery string) api.BindingVerificationDiagnosticBlocker {
	return api.BindingVerificationDiagnosticBlocker{Code: code, Message: message, Recovery: recovery}
}
func diagnosticForbidden(err error) bool {
	var p *api.Problem
	return errors.As(err, &p) && (p.Status == http.StatusForbidden || p.Status == http.StatusUnauthorized)
}
func diagnosticChanged(err error) bool {
	var p *api.Problem
	return errors.Is(err, state.ErrConflict) || errors.Is(err, state.ErrNotFound) || errors.Is(err, state.ErrBindingReleasePolicyRevision) || errors.As(err, &p) && (p.Status == 400 || p.Status == 404 || p.Status == 409 || p.Status == 412)
}

func applyPublicDiagnostics(out *api.BindingVerificationRunDiagnostics, r api.BindingVerificationRun, i api.AppBindingInventory, p api.BindingReleasePolicy) {
	if !i.Complete || len(i.Issues) > 0 || i.App != r.App || i.VerificationDeploymentID != r.DeploymentID || i.VerificationScope != r.Scope || p.AppID != r.Policy.AppID || p.Scope != r.Scope {
		out.Complete = false
		out.Blockers = append(out.Blockers, diagnosticBlocker("inventory_unavailable", "Complete metadata for the original selection is unavailable.", "retry_diagnostics"))
		return
	}
	compareDiagnosticMetadata(out, r, i, p)
	report, err := evaluateVerificationRun(i, p)
	if err != nil {
		out.Complete = false
		out.Blockers = append(out.Blockers, diagnosticBlocker("evaluation_unavailable", "The current public policy and evidence could not be evaluated.", "retry_diagnostics"))
		return
	}
	out.CheckPassed = new(report.Passed)
	for _, finding := range report.Blockers {
		blocker := api.BindingVerificationDiagnosticBlocker{App: r.App, Code: finding.Code, Message: diagnosticFindingMessage(finding.Code), Recovery: "wait", Type: finding.Type, Name: finding.Name, Binding: finding.Binding, Scope: finding.Scope}
		if strings.Contains(finding.Code, "application_ack") || strings.Contains(finding.Code, "application_adoption") {
			blocker.Recovery = "wait_for_application_ack"
		} else {
			for index, step := range r.Steps {
				if step.Plan.Type == finding.Type && (finding.Binding != "" && step.Plan.Binding == finding.Binding || finding.Name != "" && step.Plan.Name == finding.Name) {
					blocker.StepIndex = new(index)
					blocker.ResourceID = step.ResourceID
					blocker.ResourceKind = step.ResourceKind
					blocker.Status = diagnosticResourceStatus(step.Status)
					if step.ResourceID == "" && (step.Plan.Action == "probe" || step.Plan.Action == "queue-canary") || step.ResourceID != "" && step.Status != "succeeded" && diagnosticResourceStatus(step.Status) != "unknown" {
						blocker.Recovery = "resume"
					} else if strings.Contains(finding.Code, "expired") || strings.Contains(finding.Code, "verification") || strings.Contains(finding.Code, "unverified") || strings.Contains(finding.Code, "operations_missing") {
						blocker.Recovery = "review_new_run"
					}
					break
				}
			}
		}
		out.Blockers = append(out.Blockers, blocker)
	}
}
func diagnosticFindingMessage(code string) string {
	switch {
	case strings.Contains(code, "application_ack") || strings.Contains(code, "application_adoption"):
		return "The application has not acknowledged the current binding configuration."
	case strings.Contains(code, "expired"):
		return "Required binding evidence is older than the current policy permits."
	case strings.Contains(code, "cleanup"):
		return "Current evidence does not confirm successful object cleanup."
	case strings.Contains(code, "upload"):
		return "Current evidence does not confirm the required upload."
	case strings.Contains(code, "download"):
		return "Current evidence does not confirm the required download."
	case strings.Contains(code, "queue_completion"):
		return "Current evidence does not confirm safe queue handler completion."
	case strings.Contains(code, "runtime") || strings.Contains(code, "freshness"):
		return "Current application instances do not confirm configuration freshness."
	default:
		return "Current public evidence does not satisfy the required binding check."
	}
}
func finishDiagnostics(out *api.BindingVerificationRunDiagnostics, r api.BindingVerificationRun) {
	for index := range out.Blockers {
		if out.Blockers[index].App == "" {
			out.Blockers[index].App = r.App
		}
		if out.Blockers[index].Scope == "" {
			out.Blockers[index].Scope = r.Scope
		}
	}
	sort.SliceStable(out.Blockers, func(i, j int) bool {
		a, b := out.Blockers[i], out.Blockers[j]
		return a.App+"\x00"+a.Type+"\x00"+a.Name+"\x00"+a.Binding+"\x00"+a.Code < b.App+"\x00"+b.Type+"\x00"+b.Name+"\x00"+b.Binding+"\x00"+b.Code
	})
	sort.SliceStable(out.Changes, func(i, j int) bool {
		a, b := out.Changes[i], out.Changes[j]
		return a.App+a.Type+a.Name+a.Binding+a.Field < b.App+b.Type+b.Name+b.Binding+b.Field
	})
	out.Recovery = diagnosticRecovery(out.Blockers, []api.BindingVerificationRun{r})
}
func diagnosticRecovery(blockers []api.BindingVerificationDiagnosticBlocker, runs []api.BindingVerificationRun) []api.BindingVerificationRecoveryAction {
	needed := map[string]bool{}
	unfinished := false
	work := false
	for _, b := range blockers {
		needed[b.Recovery] = true
	}
	for _, r := range runs {
		for _, step := range r.Steps {
			unfinished = unfinished || step.Owned && step.ResourceID != "" && step.Status != "succeeded" && step.Status != "failed" && step.Status != "timed_out" && step.Status != "cancelled" && step.Status != "expired"
			work = work || step.ResourceID == "" && (step.Plan.Action == "probe" || step.Plan.Action == "queue-canary")
		}
	}
	if needed["review_new_run"] {
		needed = map[string]bool{"review_new_run": true, "cancel": unfinished}
	} else if needed["retry_diagnostics"] {
		needed = map[string]bool{"retry_diagnostics": true}
	} else if len(blockers) == 0 || work {
		needed["resume"] = true
	}
	messages := map[string]string{"cancel": "Use explicit cancellation to fence further admission and clean unfinished owned work.", "review_new_run": "Review current selections and create a new consented receipt; do not renew or replace this run implicitly.", "retry_diagnostics": "Retry diagnostics when complete metadata is available; no conclusion about admission is authorized.", "wait_for_application_ack": "Wait for the application's acknowledgment of the current configuration, then inspect or resume this receipt.", "wait": "Wait for current application readiness or evidence, then inspect or resume this receipt.", "resume": "Resume the original run ID to continue existing work and request fresh server qualification."}
	out := []api.BindingVerificationRecoveryAction{}
	for _, action := range []string{"cancel", "review_new_run", "retry_diagnostics", "wait_for_application_ack", "wait", "resume"} {
		if needed[action] {
			out = append(out, api.BindingVerificationRecoveryAction{Action: action, Message: messages[action]})
		}
	}
	return out
}
