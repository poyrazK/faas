package state

import (
	"context"
	"encoding/json"
	"sort"

	"github.com/onebox-faas/faas/pkg/api"
)

func (m *MemStore) promotionReleaseInputsLocked(p ProjectEnvironmentPromotion) (promotionReleaseConfiguration, error) {
	project, ok := m.projects[p.ProjectID]
	if !ok || project.AccountID != p.AccountID {
		return promotionReleaseConfiguration{}, ErrNotFound
	}
	sourceConfig := m.projectEnvironmentConfigLatestLocked(p.ProjectID, p.FromEnvironment)
	targetConfig := m.projectEnvironmentConfigLatestLocked(p.ProjectID, p.ToEnvironment)
	c := promotionReleaseConfiguration{AccountID: p.AccountID, ProjectID: p.ProjectID, From: p.FromEnvironment, To: p.ToEnvironment, Plan: m.accounts[p.AccountID].Plan,
		SourceReleaseID: m.activeProjectReleaseSets[releaseKey(p.ProjectID, p.FromEnvironment)], TargetReleaseID: m.activeProjectReleaseSets[releaseKey(p.ProjectID, p.ToEnvironment)],
		SourceConfig: projectReleaseConfigVersion{Version: sourceConfig.Version, Hash: sourceConfig.ConfigHash, Values: sourceConfig.Values},
		TargetConfig: projectReleaseConfigVersion{Version: targetConfig.Version, Hash: targetConfig.ConfigHash, Values: targetConfig.Values}, Workloads: []promotionReleaseWorkload{}}
	for _, app := range m.apps {
		if app.AccountID != p.AccountID || app.ProjectID != p.ProjectID || app.Status == AppDeleted || app.PreviewOfSlug != "" {
			continue
		}
		w := promotionReleaseWorkload{AppID: app.ID, Slug: app.Slug, SourceLive: []projectReleaseVariable{}, TargetLive: []projectReleaseVariable{}, ReservedKeys: []string{}, SourceLiveSecrets: []runtimeAppSealedSecretRow{}, TargetLiveSecrets: []runtimeAppSealedSecretRow{}}
		w.Name = app.WorkloadName
		if w.Name == "" {
			w.Name = app.Slug
		}
		source := m.deployments[releaseMemberForApp(m.projectReleaseSets[p.SourceReleaseSetID], app.ID)]
		target := m.deployments[releaseMemberForApp(m.projectReleaseSets[p.PreviousTargetReleaseSetID], app.ID)]
		if source.AppID == app.ID && source.Scope == p.FromEnvironment && source.Status == DeployLive {
			w.Source = projectCloneArtifactFromDeployment(source)
		}
		if target.AppID == app.ID && target.Scope == p.ToEnvironment && (target.Status == DeployLive || target.Status == DeploySuperseded) {
			w.Previous = projectCloneArtifactFromDeployment(target)
		}
		if rows, pinned := m.releaseEnvironmentPins[source.ID]; pinned {
			values := releaseVariables(rows)
			w.SourceSnapshot = &values
		}
		if rows, pinned := m.releaseEnvironmentPins[target.ID]; pinned {
			values := releaseVariables(rows)
			w.TargetSnapshot = &values
		}
		var err error
		_, w.SourceSettings, err = m.environmentWorkloadSettingsLocked(app, p.FromEnvironment)
		if err != nil {
			return c, err
		}
		if head := m.projectEnvironmentWorkloadSpecs[m.projectEnvironmentWorkloadHeads[workloadSpecHeadKey(m.environmentIDLocked(p.ProjectID, p.FromEnvironment), app.ID)]]; head.ID != "" {
			w.SourceHeadHash = head.Hash
		}
		if spec := m.projectEnvironmentWorkloadSpecs[m.projectEnvironmentWorkloadDeploymentSpecs[source.ID]]; spec.ID != "" {
			w.SourceSettings = spec.Settings
		}
		_, w.TargetSettings, err = m.environmentWorkloadSettingsLocked(app, p.ToEnvironment)
		if err != nil {
			return c, err
		}
		for _, value := range m.envs {
			if value.AccountID != p.AccountID || value.AppID != app.ID {
				continue
			}
			if value.Scope == p.FromEnvironment {
				w.SourceLive = append(w.SourceLive, projectReleaseVariable(value))
			}
			if value.Scope == p.ToEnvironment {
				w.TargetLive = append(w.TargetLive, projectReleaseVariable(value))
			} else {
				w.OtherVariables++
			}
		}
		for _, secret := range m.secrets {
			if secret.AccountID != p.AccountID || secret.AppID != app.ID {
				continue
			}
			row := releaseSecretRow(secret)
			row.Ciphertext, row.Kid = nil, ""
			if secret.Scope == p.FromEnvironment {
				w.SourceLiveSecrets = append(w.SourceLiveSecrets, row)
			}
			if secret.Scope == p.ToEnvironment {
				w.TargetLiveSecrets = append(w.TargetLiveSecrets, row)
				w.ReservedKeys = append(w.ReservedKeys, secret.Key)
			}
		}
		for key := range m.appEnvironmentSecretRefs {
			if key.AppID == app.ID {
				w.OtherVariables++
				if m.projectEnvironments[key.EnvironmentID].Slug == p.ToEnvironment {
					w.ReservedKeys = append(w.ReservedKeys, key.Key)
				}
			}
		}
		for key, intent := range m.appEnvironmentWorkloadIntents {
			if key.AppID != app.ID {
				continue
			}
			w.OtherVariables += len(intent.ServiceBindings)
			if m.projectEnvironments[key.EnvironmentID].Slug == p.ToEnvironment {
				for _, binding := range intent.ServiceBindings {
					w.ReservedKeys = append(w.ReservedKeys, binding.EnvKey)
				}
			}
		}
		w.SecretIneligible = len(m.environmentSecretRefsLocked(app.ID, p.ToEnvironment)) > 0 || len(m.environmentSecretRefsLocked(app.ID, p.FromEnvironment)) > 0 || len(m.environmentSecretSuppressionsLocked(app.ID, p.ToEnvironment)) > 0 || len(m.environmentSecretSuppressionsLocked(app.ID, p.FromEnvironment)) > 0
		w.SourceBindings = m.promotionReleaseBindingsLocked(app.ID, p.FromEnvironment)
		w.TargetBindings = m.promotionReleaseBindingsLocked(app.ID, p.ToEnvironment)
		normalizePromotionReleaseInputs(&w)
		c.Workloads = append(c.Workloads, w)
	}
	if p.Workers {
		for i := range c.Workloads {
			w := &c.Workloads[i]
			if w.SourceSettings.WorkloadClass == WorkloadClassWorker && w.SourceSnapshot == nil {
				ready, err := m.releaseWorkerReadyLocked(p.AccountID, w.AppID, w.Source.ID)
				if err != nil {
					return c, err
				}
				w.SourceWorkerQualified = ready != ""
			}
		}
		var workers []*api.ProjectReleaseWorker
		for _, workload := range c.Workloads {
			if workload.SourceSettings.WorkloadClass != WorkloadClassWorker && workload.TargetSettings.WorkloadClass != WorkloadClassWorker {
				continue
			}
			worker, err := m.releaseWorkerCaptureLocked(p.AccountID, p.ProjectID, c.TargetReleaseID, workload.AppID)
			if err != nil {
				return c, err
			}
			worker, err = promotionWorkerPolicy(c, worker)
			if err != nil {
				return c, err
			}
			previous := m.projectEnvironmentWorkloadSpecs[m.projectEnvironmentWorkloadDeploymentSpecs[worker.PreviousDeploymentID]]
			settings := previous.Settings
			if previous.ID == "" {
				settings, err = WorkloadSettingsFromApp(m.apps[worker.AppID])
				if err != nil {
					return c, err
				}
			}
			if err = validatePromotionWorkerBaseline(settings, worker); err != nil {
				return c, err
			}
			workers = append(workers, worker)
		}
		sort.Slice(workers, func(i, j int) bool { return workers[i].AppID < workers[j].AppID })
		c.setWorkers(workers)
	}

	sort.Slice(c.Workloads, func(i, j int) bool { return c.Workloads[i].AppID < c.Workloads[j].AppID })
	if len(p.ReleaseSecrets) > 0 {
		input := projectReleaseConfigCapture{ProjectID: p.ProjectID}
		m.loadReleaseSecretVersionsLocked(&input, p.AccountID, promotionSecretRequest(p.ReleaseSecrets))
		c.SecretVersions = input.SecretVersions
	}
	return c, nil
}

func (m *MemStore) environmentIDLocked(projectID, scope string) string {
	env, _ := m.projectEnvironmentBySlugLocked(projectID, scope)
	return env.ID
}

func (m *MemStore) promotionReleaseBindingsLocked(appID, scope string) json.RawMessage {
	envID := m.environmentIDLocked(m.apps[appID].ProjectID, scope)
	refs := map[string]environmentSecretRef{}
	for key, row := range m.appEnvironmentSecretRefs {
		if key.AppID == appID && key.EnvironmentID == envID {
			refs[key.Key] = row
		}
	}
	intent, ok := m.appEnvironmentWorkloadIntents[environmentWorkloadIntentKey{AppID: appID, EnvironmentID: envID}]
	var capturedIntent *EnvironmentWorkloadIntent
	if ok {
		capturedIntent = &intent
	}
	raw, _ := json.Marshal(struct {
		Refs         map[string]environmentSecretRef
		Suppressions []string
		Intent       *EnvironmentWorkloadIntent
	}{refs, m.environmentSecretSuppressionsLocked(appID, scope), capturedIntent})
	return raw
}

func (m *MemStore) PreviewProjectEnvironmentPromotionReleaseConfiguration(_ context.Context, p ProjectEnvironmentPromotion) (*ProjectEnvironmentPromotionReleaseConfiguration, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	c, err := m.promotionReleaseInputsLocked(p)
	if err != nil {
		return nil, err
	}
	return c.prepare(p, m.runtimeAppValuesLocked)
}

func (m *MemStore) ProjectEnvironmentPromotionReleaseConfiguration(_ context.Context, accountID, promotionID string) (*ProjectEnvironmentPromotionReleaseConfiguration, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	p, ok := m.projectEnvironmentPromotions[promotionID]
	if !ok || p.AccountID != accountID {
		return nil, ErrNotFound
	}
	c, ok := m.promotionReleaseConfigurations[promotionID]
	if !ok {
		return nil, nil
	}
	if err := c.authenticate(); err != nil {
		return nil, err
	}
	return c.public(), nil
}

func (m *MemStore) capturePromotionReleaseConfigurationLocked(p ProjectEnvironmentPromotion, workloads []ProjectEnvironmentPromotionWorkload) (*promotionReleaseConfiguration, error) {
	c, err := m.promotionReleaseInputsLocked(p)
	if err != nil {
		return nil, err
	}
	view, err := c.prepare(p, m.runtimeAppValuesLocked)
	if err != nil {
		return nil, err
	}
	if view == nil {
		if p.ReleaseConfigurationHash != "" {
			return nil, ErrConflict
		}
		return nil, nil
	}
	if err := validatePromotionWorkerAdmission(p, &c); err != nil {
		return nil, err
	}
	if (c.DestinationConfig != nil || c.Secrets != nil) && p.HealthPolicy == nil {
		return nil, ErrInvalidArgument
	}
	if view.Hash != p.ReleaseConfigurationHash || c.SourceConfig.Hash != p.SourceConfigHash || c.TargetConfig.Hash != p.PreviousTargetConfigHash {
		return nil, ErrConflict
	}
	if err := c.validateWorkloads(workloads); err != nil {
		return nil, err
	}
	return &c, nil
}

func (m *MemStore) guardPromotionReleaseConfigurationLocked(p ProjectEnvironmentPromotion, members []ProjectReleaseMember, rollback, completed bool) error {
	c, ok := m.promotionReleaseConfigurations[p.ID]
	if !ok {
		return nil
	}
	p.Workers = len(c.workers()) > 0
	current, err := m.promotionReleaseInputsLocked(p)
	if err != nil {
		return err
	}
	if rollback && !completed {
		if err := m.guardPromotionWorkerReadyLocked(p, m.promotionHealth[p.ID], true); err != nil {
			return err
		}
	}
	if !rollback && !completed {
		p.ResolveServiceBindings = c.DestinationConfig != nil
		p.Migrate = c.Migration != nil
		p.ReleaseSecrets = c.RequestedSecrets
		current.SecretVersions = c.secretVersions()
		view, err := current.prepare(p, m.runtimeAppValuesLocked)
		if err != nil || view == nil || view.Hash != c.Hash {
			return ErrConflict
		}
	} else if err := c.guardTarget(current, m.runtimeAppValuesLocked); err != nil {
		return err
	}
	if len(members) != len(c.Workloads) {
		return ErrConflict
	}
	for _, w := range c.Workloads {
		expectedID := w.Previous.ID
		if !rollback {
			prepared, ok := m.projectEnvironmentPromotionWorkloadSpecs[workloadSpecHeadKey(p.ID, w.AppID)]
			if !ok {
				return ErrConflict
			}
			expectedID = prepared.DeploymentID
			if err := validatePromotionReleaseCandidate(c, w, m.deployments[expectedID], m.runtimeAppValuesLocked); err != nil {
				return err
			}
		}
		if releaseMemberForApp(ProjectReleaseSet{Members: members}, w.AppID) != expectedID {
			return ErrConflict
		}
	}
	return nil
}
