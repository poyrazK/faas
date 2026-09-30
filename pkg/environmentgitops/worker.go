// Package environmentgitops coordinates the durable control-plane GitOps
// loop. Resource adapters implement intent observation and execution; the
// worker does not touch VMs or bypass customer-intent ownership.
package environmentgitops

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/environmentsync"
	"github.com/onebox-faas/faas/pkg/state"
)

type Observation = state.EnvironmentGitOpsObservation
type Step = state.EnvironmentGitOpsStep

// Backend must enforce the run's lease and generation inside each intent
// transaction, revalidate the plan before its first write, and persist step
// progress before returning. Returned steps contain no customer values.
type Backend interface {
	Observe(context.Context, state.EnvironmentGitOpsLease, environmentsync.DesiredState) (Observation, error)
	Apply(context.Context, state.EnvironmentGitOpsLease, environmentsync.Plan) ([]Step, error)
}

// EffectRecoverer resumes durable post-commit work before planning. An equal
// intent plan after a crash does not prove the serving fleet has applied it.
type EffectRecoverer interface {
	RecoverEffects(context.Context, state.EnvironmentGitOpsLease) error
}

// RuntimeVerifier proves effective runtime separately from customer intent.
// In report mode it must observe only; enforcement may resume durable work.
type RuntimeVerifier interface {
	VerifyRuntime(context.Context, state.EnvironmentGitOpsLease, environmentsync.Plan) (bool, error)
}

type Worker struct {
	Store         state.EnvironmentGitOpsStore
	Backend       Backend
	Log           *slog.Logger
	Now           func() time.Time
	LeaseDuration time.Duration
	CheckInterval time.Duration
	RetryInterval time.Duration
}

func (w *Worker) clock() time.Time {
	if w.Now != nil {
		return w.Now().UTC()
	}
	return time.Now().UTC()
}

func (w *Worker) validate() error {
	if w.Store == nil || w.Backend == nil || w.LeaseDuration <= 0 || w.CheckInterval <= 0 || w.RetryInterval <= 0 {
		return fmt.Errorf("environment GitOps worker requires store, backend, and positive lease/check/retry durations")
	}
	return nil
}

// RunOnce processes at most one claimed source. It deliberately uses the
// persisted approved definition so a source outage does not discard authority.
func (w *Worker) RunOnce(ctx context.Context) (bool, error) {
	if err := w.validate(); err != nil {
		return false, err
	}
	lease, err := w.Store.ClaimEnvironmentGitOps(ctx, uuid.NewString(), w.clock(), w.LeaseDuration)
	if errors.Is(err, state.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	runCtx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	stopHeartbeat := w.heartbeat(runCtx, cancel, lease)
	status, plan, steps, code := w.reconcile(runCtx, lease)
	stopHeartbeat()
	cause := context.Cause(runCtx)
	if cause != nil {
		// Cancellation or lost authority leaves the durable job for recovery.
		// A stopped worker must not publish success from an unfenced result.
		if errors.Is(cause, state.ErrConflict) {
			return true, nil
		}
		return true, cause
	}
	rawPlan, _ := json.Marshal(plan)
	rawSteps, _ := json.Marshal(steps)
	now := w.clock()
	next := now.Add(w.CheckInterval)
	if code != "" {
		next = now.Add(w.RetryInterval)
	}
	err = w.Store.FinishEnvironmentGitOps(ctx, lease, status, rawPlan, rawSteps, code, now, next)
	if errors.Is(err, state.ErrConflict) {
		return true, nil // a newer approved generation owns the next attempt
	}
	return true, err
}

func (w *Worker) heartbeat(ctx context.Context, cancel context.CancelCauseFunc, lease state.EnvironmentGitOpsLease) func() {
	heartbeatCtx, stop := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		interval := max(time.Nanosecond, w.LeaseDuration/3)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-heartbeatCtx.Done():
				return
			case <-ticker.C:
				if err := w.Store.RenewEnvironmentGitOps(heartbeatCtx, lease, w.clock(), w.LeaseDuration); err != nil {
					if heartbeatCtx.Err() == nil {
						cancel(err)
					}
					return
				}
			}
		}
	}()
	return func() { stop(); <-done }
}

func (w *Worker) reconcile(ctx context.Context, lease state.EnvironmentGitOpsLease) (string, environmentsync.Plan, []Step, string) {
	steps := []Step{}
	var definition api.EnvironmentDefinition
	if json.Unmarshal(lease.Revision.Definition, &definition) != nil {
		return "failed", environmentsync.Plan{}, steps, "environment_definition_invalid"
	}
	desired, err := environmentsync.Compile(definition)
	if err != nil || desired.Digest != lease.Revision.Digest {
		return "failed", environmentsync.Plan{}, steps, "environment_definition_invalid"
	}
	if recoverer, ok := w.Backend.(EffectRecoverer); ok {
		if err := recoverer.RecoverEffects(ctx, lease); err != nil {
			return "partial", environmentsync.Plan{}, steps, "environment_fleet_unacknowledged"
		}
	}
	plan, err := w.observePlan(ctx, lease, desired)
	if err != nil {
		return "failed", environmentsync.Plan{}, steps, "environment_observation_failed"
	}
	if !plan.CanApply() {
		return "blocked", plan, steps, ""
	}
	if lease.Source.Spec.Mode == "report" {
		status, code := w.runtimeStatus(ctx, lease, plan)
		return status, plan, steps, code
	}
	if plan.HasDrift() {
		steps, err = w.Backend.Apply(ctx, lease, plan)
		if steps == nil {
			steps = []Step{}
		}
		if err != nil {
			return "partial", plan, steps, "environment_apply_failed"
		}
		// An executor's success is insufficient: reread actual intent and
		// compare it with the same approved revision before publishing applied.
		plan, err = w.observePlan(ctx, lease, desired)
		if err != nil {
			return "partial", plan, steps, "environment_verification_failed"
		}
	}
	if !plan.CanApply() {
		return "blocked", plan, steps, ""
	}
	status, code := w.runtimeStatus(ctx, lease, plan)
	return status, plan, steps, code
}

func (w *Worker) runtimeStatus(ctx context.Context, lease state.EnvironmentGitOpsLease, plan environmentsync.Plan) (string, string) {
	status := convergenceStatus(plan)
	verifier, ok := w.Backend.(RuntimeVerifier)
	if status != "converged" || !ok {
		return status, ""
	}
	ready, err := verifier.VerifyRuntime(ctx, lease, plan)
	if err != nil {
		return "partial", "environment_runtime_verification_failed"
	}
	if !ready {
		if lease.Source.Spec.Mode == "report" {
			return "drifted", "environment_runtime_unacknowledged"
		}
		return "partial", "environment_runtime_unacknowledged"
	}
	return status, ""
}

func (w *Worker) observePlan(ctx context.Context, lease state.EnvironmentGitOpsLease, desired environmentsync.DesiredState) (environmentsync.Plan, error) {
	observation, err := w.Backend.Observe(ctx, lease, desired)
	if err != nil {
		return environmentsync.Plan{}, err
	}
	return environmentsync.BuildPlan(desired, observation.State, observation.Owners, environmentsync.PlanOptions{
		Manager: lease.Source.ID, Revision: lease.Revision.ID, CommitSHA: lease.Revision.CommitSHA, Generation: lease.Source.Generation,
		Prune: lease.Source.Spec.Prune, Now: w.clock(), Overrides: observation.Overrides,
	})
}

func convergenceStatus(plan environmentsync.Plan) string {
	for _, change := range plan.Changes {
		if change.Action == "overridden" {
			return "overridden"
		}
	}
	if plan.HasDrift() {
		return "drifted"
	}
	return "converged"
}

func (w *Worker) Run(ctx context.Context, pollInterval time.Duration) error {
	if err := w.validate(); err != nil || pollInterval <= 0 {
		return fmt.Errorf("invalid environment GitOps worker configuration")
	}
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()
	for {
		worked, err := w.RunOnce(ctx)
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err != nil && w.Log != nil {
			// Stable diagnostics only: backend errors may contain customer data.
			w.Log.Warn("environment GitOps attempt could not complete", "error_code", "environment_reconcile_failed")
		}
		if worked && err == nil {
			continue
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}
