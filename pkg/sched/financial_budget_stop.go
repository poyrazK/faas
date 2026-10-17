package sched

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/onebox-faas/faas/pkg/financial"
	"github.com/onebox-faas/faas/pkg/state"
)

func (e *Engine) financialBudgetNow() time.Time {
	if e.now != nil {
		return e.now().UTC()
	}
	return time.Now().UTC()
}

func (e *Engine) checkFinancialBudgetAdmission(ctx context.Context, app state.App, deploymentID string) error {
	return state.CheckFinancialBudgetAdmission(ctx, e.store, app, deploymentID, e.financialBudgetNow())
}

func (e *Engine) checkFinancialBudgetPublication(ctx context.Context, p state.RuntimeInstancePublication) error {
	if _, ok := e.store.(state.FinancialBudgetStopStore); !ok {
		return nil
	}
	app, err := e.store.AppByID(ctx, p.AppID)
	if err != nil {
		return err
	}
	return e.checkFinancialBudgetAdmission(ctx, app, p.Fence.DeploymentID)
}

// Do not return provisional capacity to the admission ledger on an uncertain
// destroy. The durable sweep will retry the still-resident instance record.
func (e *Engine) retireFinancialBudgetRuntime(ctx context.Context, ins state.Instance) error {
	cleanup := context.WithoutCancel(ctx)
	if err := e.timedDestroy(cleanup, ins.NodeID, ins.ID, DestroyTimeout); err != nil {
		return err
	}
	if _, err := e.transitionWithKindCAS(cleanup, ins.ID, ins.AppID, state.StateStopped, "financial_budget_stop", "admission_revoked_during_boot"); err != nil {
		return err
	}
	fresh, err := e.store.InstanceByID(cleanup, ins.ID)
	if err != nil {
		return err
	}
	if state.State(fresh.State).CountsForRAM() {
		return fmt.Errorf("budget-rejected runtime %s remains resident", ins.ID)
	}
	e.ledger.Release(ins.ID)
	return nil
}

// ReconcileFinancialBudgetStops runs on the owning scheduler's durable sweep.
// A missed notification or restarted scheduler cannot lose the stop. New work
// is refused immediately; existing work gets the configured drain interval.
func (e *Engine) ReconcileFinancialBudgetStops(ctx context.Context, appID string) error {
	store, ok := e.store.(state.FinancialBudgetStopStore)
	if !ok {
		return nil
	}
	release := e.lockApp(appID)
	defer release()
	app, err := e.store.AppByID(ctx, appID)
	if err != nil || !e.ownsApp(app) {
		return err
	}
	now := e.financialBudgetNow()
	if err := e.reconcileStrictCompute(ctx, app, now); err != nil {
		return err
	}
	if app.PreviewOfSlug == "" {
		return nil
	}
	stops, err := store.ActiveFinancialBudgetStops(ctx, app.AccountID, now)
	if err != nil || len(stops) == 0 {
		return err
	}
	var failures []error
	for _, stop := range stops {
		if err := e.reconcileFinancialBudgetStop(ctx, store, app, stop, now); err != nil {
			failures = append(failures, err)
		}
	}
	return errors.Join(failures...)
}

func (e *Engine) reconcileFinancialBudgetStop(ctx context.Context, store state.FinancialBudgetStopStore, app state.App, stop state.FinancialBudgetStop, now time.Time) error {
	environment := ""
	if stop.Spec.Scope.Kind == "environment" {
		reader, ok := e.store.(interface {
			ProjectEnvironmentByID(context.Context, string) (state.ProjectEnvironment, error)
		})
		if !ok {
			return fmt.Errorf("budget environment reader unavailable")
		}
		env, err := reader.ProjectEnvironmentByID(ctx, stop.Spec.Scope.ID)
		if err != nil {
			return err
		}
		if env.AccountID != app.AccountID || env.ProjectID != app.ProjectID {
			return nil
		}
		environment = env.ID
	}
	if !stop.Matches(app, environment) {
		return nil
	}
	target := financial.BudgetStopTarget{AppID: app.ID, EnvironmentID: environment, State: "requested", ObservedAt: now}
	instances, err := e.store.ListInstancesForApp(ctx, app.ID)
	if err != nil {
		return err
	}
	var failures []error
	for _, candidate := range instances {
		if !state.State(candidate.State).CountsForRAM() {
			continue
		}
		if environment != "" {
			matches, err := state.MatchFinancialBudgetEnvironment(ctx, e.store, app, environment, candidate.DeploymentID)
			if err != nil {
				target.State, target.LastError = "failed", "runtime_identity_unavailable"
				target.ResidentCount++
				failures = append(failures, err)
				continue
			}
			if !matches {
				continue
			}
		}
		if now.Before(stop.DrainDeadline) {
			target.ResidentCount++
			continue
		}
		// Existing deployment snapshots and rootfs remain cold-bootable. At
		// the deadline destroy the resident process through vmmd; attempting
		// a fresh snapshot here could extend the drain without a bound.
		if err := e.timedDestroy(ctx, candidate.NodeID, candidate.ID, DestroyTimeout); err != nil {
			target.State, target.LastError = "failed", "vm_teardown_failed"
			target.ResidentCount++
			failures = append(failures, err)
			continue
		}
		if _, err := e.transitionWithKindCAS(ctx, candidate.ID, app.ID, state.StateStopped, "financial_budget_stop", stop.ID); err != nil {
			target.State, target.LastError = "failed", "instance_settlement_failed"
			target.ResidentCount++
			failures = append(failures, err)
			continue
		}
		fresh, err := e.store.InstanceByID(ctx, candidate.ID)
		if err != nil || state.State(fresh.State).CountsForRAM() {
			target.State, target.LastError = "failed", "instance_settlement_failed"
			target.ResidentCount++
			failures = append(failures, fmt.Errorf("budget stop instance %s not settled", candidate.ID))
			continue
		}
		e.ledger.Release(candidate.ID)
	}
	if err := e.reconcileFinancialPreviewCommands(ctx, app, stop, now, &target); err != nil {
		failures = append(failures, err)
	}
	if target.State != "failed" {
		target.State = "enforced"
		if target.ResidentCount > 0 {
			target.State = "draining"
		}
	}
	if err := store.RecordFinancialBudgetStopTarget(ctx, app.AccountID, stop.ID, target); err != nil && !errors.Is(err, state.ErrConflict) {
		failures = append(failures, err)
	}
	return errors.Join(failures...)
}
