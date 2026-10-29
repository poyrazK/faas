package apidserver

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"sync"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/edgerules"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/environmentgitops"
	"github.com/onebox-faas/faas/pkg/environmentsync"
	"github.com/onebox-faas/faas/pkg/gateway"
	"github.com/onebox-faas/faas/pkg/hostidentity"
	"github.com/onebox-faas/faas/pkg/state"
)

// This backend is owned by apid. It retains the existing policy barrier and
// adds durable post-commit effects; it must not be replaced at startup by the
// intent-only adapter used by core store tests.
type environmentGitOpsBackend struct {
	server  *server
	intent  state.EnvironmentGitOpsIntentStore
	effects state.EnvironmentGitOpsEffectStore
}

var _ environmentgitops.Backend = (*environmentGitOpsBackend)(nil)
var _ environmentgitops.EffectRecoverer = (*environmentGitOpsBackend)(nil)

func (b *environmentGitOpsBackend) Observe(ctx context.Context, lease state.EnvironmentGitOpsLease, desired environmentsync.DesiredState) (environmentgitops.Observation, error) {
	return b.intent.ObserveEnvironmentGitOps(ctx, lease, desired)
}

func (s *server) lockGitOpsEdgeApps(ctx context.Context, apps []string) (func(context.Context), error) {
	return s.edgeRules.LockEdgeRuleMutations(ctx, apps)
}

func (b *environmentGitOpsBackend) Apply(ctx context.Context, lease state.EnvironmentGitOpsLease, plan environmentsync.Plan) ([]environmentgitops.Step, error) {
	if retirement, ok := b.intent.(state.EnvironmentGitOpsWorkloadRetirementStore); ok {
		steps, err := retirement.BeginEnvironmentGitOpsWorkloadRetirements(ctx, lease, plan)
		if err != nil || len(steps) > 0 {
			return steps, err
		}
	}

	if creator, ok := b.intent.(state.EnvironmentGitOpsWorkloadCreationStore); ok {
		steps, err := creator.PrepareEnvironmentGitOpsWorkloads(ctx, lease, plan)
		if err != nil || len(steps) != 0 {
			// Reservation changes physical identity and the plan hash. The worker
			// rereads before a subsequent attempt changes routes or other intent.
			return steps, err
		}
	}
	var definition api.EnvironmentDefinition
	if json.Unmarshal(lease.Revision.Definition, &definition) != nil {
		return nil, state.ErrInvalidArgument
	}
	desired, err := environmentsync.Compile(definition)
	if err != nil {
		return nil, err
	}
	observed, err := b.Observe(ctx, lease, desired)
	if err != nil {
		return nil, err
	}
	apps := gitOpsChangedPolicyApps(plan, observed.State.ResourceIDs)
	unlock, err := b.server.lockGitOpsEdgeApps(ctx, apps)
	if err != nil {
		return nil, err
	}
	defer unlock(ctx)
	// The first prepared host must still be fenced when the complete intent
	// transaction commits. A per-workload timeout alone could add up to more
	// than the gateway's fence TTL for a large environment.
	writeCtx, stopWrite := context.WithTimeout(ctx, edgerules.EdgeRuleConvergenceTimeout)
	defer stopWrite()
	convergences, specs, err := b.preparePolicies(writeCtx, lease, apps)
	if err != nil {
		return nil, err
	}
	steps, err := b.effects.ApplyEnvironmentGitOpsWithEffects(writeCtx, lease, plan, specs)
	if err != nil {
		for _, convergence := range convergences {
			if errors.Is(err, state.ErrConflict) || errors.Is(err, state.ErrInvalidArgument) || errors.Is(err, state.ErrEnvironmentGitManaged) {
				convergence.Abort(ctx)
			} else {
				// A lost COMMIT reply does not prove rollback. Keep the
				// temporary fence; recovery checks durable effects before
				// publishing apply. Abort could release an old cached policy
				// even though its replacement committed successfully.
				convergence.Close(ctx)
			}
		}
		return nil, err
	}
	defer func() {
		for _, convergence := range convergences {
			convergence.Close(ctx)
		}
	}()
	pending, err := b.effects.PendingEnvironmentGitOpsEffects(ctx, lease)
	if err != nil {
		return steps, err
	}
	byGeneration := map[int64]state.EnvironmentGitOpsEffect{}
	for _, effect := range pending {
		byGeneration[effect.GatewayGeneration] = effect
	}
	// Each generation has its own subscription. Notify all prepared hosts
	// within one barrier interval, instead of waiting N intervals before the
	// last host learns that the complete intent transaction has committed.
	results := make(chan error, len(convergences))
	var applied sync.WaitGroup
	for _, convergence := range convergences {
		effect, exists := byGeneration[convergence.Generation]
		if !exists {
			results <- state.ErrConflict
			continue
		}
		applied.Add(1)
		go func() {
			defer applied.Done()
			results <- b.applyEffect(ctx, lease, effect, convergence)
		}()
	}
	applied.Wait()
	close(results)
	var applyError error
	for result := range results {
		applyError = errors.Join(applyError, result)
	}
	return steps, applyError
}

func gitOpsChangedPolicyApps(plan environmentsync.Plan, ids map[string]string) []string {
	apps := []string{}
	for _, change := range plan.Changes {
		if change.Path == "policies" && (change.Action == "create" || change.Action == "update" || change.Action == "remove") {
			apps = append(apps, ids[change.Resource])
		}
	}
	slices.Sort(apps)
	return slices.Compact(apps)
}

func (b *environmentGitOpsBackend) preparePolicies(ctx context.Context, lease state.EnvironmentGitOpsLease, apps []string) ([]*edgerules.EdgeRuleConvergence, []state.EnvironmentGitOpsEffectSpec, error) {
	convergences := []*edgerules.EdgeRuleConvergence{}
	specs := []state.EnvironmentGitOpsEffectSpec{}
	for _, app := range apps {
		host := gateway.BuildEnvironmentHost(hostidentity.DeploySuffix(), lease.Source.EnvironmentID, app)
		if host == "" {
			for _, convergence := range convergences {
				convergence.Abort(ctx)
			}
			return nil, nil, state.ErrInvalidArgument
		}
		convergence, err := b.server.edgeRules.PrepareEdgeRuleMutationLocked(ctx, app, "", "environment_gitops", func(context.Context) {}, host)
		if err != nil {
			for _, prepared := range convergences {
				prepared.Abort(ctx)
			}
			return nil, nil, err
		}
		convergences = append(convergences, convergence)
		nodes := make([]string, 0, len(convergence.Expected))
		for node := range convergence.Expected {
			nodes = append(nodes, node)
		}
		specs = append(specs, state.EnvironmentGitOpsEffectSpec{AppID: app, Kind: "edge_policy",
			GatewayGeneration: convergence.Generation, MatchHosts: convergence.Hosts, ExpectedNodes: nodes})
	}
	return convergences, specs, nil
}

func (b *environmentGitOpsBackend) applyEffect(ctx context.Context, lease state.EnvironmentGitOpsLease, effect state.EnvironmentGitOpsEffect, convergence *edgerules.EdgeRuleConvergence) error {
	convergence.OnAcknowledgement = func(ctx context.Context, node string) error {
		return b.effects.AcknowledgeEnvironmentGitOpsEffect(ctx, lease, effect.ID, effect.GatewayGeneration, node)
	}
	if err := convergence.Apply(ctx, ""); err != nil {
		return err
	}
	return b.effects.CompleteEnvironmentGitOpsEffect(ctx, lease, effect.ID)
}

func (b *environmentGitOpsBackend) RecoverEffects(ctx context.Context, lease state.EnvironmentGitOpsLease) error {
	if lease.Source.Spec.Mode != "enforce" {
		return nil
	}
	if _, err := b.reconcileWorkloadRetirements(ctx, lease); err != nil {
		return err
	}
	if err := b.recoverRuntime(ctx, lease); err != nil {
		return err
	}
	pending, err := b.effects.PendingEnvironmentGitOpsEffects(ctx, lease)
	if err != nil || len(pending) == 0 {
		return err
	}
	apps := make([]string, 0, len(pending))
	for _, effect := range pending {
		apps = append(apps, effect.AppID)
	}
	unlock, err := b.server.lockGitOpsEdgeApps(ctx, apps)
	if err != nil {
		return err
	}
	defer unlock(ctx)
	nodes, err := b.server.edgeRules.ServingEdgeRuleNodes(ctx)
	if err != nil {
		return err
	}
	if b.server.edgeRules.FleetRequired && len(nodes) == 0 {
		return errors.New("serving gateway authority is unavailable")
	}
	// Recovery uses one bounded notification window for the complete pending
	// set as well. Older effects can be numerous after repeated interrupted
	// generations; returning partial keeps them durable for another attempt.
	recoverCtx, cancel := context.WithTimeout(ctx, edgerules.EdgeRuleConvergenceTimeout+time.Second)
	defer cancel()
	for _, effect := range pending {
		effect, err = b.effects.ExtendEnvironmentGitOpsEffectTargets(recoverCtx, lease, effect.ID, nodes)
		if err != nil {
			return err
		}
		convergence, err := b.recoveryConvergence(recoverCtx, effect)
		if err != nil {
			return err
		}
		if err := b.applyEffect(recoverCtx, lease, effect, convergence); err != nil {
			return err
		}
	}
	return nil
}

func (b *environmentGitOpsBackend) recoveryConvergence(ctx context.Context, effect state.EnvironmentGitOpsEffect) (*edgerules.EdgeRuleConvergence, error) {
	convergence := &edgerules.EdgeRuleConvergence{Notif: b.server.notif, Generation: effect.GatewayGeneration,
		AppID: effect.AppID, Operation: "environment_gitops", Hosts: effect.MatchHosts, Expected: map[string]struct{}{},
		Cancel: func() {}, Unlock: func(context.Context) {}, Metrics: b.server.edgeRules.Metrics}
	for _, node := range effect.ExpectedNodes {
		convergence.Expected[node] = struct{}{}
	}
	if len(convergence.Expected) > 0 {
		var err error
		convergence.Events, convergence.Cancel, err = b.server.notif.Subscribe(ctx, []string{db.NotifyEdgeRuleAck})
		if err != nil {
			return nil, err
		}
	}
	return convergence, nil
}
