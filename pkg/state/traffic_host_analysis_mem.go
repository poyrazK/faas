// adr: 375
package state

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"sort"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/hostidentity"
)

// Only proposed rows differ from the store. Callers hold m.mu until the
// verdict and publication, including related intent and activity bookkeeping.
type memTrafficPolicyChange struct {
	GlobalRoutes bool
	Rules        map[string]EdgeRule
	Presets      map[string]CorsPreset
	Apps         map[string]App
	Environments map[string]ProjectEnvironment
	Policies     map[string]ProjectEnvironmentEdgePolicy
	Aliases      map[string]DeploymentAlias
}

func visitMemTrafficRows[T any](ctx context.Context, rows, proposed map[string]T, visit func(T) error) error {
	for id, row := range rows {
		if err := ctx.Err(); err != nil {
			return err
		}
		if replacement, found := proposed[id]; found {
			row = replacement
		}
		if err := visit(row); err != nil {
			return err
		}
	}
	for id, row := range proposed {
		if _, exists := rows[id]; exists {
			continue
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := visit(row); err != nil {
			return err
		}
	}
	return nil
}

func (m *MemStore) readMemTrafficHostAnalysisLocked(ctx context.Context, account string, change memTrafficPolicyChange) (trafficHostAnalysis, error) {
	var view trafficHostAnalysis
	groups := make(map[trafficHostGroup]trafficHostGroup)
	addRule := func(rule EdgeRule, environment string) error {
		if !rule.Enabled || (!change.GlobalRoutes && rule.AccountID != account) || (change.GlobalRoutes && rule.Kind != EdgeRuleKindRoute) {
			return nil
		}
		key := trafficHostGroup{App: rule.AppID, Pattern: rule.MatchHost, Kind: string(rule.Kind), Environment: environment}
		if rule.Kind == EdgeRuleKindCORSA && rule.Action.CORS != nil && rule.Action.CORS.CorsPresetID != nil {
			key.Preset = *rule.Action.CORS.CorsPresetID
		}
		canonical, err := memEdgeRuleTrafficProjectionSize(rule)
		if err != nil {
			return err
		}
		compiled, err := json.Marshal(rule)
		if err != nil {
			return fmt.Errorf("state: encode in-memory traffic rule: %w", err)
		}
		group, exists := groups[key]
		if !exists {
			group = key
		}
		group.Rows++
		group.Canonical += canonical + 16
		group.Compiled += int64(len(compiled)) + 2
		groups[key] = group
		return checkMemTrafficAnalysisInputs(len(groups) + len(view.Environments) + len(view.PrimaryHosts) + len(view.AliasHosts))
	}
	if err := visitMemTrafficRows(ctx, m.edgeRules, change.Rules, func(rule EdgeRule) error {
		return addRule(rule, "")
	}); err != nil {
		return view, err
	}
	environments := make(map[string][]ProjectEnvironment)
	if err := visitMemTrafficRows(ctx, m.projectEnvironments, change.Environments, func(environment ProjectEnvironment) error {
		if !change.GlobalRoutes && environment.AccountID == account {
			environments[environment.ProjectID] = append(environments[environment.ProjectID], environment)
		}
		return nil
	}); err != nil {
		return view, err
	}
	err := visitMemTrafficRows(ctx, m.apps, change.Apps, func(app App) error {
		if change.GlobalRoutes || app.AccountID != account || app.Status == AppDeleted || api.NormalizeAppVisibility(app.Visibility) == api.AppVisibilityInternal {
			return nil
		}
		if host := hostidentity.BuildPrimaryAppHost(m.trafficAppsSuffix, app.Slug); host != "" {
			view.PrimaryHosts = append(view.PrimaryHosts, host)
			if err := checkMemTrafficAnalysisInputs(len(groups) + len(view.Environments) + len(view.PrimaryHosts) + len(view.AliasHosts)); err != nil {
				return err
			}
		}
		for _, environment := range environments[app.ProjectID] {
			if err := ctx.Err(); err != nil {
				return err
			}
			host := hostidentity.BuildEnvironmentHost(hostidentity.DeployWildcardSuffix, environment.ID, app.ID)
			if host == "" {
				return analysisLimit("environment_identity", "bindings", 0, 1)
			}
			key := projectEnvironmentRoutePolicyKey(app.ID, environment.Slug)
			policy, present := change.Policies[key]
			if !present {
				policy, present = m.projectEnvironmentEdgePolicies[key]
			}
			present = present && policy.AccountID == account && policy.ProjectID == app.ProjectID
			projection := trafficHostEnvironment{ID: environment.ID, App: app.ID, Host: host, Present: present}
			if present {
				var err error
				projection.ContractBytes, err = memTrafficProjectionSize("environment_edge_policy", environmentEdgeTrafficProjection(policy))
				if err != nil {
					return err
				}
				for _, rule := range ProjectEnvironmentPolicyRules(host, environment, app, nil, &policy) {
					if err := addRule(rule, environment.ID); err != nil {
						return err
					}
				}
			}
			view.Environments = append(view.Environments, projection)
			if err := checkMemTrafficAnalysisInputs(len(groups) + len(view.Environments) + len(view.PrimaryHosts) + len(view.AliasHosts)); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return view, err
	}
	if err := visitMemTrafficRows(ctx, m.deploymentAliases, change.Aliases, func(alias DeploymentAlias) error {
		if change.GlobalRoutes || m.trafficAppsSuffix == "" {
			return nil
		}
		app, found := change.Apps[alias.AppID]
		if !found {
			app, found = m.apps[alias.AppID]
		}
		if !found || app.AccountID != account || app.Status == AppDeleted || app.DeletedAt != nil || api.NormalizeAppVisibility(app.Visibility) == api.AppVisibilityInternal {
			return nil
		}
		deployment, found := m.deployments[alias.DeploymentID]
		if !found || deployment.AppID != alias.AppID || deployment.DeletedAt != nil || !deployment.DeploymentAliasActive() {
			return nil
		}
		// Runtime routing accepts existing SQL labels verbatim. Unlike new
		// allocation, legacy labels are not revalidated against the DNS cap.
		label, ok := hostidentity.DeploymentAliasLabel(app.ID, alias.Name)
		if !ok || !api.ValidDeploymentAliasName(alias.Name) {
			return nil
		}
		host := label + m.trafficAppsSuffix
		view.AliasHosts = append(view.AliasHosts, host)
		return checkMemTrafficAnalysisInputs(len(groups) + len(view.Environments) + len(view.PrimaryHosts) + len(view.AliasHosts))
	}); err != nil {
		return view, err
	}
	referenced := make(map[string]bool)
	for _, group := range groups {
		view.Groups = append(view.Groups, group)
		if group.Preset != "" {
			referenced[group.Preset] = true
		}
	}
	err = visitMemTrafficRows(ctx, m.corsPresets, change.Presets, func(preset CorsPreset) error {
		if preset.AccountID != account || !referenced[preset.ID] {
			return nil
		}
		encoded, err := json.Marshal(preset)
		if err != nil {
			return fmt.Errorf("state: encode in-memory traffic preset: %w", err)
		}
		view.Assets = append(view.Assets, trafficHostAsset{ID: preset.ID, Compiled: int64(len(encoded))})
		return checkMemTrafficAnalysisInputs(len(view.Groups) + len(view.Environments) + len(view.PrimaryHosts) + len(view.AliasHosts) + len(view.Assets))
	})
	if err != nil {
		return view, err
	}
	sort.Slice(view.Groups, func(i, j int) bool {
		a, b := view.Groups[i], view.Groups[j]
		return cmp.Or(cmp.Compare(a.App, b.App), cmp.Compare(a.Pattern, b.Pattern), cmp.Compare(a.Kind, b.Kind),
			cmp.Compare(a.Preset, b.Preset), cmp.Compare(a.Environment, b.Environment)) < 0
	})
	sort.Slice(view.Assets, func(i, j int) bool { return view.Assets[i].ID < view.Assets[j].ID })
	sort.Slice(view.Environments, func(i, j int) bool {
		a, b := view.Environments[i], view.Environments[j]
		return a.ID+"\x00"+a.App < b.ID+"\x00"+b.App
	})
	sort.Strings(view.PrimaryHosts)
	sort.Strings(view.AliasHosts)
	metadata, err := json.Marshal(view)
	if err != nil {
		return view, fmt.Errorf("state: encode in-memory traffic metadata: %w", err)
	}
	if bytes := int64(len(metadata)); bytes > api.TrafficPolicyMaxAnalysisMetadataBytes {
		return view, analysisLimit("metadata", "bytes", api.TrafficPolicyMaxAnalysisMetadataBytes, bytes)
	}
	return view, nil
}

func checkMemTrafficAnalysisInputs(inputs int) error {
	if inputs > api.TrafficPolicyMaxAnalysisInputs {
		return analysisLimit("inputs", "groups", api.TrafficPolicyMaxAnalysisInputs, int64(inputs))
	}
	return nil
}

func (m *MemStore) readBoundedMemTrafficAnalysisLocked(ctx context.Context, account string) (trafficHostAnalysis, error) {
	var before trafficHostAnalysis
	err := boundedTrafficPolicyAnalysis(ctx, func(bounded context.Context) error {
		var err error
		before, err = m.readMemTrafficHostAnalysisLocked(bounded, account, memTrafficPolicyChange{})
		return err
	})
	return before, err
}

func (m *MemStore) checkMemTrafficPolicyChangeLocked(ctx context.Context, account string, before trafficHostAnalysis, change memTrafficPolicyChange) error {
	return boundedTrafficPolicyAnalysis(ctx, func(bounded context.Context) error {
		after, err := m.readMemTrafficHostAnalysisLocked(bounded, account, change)
		if err != nil {
			return err
		}
		return checkTrafficHostAnalysis(bounded, before, after)
	})
}

func (m *MemStore) validateMemTrafficPolicyChangeLocked(ctx context.Context, account string, change memTrafficPolicyChange) error {
	before, err := m.readBoundedMemTrafficAnalysisLocked(ctx, account)
	if err != nil {
		return err
	}
	if err := m.checkMemTrafficPolicyChangeLocked(ctx, account, before, change); err != nil {
		return err
	}
	for _, rule := range change.Rules {
		if rule.Kind == EdgeRuleKindRoute {
			return m.validateMemGlobalTrafficChangeLocked(ctx, change)
		}
	}
	return nil
}

func (m *MemStore) validateMemGlobalTrafficChangeLocked(ctx context.Context, change memTrafficPolicyChange) error {
	var before trafficHostAnalysis
	err := boundedTrafficPolicyAnalysis(ctx, func(bounded context.Context) error {
		var err error
		before, err = m.readMemTrafficHostAnalysisLocked(bounded, "", memTrafficPolicyChange{GlobalRoutes: true})
		return err
	})
	if err != nil {
		return globalTrafficPolicyError(err)
	}
	change.GlobalRoutes = true
	return globalTrafficPolicyError(m.checkMemTrafficPolicyChangeLocked(ctx, "", before, change))
}

func (m *MemStore) validateMemAppTrafficChangeLocked(ctx context.Context, app App) error {
	if app.Status == AppDeleted || api.NormalizeAppVisibility(app.Visibility) == api.AppVisibilityInternal {
		return nil
	}
	return m.validateMemTrafficPolicyChangeLocked(ctx, app.AccountID, memTrafficPolicyChange{Apps: map[string]App{app.ID: app}})
}
