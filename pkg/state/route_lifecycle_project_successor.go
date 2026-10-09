package state

import (
	"context"
	"encoding/json"
	"sort"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

type lifecycleProjectSuccessor struct {
	Graphs []struct {
		ID          string                 `json:"id"`
		AccountID   string                 `json:"account_id"`
		ProjectID   string                 `json:"project_id"`
		Environment string                 `json:"environment_slug"`
		Active      bool                   `json:"active"`
		Members     []ProjectReleaseMember `json:"members"`
	} `json:"graphs"`
	Spec *struct {
		ID          string                             `json:"id"`
		AppID       string                             `json:"app_id"`
		Revision    int64                              `json:"revision"`
		Hash        string                             `json:"config_hash"`
		Settings    ProjectEnvironmentWorkloadSettings `json:"settings"`
		SettingsRaw string                             `json:"settings_raw"`
		Environment struct {
			AccountID string `json:"account_id"`
			ProjectID string `json:"project_id"`
			Slug      string `json:"slug"`
		} `json:"environment"`
	} `json:"spec"`
	Live bool `json:"live"`
}

func applyLifecycleProjectSuccessor(sourceID string, target *RoutePolicySnapshot, raw []byte) error {
	var input lifecycleProjectSuccessor
	if json.Unmarshal(raw, &input) != nil || input.Spec == nil {
		return &RouteLifecycleReviewBlockedError{"successor_frozen_workload_settings_required"}
	}
	spec := input.Spec
	if spec.SettingsRaw != "" {
		if err := json.Unmarshal([]byte(spec.SettingsRaw), &spec.Settings); err != nil {
			return &RouteLifecycleReviewBlockedError{"successor_frozen_workload_settings_invalid"}
		}
	}
	hash, err := WorkloadSettingsHash(spec.Settings)
	if err != nil || hash != spec.Hash || spec.Revision < 1 || spec.AppID != target.App.ID || spec.Environment.AccountID != target.Account.ID || spec.Environment.ProjectID != target.App.ProjectID || spec.Environment.Slug != "production" {
		return &RouteLifecycleReviewBlockedError{"successor_frozen_workload_settings_invalid"}
	}
	if target.App.ID != sourceID {
		if len(input.Graphs) != 1 || !input.Live {
			return &RouteLifecycleReviewBlockedError{"successor_production_graph_required"}
		}
		graph := input.Graphs[0]
		matches := 0
		if graph.AccountID != target.Account.ID || graph.ProjectID != target.App.ProjectID || graph.Environment != "production" || !graph.Active {
			return &RouteLifecycleReviewBlockedError{"successor_production_graph_required"}
		}
		for _, member := range graph.Members {
			if member.AppID == target.App.ID {
				if member.DeploymentID != target.Contract.DeploymentID {
					return &RouteLifecycleReviewBlockedError{"successor_graph_destination_changed"}
				}
				matches++
			}
		}
		if matches != 1 {
			return &RouteLifecycleReviewBlockedError{"successor_graph_destination_ambiguous"}
		}
	}
	target.App, err = spec.Settings.ApplyTo(target.App)
	return err
}
func pgLifecycleProjectSuccessor(ctx context.Context, tx pgx.Tx, sourceID string, target *RoutePolicySnapshot) (json.RawMessage, error) {
	if target.App.ProjectID == "" {
		return nil, nil
	}
	q := sqlc.New()
	if _, err := q.LockLifecycleSuccessorProject(ctx, tx, target.App.ProjectID); err != nil {
		return nil, err
	}
	raw, err := q.ReadLifecycleProjectSuccessor(ctx, tx, sqlc.ReadLifecycleProjectSuccessorParams{AppID: target.App.ID, DeploymentID: target.Contract.DeploymentID})
	if err != nil {
		return nil, err
	}
	return raw, applyLifecycleProjectSuccessor(sourceID, target, raw)
}
func (m *MemStore) lifecycleProjectSuccessorLocked(sourceID string, target *RoutePolicySnapshot) (json.RawMessage, error) {
	if target.App.ProjectID == "" {
		return nil, nil
	}
	graphs := []map[string]any{}
	for _, graph := range m.projectReleaseSets {
		if graph.ProjectID == target.App.ProjectID && graph.AccountID == target.Account.ID && graph.EnvironmentSlug == "production" && graph.Active {
			members := append([]ProjectReleaseMember(nil), graph.Members...)
			sort.Slice(members, func(i, j int) bool { return members[i].AppID < members[j].AppID })
			graphs = append(graphs, map[string]any{"id": graph.ID, "account_id": graph.AccountID, "project_id": graph.ProjectID, "environment_slug": graph.EnvironmentSlug, "active": graph.Active, "members": members})
		}
	}
	var spec any
	if captured, ok := m.projectEnvironmentWorkloadSpecs[m.projectEnvironmentWorkloadDeploymentSpecs[target.Contract.DeploymentID]]; ok {
		spec = map[string]any{"id": captured.ID, "app_id": captured.AppID, "revision": captured.Revision, "config_hash": captured.Hash, "settings": captured.Settings, "environment": map[string]any{"account_id": captured.AccountID, "project_id": captured.ProjectID, "slug": captured.EnvironmentSlug}}
	}
	d := m.deployments[target.Contract.DeploymentID]
	raw, err := json.Marshal(map[string]any{"graphs": graphs, "spec": spec, "live": d.AppID == target.App.ID && d.Status == DeployLive && d.Scope == "production"})
	if err != nil {
		return nil, err
	}
	return raw, applyLifecycleProjectSuccessor(sourceID, target, raw)
}
