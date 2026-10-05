package state

import (
	"context"
	"sort"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/environmentsync"
)

var _ EnvironmentGitOpsRuntimeStore = (*MemStore)(nil)

func (m *MemStore) gitOpsRuntimeTargetsLocked(memory *environmentGitOpsMemory) []EnvironmentGitOpsRuntimeTarget {
	targets := []EnvironmentGitOpsRuntimeTarget{}
	for resource, appID := range memory.resources {
		if appID == "" {
			continue
		}
		needed := len(m.environmentSecretSuppressionsLocked(appID, memory.source.EnvironmentSlug)) > 0
		unqualified := int64(0)
		for _, owner := range memory.owners {
			if owner.Resource == resource && owner.Manager == memory.source.ID && gitOpsWorkloadField(owner.Path) {
				needed = true
				unqualified = 1
			}
		}
		required := time.Unix(0, 0).UTC()
		if at, _ := m.environmentRuntimeChangedAtLocked(appID, memory.source.EnvironmentSlug); at.After(required) {
			required = at
		}
		for _, owner := range memory.owners {
			if owner.Resource == resource && owner.Manager == memory.source.ID && (strings.HasPrefix(owner.Path, "variables/") || strings.HasPrefix(owner.Path, "secret_refs/")) {
				needed = true
				variable := m.envs[envKey{AppID: appID, Scope: memory.source.EnvironmentSlug, Key: strings.TrimPrefix(owner.Path, "variables/")}]
				if variable.UpdatedAt.After(required) {
					required = variable.UpdatedAt
				}
			}
		}
		for _, effect := range memory.runtime {
			if effect.AppID == appID && effect.CompletedAt == nil {
				needed = true
				if effect.RequiredAt.After(required) {
					required = effect.RequiredAt
				}
			}
		}
		if !needed {
			continue
		}
		target := EnvironmentGitOpsRuntimeTarget{AppID: appID, Resource: resource, Environment: memory.source.EnvironmentSlug, RequiredAt: required}
		for _, instance := range m.instances {
			deployment := m.deployments[instance.DeploymentID]
			if instance.AppID != appID || deployment.Scope != target.Environment {
				continue
			}
			state := State(instance.State)
			if state == StateWaking || state == StateColdBooting || state == StateMigrating {
				target.StartingResidents++
			}
			if state == StateWaking || state == StateColdBooting || state == StateRunning || state == StateWarm || state == StateDraining {
				inputs, exists := m.instanceRuntimeConfigInputsLocked(instance.ID)
				if !exists || inputs.Scope != target.Environment || inputs.Boundary.Before(required) || !m.runtimeConfigInputsFreshLocked(appID, inputs) {
					target.StaleResidents++
				}
			}
		}
		for _, snapshot := range m.snapshots {
			deployment := m.deployments[snapshot.DeploymentID]
			if deployment.AppID == appID && deployment.Scope == target.Environment && !snapshot.Stale && !snapshot.DeletePending {
				inputs, exists := m.snapshotRuntimeConfigReceipts[snapshot.ID]
				if !exists || inputs.Scope != target.Environment || inputs.Boundary.Before(required) || !m.runtimeConfigInputsFreshLocked(appID, inputs) {
					target.StaleSnapshots++
				}
			}
		}
		target.UnqualifiedWorkloads = unqualified
		targets = append(targets, target)
	}
	sort.Slice(targets, func(i, j int) bool { return targets[i].AppID < targets[j].AppID })
	return targets
}

func (m *MemStore) ObserveEnvironmentGitOpsRuntime(_ context.Context, lease EnvironmentGitOpsLease) ([]EnvironmentGitOpsRuntimeTarget, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	memory, err := m.gitOpsLeaseLocked(lease, time.Now())
	if err != nil {
		return nil, err
	}
	return m.gitOpsRuntimeTargetsLocked(memory), nil
}

func (m *MemStore) insertGitOpsRuntimeEffectLocked(memory *environmentGitOpsMemory, lease EnvironmentGitOpsLease, plan environmentsync.Plan, appID string, required time.Time) {
	if memory.runtime == nil {
		memory.runtime = map[string]EnvironmentGitOpsRuntimeEffect{}
	}
	for id, effect := range memory.runtime {
		// Source/generation/hash/app is the PostgreSQL uniqueness boundary.
		if effect.AppID == appID && effect.Generation == lease.Source.Generation && effect.PlanHash == plan.Hash {
			if effect.CompletedAt != nil {
				effect.CompletedAt, effect.RequestedAt, effect.NextRequestAt, effect.WakeID = nil, nil, time.Now(), newID()
				if required.After(effect.RequiredAt) {
					effect.RequiredAt = required
				}
				memory.runtime[id] = effect
			}
			return
		}
	}
	id := newID()
	memory.runtime[id] = EnvironmentGitOpsRuntimeEffect{ID: id, SourceID: lease.Source.ID, AppID: appID,
		Generation: lease.Source.Generation, PlanHash: plan.Hash,
		Environment: lease.Source.EnvironmentSlug, RequiredAt: required, WakeID: newID(), NextRequestAt: time.Now()}
}

func (m *MemStore) EnsureEnvironmentGitOpsRuntime(_ context.Context, lease EnvironmentGitOpsLease, reviewed environmentsync.Plan) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	memory, err := m.gitOpsLeaseLocked(lease, time.Now())
	if err != nil {
		return err
	}
	if memory.source.Spec.Mode != "enforce" {
		return ErrConflict
	}
	desired, err := desiredEnvironmentRevision(lease.Revision)
	if err != nil {
		return err
	}
	observed, err := compileGitOpsObservation(m.gitOpsSnapshotLocked(memory), desired)
	if err != nil {
		return err
	}
	plan, err := environmentGitOpsPlan(memory.source, lease.Revision, desired, observed, false)
	if err != nil {
		return err
	}
	if !plan.CanApply() || plan.HasDrift() || plan.Hash != reviewed.Hash {
		return ErrConflict
	}
	appsPending := map[string]bool{}
	for _, effect := range memory.runtime {
		if effect.CompletedAt == nil {
			appsPending[effect.AppID] = true
		}
	}
	for _, target := range m.gitOpsRuntimeTargetsLocked(memory) {
		if !target.Fresh() && !appsPending[target.AppID] {
			m.insertGitOpsRuntimeEffectLocked(memory, lease, plan, target.AppID, target.RequiredAt)
		}
	}
	return nil
}

func cloneGitOpsRuntimeEffect(effect EnvironmentGitOpsRuntimeEffect) EnvironmentGitOpsRuntimeEffect {
	if effect.RequestedAt != nil {
		at := *effect.RequestedAt
		effect.RequestedAt = &at
	}
	if effect.CompletedAt != nil {
		at := *effect.CompletedAt
		effect.CompletedAt = &at
	}
	return effect
}

func (m *MemStore) PendingEnvironmentGitOpsRuntime(_ context.Context, lease EnvironmentGitOpsLease) ([]EnvironmentGitOpsRuntimeEffect, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	memory, err := m.gitOpsLeaseLocked(lease, time.Now())
	if err != nil {
		return nil, err
	}
	out := []EnvironmentGitOpsRuntimeEffect{}
	for _, effect := range memory.runtime {
		if effect.CompletedAt == nil {
			out = append(out, cloneGitOpsRuntimeEffect(effect))
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

func (m *MemStore) ReconcileEnvironmentGitOpsRuntime(_ context.Context, lease EnvironmentGitOpsLease, id string) (EnvironmentGitOpsRuntimeProgress, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	memory, err := m.gitOpsLeaseLocked(lease, time.Now())
	if err != nil {
		return EnvironmentGitOpsRuntimeProgress{}, err
	}
	effect, exists := memory.runtime[id]
	if !exists || effect.CompletedAt != nil {
		return EnvironmentGitOpsRuntimeProgress{}, ErrConflict
	}
	m.markEnvironmentRuntimeChangedAndSnapshotsLocked(effect.AppID, effect.Environment, effect.RequiredAt)
	var target *EnvironmentGitOpsRuntimeTarget
	targets := m.gitOpsRuntimeTargetsLocked(memory)
	for i := range targets {
		if targets[i].AppID == effect.AppID {
			target = &targets[i]
			break
		}
	}
	if target == nil {
		return EnvironmentGitOpsRuntimeProgress{}, ErrConflict
	}
	if target.RequiredAt.After(effect.RequiredAt) {
		effect.RequiredAt, effect.WakeID, effect.RequestedAt, effect.NextRequestAt = target.RequiredAt, newID(), nil, time.Now()
		m.markEnvironmentRuntimeChangedAndSnapshotsLocked(effect.AppID, effect.Environment, target.RequiredAt)
		// The pending effect must carry the new boundary while re-observing.
		memory.runtime[id] = effect
		targets = m.gitOpsRuntimeTargetsLocked(memory)
		for i := range targets {
			if targets[i].AppID == effect.AppID {
				target = &targets[i]
				break
			}
		}
	}
	progress := EnvironmentGitOpsRuntimeProgress{Ready: target.Fresh()}
	now := time.Now().UTC()
	if target.Fresh() {
		effect.CompletedAt = &now
	} else if lease.Source.Spec.Mode == "enforce" && target.StaleResidents > 0 && !effect.NextRequestAt.After(now) {
		effect.RequestedAt, effect.NextRequestAt = &now, now.Add(api.EnvironmentGitOpsRuntimeRefreshRetry)
		progress.Request = &EnvironmentGitOpsRuntimeRequest{AppID: effect.AppID, Scope: effect.Environment, WakeID: effect.WakeID}
	}
	memory.runtime[id] = effect
	return progress, nil
}
