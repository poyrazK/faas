package sched

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/financial"
	"github.com/onebox-faas/faas/pkg/state"
)

func (e *Engine) reconcileFinancialPreviewCommands(ctx context.Context, app state.App, stop state.FinancialBudgetStop, now time.Time, target *financial.BudgetStopTarget) error {
	store, ok := e.store.(state.FinancialPreviewCommandStore)
	if !ok {
		target.State, target.LastError = "failed", "runtime_identity_unavailable"
		return fmt.Errorf("preview command ownership store unavailable")
	}
	commands, err := store.FinancialPreviewCommands(ctx, app.AccountID, app.ID)
	if err != nil {
		target.State, target.LastError = "failed", "runtime_identity_unavailable"
		return err
	}
	var failures []error
	for _, command := range commands {
		if target.EnvironmentID != "" && command.EnvironmentID != target.EnvironmentID {
			matches, err := state.MatchFinancialBudgetEnvironment(ctx, e.store, app, target.EnvironmentID, command.DeploymentID)
			if err != nil {
				target.ResidentCount++
				target.State, target.LastError = "failed", "runtime_identity_unavailable"
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
		if command.NodeID == "" {
			target.ResidentCount++
			target.State, target.LastError = "failed", "runtime_identity_unavailable"
			continue
		}
		if err := e.timedDestroy(ctx, command.NodeID, command.TaskID, DestroyTimeout); err != nil {
			target.ResidentCount++
			target.State, target.LastError = "failed", "vm_teardown_failed"
			failures = append(failures, err)
			continue
		}
		// A destroy that beats preparation cannot prove the in-flight RPC
		// will not create a VM later. Retain its reservation until the
		// persisted RPC deadline or a retained success reply.
		if command.Phase == "preparing" && now.Before(command.PrepareUntil.Add(api.FinancialBudgetCommandClockGrace)) {
			target.ResidentCount++
			target.State, target.LastError = "failed", "runtime_identity_unavailable"
			continue
		}
		if err := store.RetireFinancialPreviewCommand(ctx, command.TaskID, command.LeaseToken, now); err != nil {
			target.ResidentCount++
			target.State, target.LastError = "failed", "instance_settlement_failed"
			failures = append(failures, err)
			continue
		}
		e.ledger.Release(command.TaskID)
	}
	return errors.Join(failures...)
}

func (e *Engine) seedFinancialPreviewCommandLedger(ctx context.Context, apps []state.App) error {
	store, ok := e.store.(state.FinancialPreviewCommandStore)
	if !ok {
		return nil
	}
	for _, app := range apps {
		if app.PreviewOfSlug == "" || !e.ownsApp(app) {
			continue
		}
		commands, err := store.FinancialPreviewCommands(ctx, app.AccountID, app.ID)
		if err != nil {
			return err
		}
		account, err := e.store.AccountByID(ctx, app.AccountID)
		if err != nil {
			return err
		}
		limits := api.MustLimitsFor(account.Plan)
		for _, command := range commands {
			if command.NodeID == "" {
				continue // Legacy unknown ownership remains explicit in stop status.
			}
			if err := e.ledger.Admit(Request{Instance: command.TaskID, AppID: app.ID, DeploymentID: command.DeploymentID, Plan: account.Plan,
				RAMMB: command.RAMMB, VCPU: limits.VCPU, CPUMillicores: command.CPUMillicores, MaxConcurrency: app.MaxConcurrency,
				NodeID: command.NodeID, Kind: KindAppTask, AllowConcurrencyRecovery: true, AllowCPUOvercommitRecovery: true}); err != nil {
				return fmt.Errorf("recover preview command capacity: %w", err)
			}
		}
	}
	return nil
}
