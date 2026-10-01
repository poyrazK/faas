package state

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/environmentsync"
)

var _ EnvironmentGitOpsIntentStore = (*MemStore)(nil)

func (m *MemStore) gitOpsGuardScopedWriteLocked(accountID, appID, scope string, paths []string) (*environmentGitOpsMemory, error) {
	for _, memory := range m.environmentGitOps {
		if memory.source.AccountID != accountID || memory.source.EnvironmentSlug != scope {
			continue
		}
		for resource, id := range memory.resources {
			if id != appID {
				continue
			}
			if memory.source.Spec.Mode == "enforce" {
				for _, path := range paths {
					key := (environmentsync.Field{Resource: resource, Path: path}).Key()
					if _, owned := memory.owners[key]; owned && !memory.overrides[key].ExpiresAt.After(time.Now()) {
						return nil, ErrEnvironmentGitManaged
					}
				}
			}
			return memory, nil
		}
	}
	return nil, nil
}

func touchGitOpsMemoryIntent(memory *environmentGitOpsMemory) {
	if memory != nil {
		memory.source.IntentVersion++
		memory.source.UpdatedAt = time.Now().UTC()
		memory.next = memory.source.UpdatedAt
	}
}

func (m *MemStore) gitOpsGuardAppRemovalLocked(appID string) ([]*environmentGitOpsMemory, error) {
	var sources []*environmentGitOpsMemory
	for _, memory := range m.environmentGitOps {
		for resource, id := range memory.resources {
			if id != appID {
				continue
			}
			key := (environmentsync.Field{Resource: resource, Path: "presence"}).Key()
			if _, owned := memory.owners[key]; owned && memory.source.Spec.Mode == "enforce" && !memory.overrides[key].ExpiresAt.After(time.Now()) {
				return nil, ErrEnvironmentGitManaged
			}
			sources = append(sources, memory)
		}
	}
	return sources, nil
}

func (m *MemStore) gitOpsGuardConfigurationLocked(config ProjectEnvironmentConfig) (*environmentGitOpsMemory, error) {
	for _, memory := range m.environmentGitOps {
		source := memory.source
		if source.AccountID != config.AccountID || source.ProjectID != config.ProjectID || source.EnvironmentSlug != config.EnvironmentSlug {
			continue
		}
		if source.Spec.Mode == "enforce" {
			previous := m.projectEnvironmentConfigLatestLocked(config.ProjectID, config.EnvironmentSlug)
			var oldValues, nextValues map[string]json.RawMessage
			_ = json.Unmarshal(previous.Values, &oldValues)
			_ = json.Unmarshal(config.Values, &nextValues)
			for key, owner := range memory.owners {
				if owner.Resource != "environment" || !strings.HasPrefix(owner.Path, "configuration/") {
					continue
				}
				name := strings.TrimPrefix(owner.Path, "configuration/")
				old, _ := canonicalGitOpsValue(oldValues[name])
				next, _ := canonicalGitOpsValue(nextValues[name])
				if !bytes.Equal(old, next) && !memory.overrides[key].ExpiresAt.After(time.Now()) {
					return nil, ErrEnvironmentGitManaged
				}
			}
		}
		return memory, nil
	}
	return nil, nil
}

func (m *MemStore) gitOpsSnapshotLocked(memory *environmentGitOpsMemory) gitOpsIntentSnapshot {
	source := memory.source
	snapshot := gitOpsIntentSnapshot{Version: source.IntentVersion, Project: m.projects[source.ProjectID].Slug, Environment: source.EnvironmentSlug, EnvironmentID: source.EnvironmentID, Plan: m.accounts[source.AccountID].Plan, SourceID: source.ID, Prune: source.Spec.Prune}
	config := m.projectEnvironmentConfigLatestLocked(source.ProjectID, source.EnvironmentSlug)
	_ = json.Unmarshal(config.Values, &snapshot.Configuration)
	for resource, appID := range memory.resources {
		snapshot.Resources = append(snapshot.Resources, gitOpsIntentResource{Resource: resource, AppID: appID})
	}
	for key, id := range memory.queues {
		resource, path, _ := strings.Cut(key, "#")
		snapshot.QueueBindings = append(snapshot.QueueBindings, gitOpsQueueIdentity{Resource: resource, Path: path, BindingID: id})
	}
	for _, owner := range memory.owners {
		owner.Value = append(json.RawMessage(nil), owner.Value...)
		snapshot.Owners = append(snapshot.Owners, owner)
	}
	for _, override := range memory.overrides {
		snapshot.Overrides = append(snapshot.Overrides, override)
	}
	for _, app := range m.apps {
		if app.AccountID != source.AccountID || app.ProjectID != source.ProjectID || app.Status == AppDeleted {
			continue
		}
		row := gitOpsIntentApp{ID: app.ID, Slug: app.Slug, Type: app.Type, WorkloadClass: app.WorkloadClass, Variables: map[string]string{}}
		row.SecretRefs = m.environmentSecretRefsLocked(app.ID, source.EnvironmentSlug)
		row.SuppressedKeys = m.environmentSecretSuppressionsLocked(app.ID, source.EnvironmentSlug)
		row.SuppressionCount = m.environmentSecretSuppressionCountLocked(app.ID)
		for _, deployment := range m.deployments {
			if deployment.AppID == app.ID && normalizedDeploymentScope(deployment.Scope) == source.EnvironmentSlug && deployment.Status == DeployLive {
				row.LiveDeployments = append(row.LiveDeployments, gitOpsSecretBaseline{ID: deployment.ID, SecretRefs: append(json.RawMessage(nil), deployment.OverrideEnvSecrets...)})
			}
		}
		for key := range m.appEnvironmentSecretRefs {
			if key.AppID == app.ID {
				row.SecretRefCount++
			}
		}
		for _, secret := range m.secrets {
			if secret.AppID == app.ID && secret.AccountID == source.AccountID && secret.Scope == source.EnvironmentSlug {
				row.SecretNames = append(row.SecretNames, secret.Key)
			}
		}
		for _, binding := range m.queueBindings {
			if binding.AppID == app.ID && binding.AccountID == source.AccountID && binding.EnvironmentID == source.EnvironmentID {
				row.QueueBindings = append(row.QueueBindings, m.gitOpsQueueIntentLocked(binding))
			}
		}
		for _, variable := range m.envs {
			if variable.AccountID == source.AccountID && variable.AppID == app.ID {
				row.VariableCount++
			}
			if variable.AccountID == source.AccountID && variable.AppID == app.ID && variable.Scope == source.EnvironmentSlug {
				row.Variables[variable.Key] = variable.Value
			}
		}
		key := projectEnvironmentRoutePolicyKey(app.ID, source.EnvironmentSlug)
		if routes, ok := m.projectEnvironmentRoutePolicies[key]; ok {
			contract := api.EnvironmentRouteContract{OnlyAllowDeclaredRoutes: routes.OnlyAllowDeclaredRoutes, DeclaredRoutes: []api.DeclaredRoute{}}
			for _, route := range routes.DeclaredRoutes {
				contract.DeclaredRoutes = append(contract.DeclaredRoutes, api.DeclaredRoute{Path: route.Path, Methods: append([]string(nil), route.Methods...)})
			}
			row.Routes = &contract
		}
		if policy, ok := m.projectEnvironmentEdgePolicies[key]; ok {
			rules := cloneProjectEnvironmentEdgeRules(policy.Rules)
			row.Policies = &rules
		}
		snapshot.Apps = append(snapshot.Apps, row)
	}
	return snapshot
}

func (m *MemStore) ObserveEnvironmentGitOps(_ context.Context, lease EnvironmentGitOpsLease, desired environmentsync.DesiredState) (EnvironmentGitOpsObservation, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	memory, err := m.gitOpsLeaseLocked(lease, time.Now())
	if err != nil {
		return EnvironmentGitOpsObservation{}, err
	}
	return compileGitOpsObservation(m.gitOpsSnapshotLocked(memory), desired)
}

func (m *MemStore) approvedGitOpsLocked(accountID, sourceID string) (*environmentGitOpsMemory, EnvironmentDesiredRevision, environmentsync.DesiredState, error) {
	memory, ok := m.environmentGitOps[sourceID]
	if !ok || memory.source.AccountID != accountID {
		return nil, EnvironmentDesiredRevision{}, environmentsync.DesiredState{}, ErrNotFound
	}
	if memory.source.Suspended || memory.source.ApprovedRevisionID == "" {
		return nil, EnvironmentDesiredRevision{}, environmentsync.DesiredState{}, ErrConflict
	}
	if _, err := m.projectEnvironmentBySlugLocked(memory.source.ProjectID, memory.source.EnvironmentSlug); err != nil {
		return nil, EnvironmentDesiredRevision{}, environmentsync.DesiredState{}, err
	}
	revision := memory.revisions[memory.source.ApprovedRevisionID]
	desired, err := desiredEnvironmentRevision(revision)
	return memory, revision, desired, err
}

func (m *MemStore) PreviewEnvironmentGitOpsAdoption(_ context.Context, accountID, sourceID string) (environmentsync.Plan, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	memory, revision, desired, err := m.approvedGitOpsLocked(accountID, sourceID)
	if err != nil {
		return environmentsync.Plan{}, err
	}
	observed, err := compileGitOpsObservation(m.gitOpsSnapshotLocked(memory), desired)
	if err != nil {
		return environmentsync.Plan{}, err
	}
	return environmentGitOpsPlan(memory.source, revision, desired, observed, true)
}

func (m *MemStore) AdoptEnvironmentGitOps(_ context.Context, accountID, sourceID, reviewedHash string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	memory, revision, desired, err := m.approvedGitOpsLocked(accountID, sourceID)
	if err != nil {
		return err
	}
	observed, err := compileGitOpsObservation(m.gitOpsSnapshotLocked(memory), desired)
	if err != nil {
		return err
	}
	plan, err := environmentGitOpsPlan(memory.source, revision, desired, observed, true)
	if err != nil {
		return err
	}
	if !plan.CanApply() || plan.Hash != reviewedHash {
		return ErrConflict
	}
	// Validate every preserved reference before transferring any ownership.
	refs := map[string]string{}
	for _, change := range plan.Changes {
		if change.Action == "adopt" && strings.HasPrefix(change.Path, "secret_refs/") && !bytes.Equal(change.Before, json.RawMessage("null")) {
			var ref string
			if json.Unmarshal(change.Before, &ref) != nil || !ValidSecretReference(ref) {
				return ErrInvalidArgument
			}
			refs[change.Resource+"#"+change.Path] = ref
		}
	}
	if memory.resources == nil {
		memory.resources = map[string]string{}
	}
	if memory.owners == nil {
		memory.owners = map[string]environmentsync.Ownership{}
	}
	for _, change := range plan.Changes {
		if change.Action != "adopt" {
			continue
		}
		if change.Path == "presence" {
			memory.resources[change.Resource] = observed.State.ResourceIDs[change.Resource]
		}
		if strings.HasPrefix(change.Path, "queue_bindings/") {
			if memory.queues == nil {
				memory.queues = map[string]string{}
			}
			memory.queues[(environmentsync.Field{Resource: change.Resource, Path: change.Path}).Key()] = observed.State.ResourceIDs[gitOpsQueueResource(change.Resource, change.Path)]
		}
		if strings.HasPrefix(change.Path, "secret_refs/") && !bytes.Equal(change.Before, json.RawMessage("null")) {
			key := environmentSecretRefKey{observed.State.ResourceIDs[change.Resource], memory.source.EnvironmentID, strings.TrimPrefix(change.Path, "secret_refs/")}
			if m.appEnvironmentSecretRefs == nil {
				m.appEnvironmentSecretRefs = map[environmentSecretRefKey]environmentSecretRef{}
			}
			m.appEnvironmentSecretRefs[key] = environmentSecretRef{refs[change.Resource+"#"+change.Path], time.Now().UTC()}
			delete(m.appEnvironmentSecretSuppressions, key)
			m.markEnvironmentRuntimeChangedAndSnapshotsLocked(key.AppID, memory.source.EnvironmentSlug, time.Now().UTC())
		}
		field := environmentsync.Field{Resource: change.Resource, Path: change.Path, Value: append(json.RawMessage(nil), change.After...)}
		memory.owners[field.Key()] = environmentsync.Ownership{Field: field, Manager: sourceID}
	}
	memory.source.IntentVersion++
	memory.next = time.Now().UTC()
	return nil
}

func (m *MemStore) ApplyEnvironmentGitOps(ctx context.Context, lease EnvironmentGitOpsLease, reviewed environmentsync.Plan) ([]EnvironmentGitOpsStep, error) {
	return m.applyEnvironmentGitOps(ctx, lease, reviewed, nil, false)
}

func (m *MemStore) ApplyEnvironmentGitOpsWithEffects(ctx context.Context, lease EnvironmentGitOpsLease, reviewed environmentsync.Plan, effects []EnvironmentGitOpsEffectSpec) ([]EnvironmentGitOpsStep, error) {
	return m.applyEnvironmentGitOps(ctx, lease, reviewed, effects, true)
}

func (m *MemStore) applyEnvironmentGitOps(_ context.Context, lease EnvironmentGitOpsLease, reviewed environmentsync.Plan, effects []EnvironmentGitOpsEffectSpec, requireEffects bool) ([]EnvironmentGitOpsStep, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	memory, err := m.gitOpsLeaseLocked(lease, time.Now())
	if err != nil {
		return nil, err
	}
	if memory.source.Spec.Mode != "enforce" {
		return nil, ErrConflict
	}
	desired, err := desiredEnvironmentRevision(memory.revisions[memory.source.ApprovedRevisionID])
	if err != nil {
		return nil, err
	}
	snapshot := m.gitOpsSnapshotLocked(memory)
	observed, err := compileGitOpsObservation(snapshot, desired)
	if err != nil {
		return nil, err
	}
	plan, err := environmentGitOpsPlan(memory.source, lease.Revision, desired, observed, false)
	if err != nil {
		return nil, err
	}
	if !plan.CanApply() || plan.Hash != reviewed.Hash {
		return nil, ErrConflict
	}
	if requireEffects {
		effects, err = validateGitOpsEffects(plan, observed, effects)
		if err != nil {
			return nil, err
		}
		for _, candidate := range effects {
			for _, source := range m.environmentGitOps {
				for _, effect := range source.effects {
					if effect.GatewayGeneration == candidate.GatewayGeneration {
						return nil, ErrConflict
					}
				}
			}
		}
	}
	queueState, queueIdentities, err := m.prepareGitOpsQueuesLocked(memory.source, desired.Definition, plan, observed.State.ResourceIDs)
	if err != nil {
		return nil, err
	}
	// Decode and validate every operation before changing any in-memory rows.
	configChanged := false
	policies := map[string][]ProjectEnvironmentEdgeRule{}
	routes := map[string]api.EnvironmentRouteContract{}
	variables := map[string]string{}
	secretRefs := map[string]string{}
	for _, change := range plan.Changes {
		if change.Action == "keep" || change.Action == "retain_unmanaged" || change.Action == "overridden" {
			continue
		}
		if change.Path == "presence" {
			return nil, ErrConflict
		}
		if strings.HasPrefix(change.Path, "configuration/") {
			key := strings.TrimPrefix(change.Path, "configuration/")
			if change.Action == "remove" {
				delete(snapshot.Configuration, key)
			} else {
				snapshot.Configuration[key] = change.After
			}
			configChanged = true
		} else if change.Action != "remove" {
			switch {
			case change.Path == "policies":
				rules, err := decodeEnvironmentGitOpsPolicies(change.After)
				if err != nil {
					return nil, err
				}
				policies[change.Resource] = rules
			case change.Path == "routes":
				var contract api.EnvironmentRouteContract
				if json.Unmarshal(change.After, &contract) != nil {
					return nil, ErrInvalidArgument
				}
				routes[change.Resource] = contract
			case strings.HasPrefix(change.Path, "queue_bindings/"):
				// Validated on detached queue/consumer maps above.
			case strings.HasPrefix(change.Path, "secret_refs/"):
				var ref string
				if json.Unmarshal(change.After, &ref) != nil || !ValidSecretReference(ref) {
					return nil, ErrInvalidArgument
				}
				secretRefs[change.Resource+"#"+change.Path] = ref
			case strings.HasPrefix(change.Path, "variables/"):
				var value string
				if json.Unmarshal(change.After, &value) != nil {
					return nil, ErrInvalidArgument
				}
				variables[change.Resource+"#"+change.Path] = value
			default:
				return nil, ErrInvalidArgument
			}
		}
	}
	var configValues json.RawMessage
	var configHash string
	if configChanged {
		raw, _ := json.Marshal(snapshot.Configuration)
		configValues, configHash, err = api.NormalizeProjectEnvironmentConfig(raw)
		if err != nil {
			return nil, ErrInvalidArgument
		}
	}
	if queueState != nil {
		m.queueBindings, m.triggers = queueState.queueBindings, queueState.triggers
		if memory.queues == nil {
			memory.queues = map[string]string{}
		}
		for key, id := range queueIdentities {
			memory.queues[key] = id
		}
	}
	now, source := time.Now().UTC(), memory.source
	steps := []EnvironmentGitOpsStep{}
	if memory.owners == nil {
		memory.owners = map[string]environmentsync.Ownership{}
	}
	for _, change := range plan.Changes {
		if change.Action == "keep" || change.Action == "retain_unmanaged" || change.Action == "overridden" {
			continue
		}
		appID := observed.State.ResourceIDs[change.Resource]
		key := projectEnvironmentRoutePolicyKey(appID, source.EnvironmentSlug)
		switch {
		case strings.HasPrefix(change.Path, "secret_refs/"):
			refKey := environmentSecretRefKey{appID, source.EnvironmentID, strings.TrimPrefix(change.Path, "secret_refs/")}
			if m.appEnvironmentSecretRefs == nil {
				m.appEnvironmentSecretRefs = map[environmentSecretRefKey]environmentSecretRef{}
			}
			if change.Action == "remove" {
				m.suppressEnvironmentSecretReferenceLocked(refKey, now)
			} else {
				m.appEnvironmentSecretRefs[refKey] = environmentSecretRef{secretRefs[change.Resource+"#"+change.Path], now}
				delete(m.appEnvironmentSecretSuppressions, refKey)
			}
			m.markEnvironmentRuntimeChangedAndSnapshotsLocked(appID, source.EnvironmentSlug, now)

		case strings.HasPrefix(change.Path, "variables/"):
			m.markEnvironmentRuntimeChangedAndSnapshotsLocked(appID, source.EnvironmentSlug, now)
			variableKey := envKey{AppID: appID, Scope: source.EnvironmentSlug, Key: strings.TrimPrefix(change.Path, "variables/")}
			if change.Action == "remove" {
				delete(m.envs, variableKey)
			} else {
				row := m.envs[variableKey]
				row.AccountID, row.AppID, row.Scope, row.Key = source.AccountID, appID, source.EnvironmentSlug, variableKey.Key
				row.Value, row.UpdatedAt = variables[change.Resource+"#"+change.Path], now
				if row.CreatedAt.IsZero() {
					row.CreatedAt = now
				}
				m.envs[variableKey] = row
			}
		case change.Path == "routes":
			if change.Action == "remove" {
				delete(m.projectEnvironmentRoutePolicies, key)
			} else {
				contract := routes[change.Resource]
				row := ProjectEnvironmentRoutePolicy{AccountID: source.AccountID, ProjectID: source.ProjectID, AppID: appID, EnvironmentSlug: source.EnvironmentSlug, OnlyAllowDeclaredRoutes: contract.OnlyAllowDeclaredRoutes, CreatedAt: now, UpdatedAt: now}
				for _, route := range contract.DeclaredRoutes {
					row.DeclaredRoutes = append(row.DeclaredRoutes, DeclaredRoute{Path: route.Path, Methods: route.Methods})
				}
				m.projectEnvironmentRoutePolicies[key] = row
			}
		case change.Path == "policies":
			if change.Action == "remove" {
				delete(m.projectEnvironmentEdgePolicies, key)
			} else {
				m.projectEnvironmentEdgePolicies[key] = ProjectEnvironmentEdgePolicy{AccountID: source.AccountID, ProjectID: source.ProjectID, AppID: appID, EnvironmentSlug: source.EnvironmentSlug, Rules: policies[change.Resource], CreatedAt: now, UpdatedAt: now}
			}
		}
		field := environmentsync.Field{Resource: change.Resource, Path: change.Path, Value: append(json.RawMessage(nil), change.After...)}
		if change.Action == "remove" {
			delete(memory.owners, field.Key())
			delete(memory.overrides, field.Key())
		} else {
			memory.owners[field.Key()] = environmentsync.Ownership{Field: field, Manager: source.ID}
		}
		steps = append(steps, EnvironmentGitOpsStep{Resource: change.Resource, Path: change.Path, Action: change.Action, Status: "applied"})
	}
	if configChanged {
		key := projectEnvironmentConfigKey(source.ProjectID, source.EnvironmentSlug)
		m.projectEnvironmentConfigs[key] = append(m.projectEnvironmentConfigs[key], ProjectEnvironmentConfig{ID: newID(), AccountID: source.AccountID, ProjectID: source.ProjectID, EnvironmentSlug: source.EnvironmentSlug, Version: int64(len(m.projectEnvironmentConfigs[key]) + 1), Values: configValues, ConfigHash: configHash, CreatedAt: now})
	}
	if len(steps) > 0 {
		memory.source.IntentVersion++
	}
	if memory.effects == nil {
		memory.effects = map[string]EnvironmentGitOpsEffect{}
	}
	for _, spec := range effects {
		effect := EnvironmentGitOpsEffect{EnvironmentGitOpsEffectSpec: spec, ID: newID(), SourceID: source.ID,
			RevisionID: lease.Revision.ID, Generation: source.Generation, IntentVersion: memory.source.IntentVersion, PlanHash: plan.Hash}
		memory.effects[effect.ID] = cloneEnvironmentGitOpsEffect(effect)
	}
	if requireEffects {
		for _, appID := range gitOpsChangedVariableApps(plan, observed.State.ResourceIDs) {
			boundary, _ := m.environmentRuntimeChangedAtLocked(appID, source.EnvironmentSlug)
			m.insertGitOpsRuntimeEffectLocked(memory, lease, plan, appID, boundary)
		}
	}
	run := memory.runs[lease.RunID]
	run.Status = "applying"
	run.Plan, _ = json.Marshal(plan)
	run.Steps, _ = json.Marshal(steps)
	memory.runs[lease.RunID] = run
	return steps, nil
}
