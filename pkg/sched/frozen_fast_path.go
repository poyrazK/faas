// adr: 1740, 1741 — a ticketed thaw is a ledger check, the vmmd call and
// one update guarded by the app's runtime generation.
package sched

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// EnableFrozenFastPath thaws a frozen VM from the ticket recorded when it
// froze (schedd `frozen_fast_path`, off by default; ADR-1740). Turning it
// off returns every thaw to ADR-1151's path at once.
func (e *Engine) EnableFrozenFastPath() { e.frozenFastPath.Store(true) }

// frozenTicket is what a thaw needs, recorded when the VM froze: the plan,
// the config fence its proof captured, the app's runtime generation read
// before everything else (ADR-1741) and the frozen charge to restore when
// the thaw is refused.
type frozenTicket struct {
	plan       frozenThawPlan
	fence      state.RuntimeAppConfigFence
	generation int64
	chargeMB   int
}

// frozenTickets holds the tickets of this schedd's frozen VMs, by app. They
// live only in memory: a restart loses them and thaws take ADR-1151's path.
type frozenTickets struct {
	mu    sync.Mutex
	byApp map[string]map[string]frozenTicket
}

func (t *frozenTickets) put(ticket frozenTicket) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.byApp == nil {
		t.byApp = map[string]map[string]frozenTicket{}
	}
	appID := ticket.plan.app.ID
	if t.byApp[appID] == nil {
		t.byApp[appID] = map[string]frozenTicket{}
	}
	t.byApp[appID][ticket.plan.frozen.ID] = ticket
}

func (t *frozenTickets) drop(instanceID string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	for appID, tickets := range t.byApp {
		if _, ok := tickets[instanceID]; ok {
			delete(tickets, instanceID)
			if len(tickets) == 0 {
				delete(t.byApp, appID)
			}
			return
		}
	}
}

// take removes and returns the app's ticket for preferred when it has one,
// else its most recently used, among VMs the ledger still holds frozen.
// Tickets of VMs that left the frozen state some other way are dropped on
// the way.
func (t *frozenTickets) take(appID, preferred string, frozen func(instanceID string) bool) (frozenTicket, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	var best frozenTicket
	found := false
	for id, ticket := range t.byApp[appID] {
		if !frozen(id) {
			delete(t.byApp[appID], id)
			continue
		}
		a, b := ticket.plan.frozen, best.plan.frozen
		if !found || b.ID != preferred && (a.ID == preferred || a.LastRequestAt.After(b.LastRequestAt) ||
			a.LastRequestAt.Equal(b.LastRequestAt) && a.ID < b.ID) {
			best, found = ticket, true
		}
	}
	if found {
		delete(t.byApp[appID], best.plan.frozen.ID)
	}
	if len(t.byApp[appID]) == 0 {
		delete(t.byApp, appID)
	}
	return best, found
}

var errNoThawTicket = errors.New("sched: frozen VM gets no thaw ticket")

// issueThawTicket records the ticket for a VM that just froze. The caller
// holds the app lock. A VM without one thaws through ADR-1151's path.
func (e *Engine) issueThawTicket(ctx context.Context, ins state.Instance, chargeMB int) {
	ticket, err := e.thawTicketFor(ctx, ins, chargeMB)
	if err != nil {
		e.log.Debug("sched: frozen fast path: no ticket", "instance", ins.ID, "err", err)
		return
	}
	e.thawTickets.put(ticket)
}

// thawTicketFor reads, off the request path, everything the ordinary wake
// path would read to thaw ins, and refuses a ticket wherever ADR-1740's
// guards could not stand in for those reads.
func (e *Engine) thawTicketFor(ctx context.Context, ins state.Instance, chargeMB int) (frozenTicket, error) {
	guards, ok := e.store.(state.FrozenThawStore)
	if !ok || (ins.Mode != "" && ins.Mode != string(state.InstanceModeNormal)) {
		return frozenTicket{}, errNoThawTicket
	}
	// The generation is read before everything else: a change after it moves
	// the generation the guarded thaw compares, and a change before it shows
	// in the reads below.
	basis, err := guards.FrozenThawBasis(ctx, ins.AppID, ins.DeploymentID)
	if err != nil {
		return frozenTicket{}, err
	}
	if !basis.Selected || basis.Generation <= 0 {
		return frozenTicket{}, errNoThawTicket
	}
	// An operator cold boot (ADR-1523) advances the deployment's recovery
	// generation, a deployments column, so one after the read above moves the
	// generation the guarded thaw compares. One before it shows here: the
	// frozen VM predates the recovery and must cold boot, not thaw.
	if fresh, err := e.snapshotRecoveryFresh(ctx, ins.ID); err != nil {
		return frozenTicket{}, err
	} else if !fresh {
		return frozenTicket{}, errNoThawTicket
	}
	// The ticket stands in for the wake's workload-retirement gate; a
	// retirement after this moves the generation the guarded thaw checks.
	if err := state.CheckEnvironmentWorkloadAdmission(ctx, e.store, ins.AppID, ""); err != nil {
		return frozenTicket{}, err
	}
	app, err := e.store.AppByID(ctx, ins.AppID)
	if err != nil {
		return frozenTicket{}, err
	}
	// The application-standards rule (ADR-595) is evaluated here, in Go;
	// the guarded thaw then requires the enrollment row unchanged. A VM
	// whose admission captured managed standards publishes only with a
	// receipt, so it keeps the ordinary path.
	if app.Status != state.AppActive {
		return frozenTicket{}, errNoThawTicket
	}
	if err := e.checkApplicationStandardAdmission(ctx, app); err != nil {
		return frozenTicket{}, err
	}
	if captures, ok := e.store.(state.InstanceApplicationStandardAdmissionStore); ok && app.OrgID != "" {
		capture, err := captures.GetInstanceApplicationStandardAdmission(ctx, ins.ID)
		if err != nil && !errors.Is(err, state.ErrNotFound) {
			return frozenTicket{}, err
		}
		if err == nil && capture.Managed {
			return frozenTicket{}, errNoThawTicket
		}
	}
	acct, err := e.store.AccountByID(ctx, app.AccountID)
	if err != nil {
		return frozenTicket{}, err
	}
	limits, ok := api.LimitsFor(acct.Plan)
	if !acct.Active() || !ok {
		return frozenTicket{}, errNoThawTicket
	}
	dep, err := e.store.DeploymentByID(ctx, ins.DeploymentID)
	if err != nil {
		return frozenTicket{}, err
	}
	resolved, err := state.ResolveAppForDeployment(ctx, e.store, app, dep)
	if err != nil {
		return frozenTicket{}, err
	}
	if ins.RAMMB != resolved.RAMMB || !instanceModeMatchesApp(resolved, ins) || securityQuarantineErr(dep) != nil {
		return frozenTicket{}, errNoThawTicket
	}
	scaling, err := e.runtimeScalingStateForDeployment(ctx, resolved, dep)
	if err != nil {
		return frozenTicket{}, err
	}
	values, err := e.loadRuntimeDeploymentValues(ctx, resolved, dep)
	if err != nil {
		return frozenTicket{}, err
	}
	captured, err := e.store.InstanceRuntimeConfigFence(ctx, acct.ID, app.ID, ins.ID)
	if err != nil {
		return frozenTicket{}, err
	}
	if captured != values.ConfigFence || runtimeValuesHaveEphemeralSecrets(values.Snapshot) {
		return frozenTicket{}, errNoThawTicket
	}
	plan := frozenThawPlan{frozen: ins, app: resolved, acct: acct, limits: limits, dep: dep,
		environmentKey: runtimeEnvironmentAdmissionKey(scaling.Scope, scaling.EnvironmentID),
		production:     reaperProductionScope(scaling.Scope)}
	plan.frozen.State = string(state.StateFrozen)
	if node, nodeErr := e.store.ComputeNodeByID(ctx, ins.NodeID); nodeErr == nil {
		plan.node = nodeAdmissionLimits{ceilingMB: node.AdmissionCeilingMB, vcpuBudget: node.VCPUBudget,
			cpuBudgetMillicores: int(cpuBudgetMillicores(node))}
		plan.region = stringValue(node.Region)
	} else {
		return frozenTicket{}, fmt.Errorf("sched: frozen fast path: node: %w", nodeErr)
	}
	return frozenTicket{plan: plan, fence: captured, generation: basis.Generation, chargeMB: chargeMB}, nil
}

// thawFromTicketLocked is wake phase 1's fast path (ADR-1740). It runs
// under the app lock before any read and reports handled=false whenever the
// ordinary path must decide: no ticket, a running instance phase 1 would
// return, a capacity refusal, or a thaw the guards refused (the VM is then
// frozen again, or parked).
func (e *Engine) thawFromTicketLocked(ctx context.Context, appID, deploymentID, scope string, wakeStarted time.Time) (WakeResult, bool) {
	if !e.frozenFastPath.Load() || deploymentID != "" || scope != "" || e.ledger.Concurrency(appID) > 0 {
		return WakeResult{}, false
	}
	if _, selected := wakeEnvironmentFrom(ctx); selected {
		return WakeResult{}, false
	}
	thawer, ok := e.vmm.(FrozenVMM)
	guards, guardsOK := e.store.(state.FrozenThawStore)
	if !ok || !guardsOK {
		return WakeResult{}, false
	}
	ticket, ok := e.thawTickets.take(appID, PreferredInstanceFromContext(ctx), e.ledger.IsFrozen)
	if !ok {
		return WakeResult{}, false
	}
	plan, timing := ticket.plan, thawTiming{wakeStarted: wakeStarted, thawTried: time.Now()}
	if _, _, refused := e.wakeServingCapacity(plan.app, plan.limits, plan.dep.ID, plan.environmentKey, plan.production).refusal(); refused {
		e.thawTickets.put(ticket)
		return WakeResult{}, false
	}
	if !e.readmitFrozenLocked(ctx, plan) {
		return WakeResult{}, false
	}
	if !e.thawFrozenVM(ctx, thawer, plan, &timing) {
		return WakeResult{}, false
	}
	bookkeeping := e.publicationBookkeeping(plan.frozen.ID, plan.app.ID, plan.frozen.WakeID, state.StateFrozen, state.StateRunning, "frozen_thaw")
	fresh, err := guards.ThawFrozenInstance(ctx, state.FrozenThaw{
		AccountID: plan.acct.ID, AppID: plan.app.ID, InstanceID: plan.frozen.ID, NodeID: plan.frozen.NodeID, WakeID: plan.frozen.WakeID,
		Fence: ticket.fence, Generation: ticket.generation, Bookkeeping: bookkeeping,
	})
	timing.published = time.Now()
	switch {
	case errors.Is(err, state.ErrConflict):
		e.refreezeLocked(ctx, thawer, ticket, "guard")
		return WakeResult{}, false
	case err != nil:
		e.log.Warn("sched: frozen fast path: publish RUNNING", "instance", plan.frozen.ID, "err", err)
		e.abandonThawPublication(ctx, plan.frozen)
		return WakeResult{}, false
	}
	return e.answerThaw(ctx, plan, fresh, bookkeeping, timing, "fast"), true
}

// refreezeLocked returns a VM the fast path thawed but may not publish to
// the frozen state, so the ordinary wake path decides with fresh reads. A VM
// that cannot be frozen again is parked.
func (e *Engine) refreezeLocked(ctx context.Context, thawer FrozenVMM, ticket frozenTicket, why string) {
	frozen := ticket.plan.frozen
	e.observeThaw("fast_refused")
	freezeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), frozenRPCTimeout)
	_, err := thawer.FreezeInstance(freezeCtx, e.nodeForRoute(frozen.NodeID), frozen.ID, false)
	cancel()
	if err != nil || !e.ledger.Freeze(frozen.ID, ticket.chargeMB) {
		e.log.Warn("sched: frozen fast path: refreeze failed; parking", "instance", frozen.ID, "refused_by", why, "err", err)
		e.parkFrozenLocked(ctx, frozen, "thaw_failed")
		return
	}
	e.log.Debug("sched: frozen fast path refused; frozen again", "instance", frozen.ID, "refused_by", why)
}
