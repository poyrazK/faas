package main

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"sync"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/environmentgitops"
	"github.com/onebox-faas/faas/pkg/environmentsync"
	"github.com/onebox-faas/faas/pkg/gateway"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/wire"
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

type edgeRuleBatchMutationLocker interface {
	AcquireEdgeRuleMutationLocks(context.Context, []string) (func(context.Context), error)
}

func (b *environmentGitOpsBackend) Observe(ctx context.Context, lease state.EnvironmentGitOpsLease, desired environmentsync.DesiredState) (environmentgitops.Observation, error) {
	return b.intent.ObserveEnvironmentGitOps(ctx, lease, desired)
}

func (s *server) lockGitOpsEdgeApps(ctx context.Context, apps []string) (func(context.Context), error) {
	apps = slices.Clone(apps)
	slices.Sort(apps)
	apps = slices.Compact(apps)
	edgeRuleMutationMu.Lock()
	releases := []func(context.Context){}
	var once sync.Once
	unlock := func(ctx context.Context) {
		once.Do(func() {
			for i := len(releases) - 1; i >= 0; i-- {
				releases[i](ctx)
			}
			edgeRuleMutationMu.Unlock()
		})
	}
	if locker, ok := s.store.(edgeRuleBatchMutationLocker); ok {
		release, err := locker.AcquireEdgeRuleMutationLocks(ctx, apps)
		if err != nil {
			unlock(ctx)
			return nil, err
		}
		releases = append(releases, release)
	} else if locker, ok := s.store.(edgeRuleMutationLocker); ok {
		for _, app := range apps {
			release, err := locker.AcquireEdgeRuleMutationLock(ctx, app)
			if err != nil {
				unlock(ctx)
				return nil, err
			}
			releases = append(releases, release)
		}
	}
	return unlock, nil
}

func (b *environmentGitOpsBackend) Apply(ctx context.Context, lease state.EnvironmentGitOpsLease, plan environmentsync.Plan) ([]environmentgitops.Step, error) {
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
	writeCtx, stopWrite := context.WithTimeout(ctx, edgeRuleConvergenceTimeout)
	defer stopWrite()
	convergences, specs, err := b.preparePolicies(writeCtx, lease, apps)
	if err != nil {
		return nil, err
	}
	steps, err := b.effects.ApplyEnvironmentGitOpsWithEffects(writeCtx, lease, plan, specs)
	if err != nil {
		for _, convergence := range convergences {
			if errors.Is(err, state.ErrConflict) || errors.Is(err, state.ErrInvalidArgument) || errors.Is(err, state.ErrEnvironmentGitManaged) {
				convergence.abort(ctx)
			} else {
				// A lost COMMIT reply does not prove rollback. Keep the
				// temporary fence; recovery checks durable effects before
				// publishing apply. Abort could release an old cached policy
				// even though its replacement committed successfully.
				convergence.close(ctx)
			}
		}
		return nil, err
	}
	defer func() {
		for _, convergence := range convergences {
			convergence.close(ctx)
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
		effect, exists := byGeneration[convergence.generation]
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

func (b *environmentGitOpsBackend) preparePolicies(ctx context.Context, lease state.EnvironmentGitOpsLease, apps []string) ([]*edgeRuleConvergence, []state.EnvironmentGitOpsEffectSpec, error) {
	convergences := []*edgeRuleConvergence{}
	specs := []state.EnvironmentGitOpsEffectSpec{}
	for _, app := range apps {
		host := gateway.BuildEnvironmentHost(wire.DeployWildcardSuffix, lease.Source.EnvironmentID, app)
		if host == "" {
			for _, convergence := range convergences {
				convergence.abort(ctx)
			}
			return nil, nil, state.ErrInvalidArgument
		}
		convergence, err := b.server.prepareEdgeRuleMutationLocked(ctx, app, "", "environment_gitops", func(context.Context) {}, host)
		if err != nil {
			for _, prepared := range convergences {
				prepared.abort(ctx)
			}
			return nil, nil, err
		}
		convergences = append(convergences, convergence)
		nodes := make([]string, 0, len(convergence.expected))
		for node := range convergence.expected {
			nodes = append(nodes, node)
		}
		specs = append(specs, state.EnvironmentGitOpsEffectSpec{AppID: app, Kind: "edge_policy",
			GatewayGeneration: convergence.generation, MatchHosts: convergence.hosts, ExpectedNodes: nodes})
	}
	return convergences, specs, nil
}

func (b *environmentGitOpsBackend) applyEffect(ctx context.Context, lease state.EnvironmentGitOpsLease, effect state.EnvironmentGitOpsEffect, convergence *edgeRuleConvergence) error {
	convergence.onAcknowledgement = func(ctx context.Context, node string) error {
		return b.effects.AcknowledgeEnvironmentGitOpsEffect(ctx, lease, effect.ID, effect.GatewayGeneration, node)
	}
	if err := convergence.apply(ctx, ""); err != nil {
		return err
	}
	return b.effects.CompleteEnvironmentGitOpsEffect(ctx, lease, effect.ID)
}

func (b *environmentGitOpsBackend) RecoverEffects(ctx context.Context, lease state.EnvironmentGitOpsLease) error {
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
	nodes, err := b.server.servingEdgeRuleNodes(ctx)
	if err != nil {
		return err
	}
	if b.server.edgeRuleFleetRequired && len(nodes) == 0 {
		return errors.New("serving gateway authority is unavailable")
	}
	// Recovery uses one bounded notification window for the complete pending
	// set as well. Older effects can be numerous after repeated interrupted
	// generations; returning partial keeps them durable for another attempt.
	recoverCtx, cancel := context.WithTimeout(ctx, edgeRuleConvergenceTimeout+time.Second)
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

func (b *environmentGitOpsBackend) recoveryConvergence(ctx context.Context, effect state.EnvironmentGitOpsEffect) (*edgeRuleConvergence, error) {
	convergence := &edgeRuleConvergence{notif: b.server.notif, generation: effect.GatewayGeneration,
		appID: effect.AppID, operation: "environment_gitops", hosts: effect.MatchHosts, expected: map[string]struct{}{},
		cancel: func() {}, unlock: func(context.Context) {}}
	for _, node := range effect.ExpectedNodes {
		convergence.expected[node] = struct{}{}
	}
	if len(convergence.expected) > 0 {
		var err error
		convergence.events, convergence.cancel, err = b.server.notif.Subscribe(ctx, []string{db.NotifyEdgeRuleAck})
		if err != nil {
			return nil, err
		}
	}
	return convergence, nil
}
