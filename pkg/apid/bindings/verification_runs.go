// adr:1355 — API-side plan review and receipt lifecycle.
package bindings

import (
	"context"
	"errors"
	"fmt"
	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/bindingcheck"
	"github.com/onebox-faas/faas/pkg/state"
	"maps"
	"net/http"
	"time"
)

type VerificationRunBackend struct {
	DiagnosticsAccess     func(api.BindingVerificationRun) error
	Store                 state.BindingVerificationRunStore
	Tasks                 state.AppTaskStore
	Canaries              state.QueueBindingCanaryStore
	Inventory             func(context.Context, string) (api.AppBindingInventory, api.BindingReleasePolicy, error)
	InventoryTargets      func(context.Context, string, map[string]string) (api.AppBindingInventory, api.BindingReleasePolicy, error)
	CaptureTask           func(context.Context, string, api.BindingVerificationPlanStep, time.Time) (*state.CreateAppTaskParams, error)
	TaskProbesEnabled     *bool
	AccountID, AppID, App string
}

func (b VerificationRunBackend) Read(ctx context.Context, id string) (api.BindingVerificationRun, error) {
	return b.Store.GetBindingVerificationRun(ctx, b.AccountID, b.AppID, id)
}
func (b VerificationRunBackend) Create(ctx context.Context, in api.CreateBindingVerificationRunRequest) (api.BindingVerificationRun, error) {
	if in.MaxConcurrency < 0 || in.MaxConcurrency > api.BindingVerificationRunnerMaxConcurrency {
		return api.BindingVerificationRun{}, state.ErrInvalidArgument
	}
	in.MaxConcurrency = api.BindingVerificationConcurrency(in.MaxConcurrency)
	if parsed, err := uuid.Parse(in.ID); err != nil || parsed == uuid.Nil || parsed.String() != in.ID || in.TTLSeconds < 1 || in.TTLSeconds > api.BindingVerificationRunMaxTTLSeconds {
		return api.BindingVerificationRun{}, state.ErrInvalidArgument
	}
	if old, err := b.Read(ctx, in.ID); err == nil {
		if old.MaxConcurrency != in.MaxConcurrency || old.DeploymentID != in.DeploymentID || old.ReviewDigest != in.ReviewDigest || !maps.Equal(old.Inventory.ServiceTargets, in.ServiceTargets) || old.QueuePublish != in.QueuePublish || old.ObjectStorageWrite != in.ObjectStorageWrite {
			return old, state.ErrConflict
		}
		return old, nil
	} else if !errors.Is(err, state.ErrNotFound) {
		return old, err
	}
	now := time.Now().UTC().Truncate(time.Microsecond)
	prepared, err := b.Prepare(ctx, in, now, now.Add(time.Duration(in.TTLSeconds)*time.Second))
	if err != nil {
		return api.BindingVerificationRun{}, err
	}
	return b.Store.CreateBindingVerificationRun(ctx, b.AccountID, b.AppID, prepared)
}
func (b VerificationRunBackend) inventory(ctx context.Context, dep string, targets map[string]string) (api.AppBindingInventory, api.BindingReleasePolicy, error) {
	var i api.AppBindingInventory
	var p api.BindingReleasePolicy
	var err error
	if b.InventoryTargets != nil {
		i, p, err = b.InventoryTargets(ctx, dep, targets)
	} else {
		i, p, err = b.Inventory(ctx, dep)
	}
	if err == nil && !maps.Equal(i.ServiceTargets, targets) {
		err = state.ErrConflict
	}
	return i, p, err
}
func (b VerificationRunBackend) Prepare(ctx context.Context, in api.CreateBindingVerificationRunRequest, now, expires time.Time) (state.VerificationRunCreate, error) {
	if in.MaxConcurrency < 0 || in.MaxConcurrency > api.BindingVerificationRunnerMaxConcurrency {
		return state.VerificationRunCreate{}, state.ErrInvalidArgument
	}
	in.MaxConcurrency = api.BindingVerificationConcurrency(in.MaxConcurrency)
	if id, err := uuid.Parse(in.ID); err != nil || id == uuid.Nil || id.String() != in.ID || !expires.After(now) || expires.Sub(now) > time.Duration(api.BindingVerificationRunMaxTTLSeconds)*time.Second {
		return state.VerificationRunCreate{}, state.ErrInvalidArgument
	}
	inventory, policy, err := b.inventory(ctx, in.DeploymentID, in.ServiceTargets)
	if err != nil {
		return state.VerificationRunCreate{}, err
	}
	if !validVerificationRunInventory(inventory, policy, b.App, b.AppID, in.DeploymentID) || bindingcheck.VerificationReviewDigest(inventory, policy) != in.ReviewDigest {
		return state.VerificationRunCreate{}, state.ErrConflict
	}
	plan, _, _, err := bindingcheck.VerificationPlan(inventory, policy, time.Now())
	if err != nil {
		return state.VerificationRunCreate{}, fmt.Errorf("%w: verification plan has blockers", state.ErrConflict)
	}
	report := api.BindingVerificationRun{MaxConcurrency: in.MaxConcurrency, ID: in.ID, App: b.App, DeploymentID: in.DeploymentID, Scope: policy.Scope, Status: "pending", CreatedAt: now, ExpiresAt: expires, ReviewDigest: in.ReviewDigest, Policy: policy, Inventory: inventory, ObjectStorageWrite: in.ObjectStorageWrite, QueuePublish: in.QueuePublish, Steps: []api.BindingVerificationRunStep{}}
	// Review every consent and selector before capturing or admitting any work.
	for _, step := range plan {
		if step.Action == "queue-canary" && !in.QueuePublish || step.Action == "probe" && step.Type == api.BindingTypeObjectStorage && step.ProbeMode != "read" && !in.ObjectStorageWrite {
			return state.VerificationRunCreate{}, state.ErrInvalidArgument
		}
		report.Steps = append(report.Steps, verificationRunStep(step, inventory))
	}
	admissions, err := b.capture(ctx, report)
	if err != nil {
		return state.VerificationRunCreate{}, err
	}
	return state.VerificationRunCreate{Report: report, Admissions: admissions}, nil
}
func validVerificationRunInventory(i api.AppBindingInventory, p api.BindingReleasePolicy, app, appID, dep string) bool {
	return i.App == app && i.Complete && len(i.Issues) == 0 && i.RequestedDeploymentID == dep && i.VerificationDeploymentID == dep && i.VerificationScope == p.Scope && p.AppID == appID && p.Mode == "enforce" && p.Revision > 0 && i.RuntimeFreshness != nil && i.RuntimeFreshness.ConfigChangedAt != nil && !i.RuntimeFreshness.ConfigChangedAt.IsZero() && !i.RuntimeFreshness.ConfigChangedAt.After(time.Now()) && i.RuntimeFreshness.Source == "instance_started_at"
}
func verificationRunStep(plan api.BindingVerificationPlanStep, i api.AppBindingInventory) api.BindingVerificationRunStep {
	step := api.BindingVerificationRunStep{Plan: plan, Status: "planned"}
	for _, item := range i.Bindings {
		if item.Type == plan.Type && item.Name == plan.Name && item.Binding == plan.Binding && item.Scope == plan.Scope {
			if (plan.Action == "wait-queue" || plan.Action == "reuse") && item.QueueCanary != nil {
				step.ResourceID, step.ResourceKind, step.Status = item.QueueCanary.ID, "queue", item.QueueCanary.Status
			}
			if (plan.Action == "wait" || plan.Action == "reuse") && item.Verification != nil && item.Verification.TaskID != "" {
				step.ResourceID, step.ResourceKind, step.Status = item.Verification.TaskID, "task", item.Verification.Result
			}
		}
	}
	return step
}
func (b VerificationRunBackend) capture(ctx context.Context, r api.BindingVerificationRun) ([]state.VerificationRunAdmission, error) {
	out := make([]state.VerificationRunAdmission, len(r.Steps))
	for i, step := range r.Steps {
		if step.Plan.Action == "probe" || step.Plan.Action == "wait" || step.Plan.Action == "reuse" && step.Plan.Type != api.BindingTypeQueue {
			task, err := b.CaptureTask(ctx, r.DeploymentID, step.Plan, r.CreatedAt)
			if err != nil {
				return nil, err
			}
			if task == nil || task.BindingVerification == nil {
				return nil, state.ErrConflict
			}
			out[i].Task = task
		}
	}
	return out, nil
}
func (b VerificationRunBackend) validate(ctx context.Context, r api.BindingVerificationRun) error {
	admissions, err := b.review(ctx, r)
	if err != nil || r.Status == "passed" {
		return err
	}
	return b.Store.CheckBindingVerificationRun(ctx, b.AccountID, b.AppID, r.ID, admissions)
}
func (b VerificationRunBackend) review(ctx context.Context, r api.BindingVerificationRun) ([]state.VerificationRunAdmission, error) {
	for _, step := range r.Steps {
		if step.ResourceID != "" {
			if step.Status == "missing" || step.ResourceKind == "task" && api.AppTaskStatus(step.Status).Terminal() && step.Status != "succeeded" || step.ResourceKind == "queue" && step.Status != "published" && step.Status != "dispatching" && step.Status != "succeeded" {
				return nil, state.ErrConflict
			}
		}
	}
	if r.Status == "expired" || r.Status == "cancelled" {
		return nil, state.ErrConflict
	}
	inventory, policy, err := b.inventory(ctx, r.DeploymentID, r.Inventory.ServiceTargets)
	if err != nil {
		return nil, err
	}
	if !validVerificationRunInventory(inventory, policy, b.App, b.AppID, r.DeploymentID) || bindingcheck.VerificationReviewDigest(inventory, policy) != r.ReviewDigest {
		return nil, state.ErrConflict
	}
	freshPlan, _, _, planErr := bindingcheck.VerificationPlan(inventory, policy, time.Now())
	if planErr != nil {
		return nil, state.ErrConflict
	}
	for _, step := range r.Steps {
		if step.Plan.Action != "reuse" && (step.ResourceID == "" || step.Status != "succeeded") {
			continue
		}
		for _, fresh := range freshPlan {
			if fresh.Type == step.Plan.Type && fresh.Name == step.Plan.Name && fresh.Binding == step.Plan.Binding && fresh.Scope == step.Plan.Scope && fresh.Action != "reuse" {
				return nil, state.ErrConflict
			}
		}
	}
	if r.Status == "passed" {
		report, err := evaluateVerificationRun(inventory, policy)
		if err != nil {
			return nil, err
		}
		if !report.Passed {
			return nil, state.ErrConflict
		}
		return nil, nil
	}
	admissions, err := b.capture(ctx, r)
	if err != nil {
		return nil, err
	}
	return admissions, nil
}
func (b VerificationRunBackend) Status(ctx context.Context, id string) (api.BindingVerificationRun, error) {
	r, err := b.Read(ctx, id)
	if err != nil {
		return r, err
	}
	if r.Status == "cancelled" || r.Status == "expired" {
		return r, nil
	}
	if err = b.validate(ctx, r); err != nil {
		if errors.Is(err, state.ErrConflict) || errors.Is(err, state.ErrNotFound) {
			r.Status, r.Reason = "blocked", "review_changed_or_evidence_expired"
			return r, nil
		}
		return r, err
	}
	return r, nil
}
func (b VerificationRunBackend) Resume(ctx context.Context, id string) (api.BindingVerificationRun, error) {
	r, err := b.Read(ctx, id)
	if err != nil {
		return r, err
	}
	if err = b.validate(ctx, r); err != nil {
		return r, err
	}
	return r, nil
}
func (b VerificationRunBackend) Admit(ctx context.Context, id string, index int) (api.BindingVerificationRunStep, error) {
	r, err := b.Resume(ctx, id)
	if err != nil {
		return api.BindingVerificationRunStep{}, err
	}
	if r.Status != "pending" || index < 0 || index >= len(r.Steps) {
		return api.BindingVerificationRunStep{}, state.ErrConflict
	}
	return b.Store.AdmitBindingVerificationRunStep(ctx, b.AccountID, b.AppID, id, index)
}
func evaluateVerificationRun(i api.AppBindingInventory, p api.BindingReleasePolicy) (bindingcheck.Report, error) {
	age, err := time.ParseDuration(p.MaxVerificationAge)
	if err != nil {
		return bindingcheck.Report{}, err
	}
	return bindingcheck.Evaluate(i, bindingcheck.Policy{App: i.App, Scope: p.Scope, DeploymentID: i.VerificationDeploymentID, MaxVerificationAge: age, RequireApplicationAck: p.RequireApplicationAck, ObjectStorageRequirements: p.ObjectStorageRequirements, QueueRequirements: p.QueueRequirements}, time.Now())
}
func (b VerificationRunBackend) Complete(ctx context.Context, id string) (api.BindingVerificationRun, error) {
	r, err := b.Resume(ctx, id)
	if err != nil {
		return r, err
	}
	for _, step := range r.Steps {
		if (step.Plan.Action == "probe" || step.Plan.Action == "queue-canary") && (step.ResourceID == "" || !step.Owned) {
			return r, fmt.Errorf("%w: reviewed step has no owned admission", state.ErrConflict)
		}
		if step.Owned && step.Status != "succeeded" {
			return r, fmt.Errorf("%w: owned resource did not succeed", state.ErrConflict)
		}
	}
	inventory, policy, err := b.inventory(ctx, r.DeploymentID, r.Inventory.ServiceTargets)
	if err != nil {
		return r, err
	}
	report, err := evaluateVerificationRun(inventory, policy)
	if err != nil {
		return r, err
	}
	if !report.Passed {
		if r.Status == "pending" {
			if pending := pendingQualification(inventory, report); pending != nil {
				if err = b.validate(ctx, r); err != nil {
					return r, err
				}
				if !r.ExpiresAt.After(time.Now()) {
					return r, state.ErrConflict
				}
				r.Qualification = pending
				return r, nil
			}
		}
		return r, fmt.Errorf("%w: final binding preflight is blocked", state.ErrConflict)
	}
	if err = b.Store.SetBindingVerificationRunStatus(ctx, b.AccountID, b.AppID, id, "passed"); err != nil {
		return r, err
	}
	return b.Status(ctx, id)
}
func (b VerificationRunBackend) Cancel(ctx context.Context, id string) (api.BindingVerificationRun, error) {
	if err := b.Store.SetBindingVerificationRunStatus(ctx, b.AccountID, b.AppID, id, "cancelled"); err != nil {
		return api.BindingVerificationRun{}, err
	}
	r, err := b.Read(ctx, id)
	if err != nil {
		return r, err
	}
	for _, step := range r.Steps {
		if !step.Owned || step.ResourceID == "" {
			continue
		}
		if step.ResourceKind == "task" && !api.AppTaskStatus(step.Status).Terminal() {
			_, err = b.Tasks.RequestAppTaskCancellation(ctx, b.AccountID, b.AppID, step.ResourceID, time.Now().UTC())
		}
		if step.ResourceKind == "queue" && step.Status != "succeeded" && step.Status != "cancelled" && step.Status != "missing" {
			_, err = b.Canaries.CancelQueueBindingCanary(ctx, b.AccountID, b.AppID, step.ResourceID)
		}
		if err != nil && !errors.Is(err, state.ErrNotFound) {
			return r, err
		}
	}
	return b.Read(ctx, id)
}
func VerificationRunProblem(err error) *api.Problem {
	var budget *state.VerificationRunBudgetError
	if errors.As(err, &budget) {
		return api.NewProblem(http.StatusTooManyRequests, "binding_verification_budget_full", "Verification budget full", "Observe the original run and retry admission after unfinished resources settle.").WithLimit(int64(budget.Limit), int64(budget.Observed)).WithDocs("https://gregale.dev/docs/networking")
	}
	var capacity *state.VerificationRunCapacityError
	if errors.As(err, &capacity) {
		return api.NewProblem(http.StatusTooManyRequests, "binding_verification_run_limit", "Verification run limit reached", "Cancel an unfinished run or wait for its original deadline.").WithLimit(int64(capacity.Limit), int64(capacity.Observed)).WithDocs("https://gregale.dev/docs/networking")
	}
	var queueCapacity *state.QueueCanaryCapacityError
	if errors.As(err, &queueCapacity) {
		return QueueCanaryProblem(err)
	}
	var existing *api.Problem
	if errors.As(err, &existing) {
		return existing
	}
	switch {
	case err == nil:
		return nil
	case errors.Is(err, state.ErrInvalidArgument) || errors.Is(err, state.ErrAppTaskInvalid):
		return api.ErrValidation("invalid verification run or missing write/publication consent")
	case errors.Is(err, state.ErrNotFound):
		return api.NewProblem(http.StatusNotFound, api.CodeNotFound, "Verification run unavailable", "No owned run or eligible deployment was found.")
	case errors.Is(err, state.ErrConflict):
		return api.NewProblem(http.StatusConflict, "binding_verification_run_changed", "Verification run blocked", "The reviewed plan, policy, configuration, deadline or evidence changed; inspect status and review a new run.")
	case errors.Is(err, state.ErrQuotaExceeded):
		return api.ErrCapacity("verification run admission is at its normal capacity limit")
	default:
		return api.ErrCapacity("verification run metadata or admission is unavailable")
	}
}

// A cancellation or create replay can be authorized with app deployment access
// while domain catalog access has since been removed. Keep that catalog hidden.
func RedactVerificationRun(r api.BindingVerificationRun, readable func(string) bool) api.BindingVerificationRun {
	items := make([]api.AppBindingInventoryItem, 0, len(r.Inventory.Bindings))
	for _, item := range r.Inventory.Bindings {
		if readable(item.Type) {
			items = append(items, item)
		} else {
			r.Inventory.Complete = false
		}
	}
	r.Inventory.Bindings = items
	steps := append([]api.BindingVerificationRunStep(nil), r.Steps...)
	for i := range steps {
		if !readable(steps[i].Plan.Type) {
			steps[i].Plan.Name = ""
			r.Inventory.Complete = false
		}
	}
	r.Steps = steps
	return r
}
