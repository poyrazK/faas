package sched

import (
	"context"
	"errors"
	"time"

	"github.com/onebox-faas/faas/pkg/financial"
	"github.com/onebox-faas/faas/pkg/state"
)

// strictComputePreload is the reaper tick's bulk read of what the strict
// compute sweep checks (ADR 20261102160622): every owned app's live instances
// and the latest compute lease of each resident one, two queries for the tick
// instead of a live listing and a lease read per instance for every app. It
// only rules instances out: before the sweep stops one it re-reads the lease
// and the row under the app lock, as the per-app read did.
type strictComputePreload struct {
	rows   map[string][]state.Instance
	leases map[string]financial.ComputeLease
}

// loadStrictComputePreload reads the tick's preload for apps, or returns nil
// when the store cannot (the sweep then reads per app).
func (e *Engine) loadStrictComputePreload(ctx context.Context, apps []state.App) *strictComputePreload {
	if _, ok := e.store.(state.FinancialComputeStore); !ok || len(apps) == 0 {
		return nil
	}
	lister, ok := e.store.(state.LiveInstancesForAppsReader)
	if !ok {
		return nil
	}
	leaseReader, ok := e.store.(state.FinancialComputeInstanceLeasesReader)
	if !ok {
		return nil
	}
	ids := make([]string, 0, len(apps))
	accounts := make(map[string]string, len(apps))
	for _, app := range apps {
		ids = append(ids, app.ID)
		accounts[app.ID] = app.AccountID
	}
	rows, err := lister.ListLiveInstancesForApps(ctx, ids)
	if err != nil {
		e.log.Warn("sched: strict compute sweep: bulk live instances; reading per app", "err", err)
		return nil
	}
	pre := &strictComputePreload{rows: make(map[string][]state.Instance, len(apps))}
	var keys []state.FinancialComputeInstanceKey
	for _, ins := range rows {
		pre.rows[ins.AppID] = append(pre.rows[ins.AppID], ins)
		if strictComputeLeaseChecked(ins) {
			keys = append(keys, state.FinancialComputeInstanceKey{AccountID: accounts[ins.AppID], AppID: ins.AppID, InstanceID: ins.ID})
		}
	}
	if pre.leases, err = leaseReader.FinancialComputeLatestInstanceLeases(ctx, keys); err != nil {
		e.log.Warn("sched: strict compute sweep: bulk leases; reading per app", "err", err)
		return nil
	}
	return pre
}

// strictComputeLeaseChecked reports whether the sweep reads an instance's
// lease: a resident instance past its boot.
func strictComputeLeaseChecked(ins state.Instance) bool {
	st := state.State(ins.State)
	return st.CountsForRAM() && st != state.StateWaking && st != state.StateColdBooting
}

// strictLeaseRetires reports whether an instance's latest lease ends its
// compute: a strict lease that settled or ran out.
func strictLeaseRetires(lease financial.ComputeLease, known bool, now time.Time) bool {
	return known && lease.Strict && (lease.SettledAt != nil || !now.Before(lease.RunUntil))
}

// The scheduler reflects a monetary hold through its existing instance owner.
// vmmd's local deadline is independently authoritative during control-plane loss.
func (e *Engine) reconcileStrictCompute(ctx context.Context, app state.App, now time.Time, pre *strictComputePreload) error {
	compute, ok := e.store.(state.FinancialComputeStore)
	if !ok {
		return nil
	}
	policies, err := compute.FinancialComputePolicies(ctx, app.AccountID, app.ID)
	if err != nil {
		return err
	}
	// A held environment cap retires only its environment's instances.
	held, heldEnvironments := false, map[string]bool{}
	for _, p := range policies {
		if !p.Spec.Enabled || !financial.StrictCompute(p.Spec) {
			continue
		}
		balance, err := compute.FinancialComputeBalance(ctx, p, now)
		if err != nil {
			return err
		}
		if balance.Held && p.Spec.Scope.Kind == "environment" {
			heldEnvironments[p.Spec.Scope.ID] = true
			continue
		}
		held = held || balance.Held
	}
	var rows []state.Instance
	if pre != nil {
		rows = pre.rows[app.ID]
	} else if rows, err = e.liveInstancesForApp(ctx, app.ID); err != nil {
		return err
	}
	var failures []error
	environments := map[string]string{}
	for _, ins := range rows {
		if !state.State(ins.State).CountsForRAM() {
			continue
		}
		retire := held
		if !retire && len(heldEnvironments) > 0 && ins.DeploymentID != "" {
			environment, known := environments[ins.DeploymentID]
			if !known {
				environment, err = compute.FinancialComputeDeploymentEnvironment(ctx, app.AccountID, app.ID, ins.DeploymentID)
				if errors.Is(err, state.ErrNotFound) {
					environment, err = "", nil
				}
				if err != nil {
					failures = append(failures, err)
					continue
				}
				environments[ins.DeploymentID] = environment
			}
			retire = heldEnvironments[environment]
		}
		if !retire && strictComputeLeaseChecked(ins) {
			// The tick's lease can only rule the instance out; a renewal
			// since then must still keep it, so a stop re-reads the lease.
			if pre != nil {
				if lease, known := pre.leases[ins.ID]; !strictLeaseRetires(lease, known, now) {
					continue
				}
			}
			lease, known, err := compute.FinancialComputeInstanceLease(ctx, app.AccountID, app.ID, ins.ID)
			if err != nil {
				failures = append(failures, err)
				continue
			}
			retire = strictLeaseRetires(lease, known, now)
		}
		if !retire {
			continue
		}
		if pre != nil {
			// The row is the tick's: stop only what is still resident,
			// read under the app lock.
			fresh, err := e.store.InstanceByID(ctx, ins.ID)
			if errors.Is(err, state.ErrNotFound) {
				continue
			}
			if err != nil {
				failures = append(failures, err)
				continue
			}
			if fresh.AppID != app.ID || !state.State(fresh.State).CountsForRAM() {
				continue
			}
			ins = fresh
		}
		if err := e.retireFinancialBudgetRuntime(ctx, ins); err != nil {
			failures = append(failures, err)
		}
	}
	return errors.Join(failures...)
}
