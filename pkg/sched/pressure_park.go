package sched

// Park-to-admit (ADR-643, production-us hunt #5 H5-32). A wake refused for
// fleet capacity parks one idle instance of another app and retries once.
// Before this, sixteen apps touched within their idle timeout held every
// placement slot on the two-node fleet and every other parked app answered
// 503 until the reaper's idle timeout caught up; RAM-pressure eviction never
// fired because it compares a global ceiling the per-node fleet cannot reach.
//
// The reaper publishes candidates from the same snapshot it reaps from, so
// the exemptions match idle reaping: in-flight requests, open connections,
// log tails, floors, workers, services, jobs and mirrors are never taken.
// Candidates are instances of apps this schedd owns; parking snapshots the
// instance, so its next wake restores (ADR-005).

import (
	"context"
	"errors"
	"sort"
	"sync"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// PressureParkIdle is how long an instance must have been idle before a
// refused wake may park it. It is below every plan's idle timeout: such an
// instance would be parked anyway, only later.
const PressureParkIdle = 30 * time.Second

// pressureParkFreshness bounds the age of the reaper's view a refused wake
// may act on. The reaper ticks every 10 s.
const pressureParkFreshness = 30 * time.Second

// pressureParkAttempts bounds how many candidates one refused wake tries
// when earlier ones turn out to be busy or already gone.
const pressureParkAttempts = 3

// PressureParkCandidate is an idle instance a refused wake may park.
type PressureParkCandidate struct {
	Instance     string
	AppID        string
	NodeID       string
	Plan         api.Plan
	RAMMB        int
	LastActivity time.Time
}

// SelectPressureParkCandidates returns the instances a refused wake may park,
// least recently used first, Scale plan last. Each app keeps its floor.
func SelectPressureParkCandidates(now time.Time, instances []InstanceInfo) []PressureParkCandidate {
	type group struct {
		running, floor int
		cands          []InstanceInfo
	}
	groups := map[string]*group{}
	for _, in := range instances {
		if in.State != state.StateRunning || in.PolicyUnavailable || in.WorkloadClass == state.WorkloadClassWorker {
			continue
		}
		switch state.InstanceMode(in.Mode) {
		case state.InstanceModeMirror, state.InstanceModeWorker, state.InstanceModeService, state.InstanceModeJob:
			continue
		}
		key := reaperEnvironmentKey(in)
		g := groups[key]
		if g == nil {
			g = &group{floor: max(in.MinInstances, in.PrewarmMinInstances)}
			groups[key] = g
		}
		g.running++
		if in.EvictionPriority == string(api.EvictionPriorityReserved) ||
			in.InflightRequests > 0 || in.OpenConns > 0 || in.TailCount > 0 ||
			now.Sub(in.Started) < MinInstanceAge || now.Sub(idleReference(in)) < PressureParkIdle {
			continue
		}
		g.cands = append(g.cands, in)
	}
	var out []PressureParkCandidate
	for _, g := range groups {
		sort.Slice(g.cands, func(a, b int) bool { return idleReference(g.cands[a]).Before(idleReference(g.cands[b])) })
		allowed := max(g.running-g.floor, 0)
		for _, in := range g.cands[:min(allowed, len(g.cands))] {
			out = append(out, PressureParkCandidate{
				Instance: in.Instance, AppID: in.AppID, NodeID: in.NodeID,
				Plan: in.Plan, RAMMB: in.RAMMB, LastActivity: idleReference(in),
			})
		}
	}
	sort.Slice(out, func(a, b int) bool {
		as, bs := out[a].Plan == api.PlanScale, out[b].Plan == api.PlanScale
		if as != bs {
			return !as
		}
		if !out[a].LastActivity.Equal(out[b].LastActivity) {
			return out[a].LastActivity.Before(out[b].LastActivity)
		}
		return out[a].Instance < out[b].Instance
	})
	return out
}

// pressureParkView is the reaper's latest candidate list plus the instances
// refused wakes have already taken from it.
type pressureParkView struct {
	mu      sync.Mutex
	at      time.Time
	cands   []PressureParkCandidate
	claimed map[string]struct{}
}

// publishPressureParkCandidates replaces the candidate list. The reaper calls
// it once per tick.
func (e *Engine) publishPressureParkCandidates(cands []PressureParkCandidate, at time.Time) {
	v := &e.pressurePark
	v.mu.Lock()
	defer v.mu.Unlock()
	v.at, v.cands, v.claimed = at, cands, map[string]struct{}{}
}

// claimPressureParkCandidate takes the best unclaimed candidate of another
// app, preferring one at least as large as the refused request.
func (e *Engine) claimPressureParkCandidate(appID string, ramMB int, now time.Time) (PressureParkCandidate, bool) {
	v := &e.pressurePark
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.at.IsZero() || now.Sub(v.at) > pressureParkFreshness {
		return PressureParkCandidate{}, false
	}
	pick := -1
	for i, c := range v.cands {
		if _, taken := v.claimed[c.Instance]; taken || c.AppID == appID {
			continue
		}
		if pick < 0 {
			pick = i
		}
		if c.RAMMB >= ramMB {
			pick = i
			break
		}
	}
	if pick < 0 {
		return PressureParkCandidate{}, false
	}
	v.claimed[v.cands[pick].Instance] = struct{}{}
	return v.cands[pick], true
}

// isFleetCapacityRefusal reports a wake refused because no compute node had
// room (placement or per-node RAM/vCPU/CPU admission), as opposed to the
// app's own concurrency limit.
func isFleetCapacityRefusal(err error) bool {
	var prob *api.Problem
	return errors.As(err, &prob) && prob.Code == api.CodeCapacity
}

// parkForCapacity parks one idle instance of another app after a gateway wake
// was refused for fleet capacity. It reports whether it parked something, in
// which case the caller retries the wake once.
func (e *Engine) parkForCapacity(ctx context.Context, appID, trigger string, err error) bool {
	if trigger != TriggerGateway || !isFleetCapacityRefusal(err) || ctx.Err() != nil {
		return false
	}
	ramMB := 0
	if app, appErr := e.store.AppByID(ctx, appID); appErr == nil {
		ramMB = app.RAMMB
	}
	for range pressureParkAttempts {
		now := time.Now()
		if e.now != nil {
			now = e.now()
		}
		c, ok := e.claimPressureParkCandidate(appID, ramMB, now)
		if !ok {
			return false
		}
		// The reaper's view is up to one tick old: re-read the row so a
		// request that landed since then keeps the instance.
		ins, readErr := e.store.InstanceByID(ctx, c.Instance)
		if readErr != nil || state.State(ins.State) != state.StateRunning ||
			now.Sub(ins.LastRequestAt) < PressureParkIdle {
			continue
		}
		if parkErr := e.Park(ctx, c.Instance); parkErr != nil {
			e.log.Warn("sched: park for a refused wake", "app", appID, "instance", c.Instance, "err", parkErr)
			continue
		}
		e.log.Info("sched: parked an idle instance for a refused wake",
			"app", appID, "parked_instance", c.Instance, "parked_app", c.AppID, "node", c.NodeID,
			"idle_s", int(now.Sub(c.LastActivity).Seconds()), "refusal", err.Error())
		if counter := e.ops.EvictionFired(string(c.Plan), "wake_pressure"); counter != nil {
			counter.Inc()
		}
		return true
	}
	return false
}

// parkZeroTrafficSibling parks one idle instance of the same app whose live
// deployment receives no weighted traffic, after an admission was refused at
// the app's concurrency cap (production-us hunt #6, H5-59). A rollout moves
// the previous deployment of a traffic split to 0%, but its warm instance
// kept a concurrency slot until its idle timeout (600 s on Scale): with the
// serving instance it filled max_concurrency plus the ADR-199 rollout grant,
// every smoke wake of the new candidate was refused, and the deploy failed
// as "verification unavailable". Such an instance can only serve exact
// revision or preview requests, and parking snapshots it, so one of those
// still restores it (ADR-005).
//
// requested is the deployment the refused admission was for; it and every
// deployment with traffic, its own floor, or a non-live status (a rollout
// candidate) keep their instances. It reports whether it parked something.
func (e *Engine) parkZeroTrafficSibling(ctx context.Context, appID, requested string) bool {
	if ctx.Err() != nil {
		return false
	}
	instances, err := e.store.ListInstancesForApp(ctx, appID)
	if err != nil {
		return false
	}
	now := time.Now()
	if e.now != nil {
		now = e.now()
	}
	routed := map[string]bool{}
	routesTraffic := func(deploymentID string) bool {
		if v, ok := routed[deploymentID]; ok {
			return v
		}
		dep, depErr := e.store.DeploymentByID(ctx, deploymentID)
		v := depErr != nil || dep.Status != state.DeployLive || dep.TrafficPercent != 0 || dep.EffectiveMinInstances() > 0
		routed[deploymentID] = v
		return v
	}
	var pick *state.Instance
	for i := range instances {
		ins := &instances[i]
		if state.State(ins.State) != state.StateRunning || ins.DeploymentID == "" || ins.DeploymentID == requested {
			continue
		}
		if mode := state.InstanceMode(ins.Mode); mode != "" && mode != state.InstanceModeNormal {
			continue
		}
		if now.Sub(ins.StartedAt) < MinInstanceAge || now.Sub(ins.LastRequestAt) < PressureParkIdle {
			continue
		}
		if routesTraffic(ins.DeploymentID) {
			continue
		}
		if pick == nil || ins.LastRequestAt.Before(pick.LastRequestAt) {
			pick = ins
		}
	}
	if pick == nil {
		return false
	}
	if parkErr := e.Park(ctx, pick.ID); parkErr != nil {
		e.log.Warn("sched: park a zero-traffic instance for a refused admission", "app", appID, "instance", pick.ID, "err", parkErr)
		return false
	}
	e.log.Info("sched: parked an idle zero-traffic instance for a refused admission",
		"app", appID, "parked_instance", pick.ID, "parked_deployment", pick.DeploymentID,
		"requested_deployment", requested, "idle_s", int(now.Sub(pick.LastRequestAt).Seconds()))
	plan := ""
	if _, acct, _, acctErr := e.resolveAppAccount(ctx, appID); acctErr == nil {
		plan = string(acct.Plan)
	}
	if counter := e.ops.EvictionFired(plan, "zero_traffic"); counter != nil {
		counter.Inc()
	}
	return true
}
