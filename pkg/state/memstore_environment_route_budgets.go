package state

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/routemonitor"
)

type memoryBudgetReceipt struct {
	Hash     string
	Response api.EnvironmentRouteBudgets
}

func cloneEnvironmentBudgets(in api.EnvironmentRouteBudgets) api.EnvironmentRouteBudgets {
	in.Config.Routes = routemonitor.CloneRoutes(in.Config.Routes)
	if in.Config.UpdatedAt != nil {
		at := *in.Config.UpdatedAt
		in.Config.UpdatedAt = &at
	}
	return in
}
func memoryBudgetKey(app, scope, environment string) string {
	return app + "\x00" + scope + "\x00" + environment
}
func (m *MemStore) environmentBudgetsLocked(account, app, scope string) (api.EnvironmentRouteBudgets, error) {
	out := api.EnvironmentRouteBudgets{AppID: app, Scope: scope, BudgetSource: "app_policy"}
	parent, err := m.routeMonitorConfigLocked(account, app)
	if err != nil {
		return out, err
	}
	if scope != api.DefaultEnvScope {
		if !api.ValidProjectEnvironmentSlug(scope) {
			return out, ErrInvalidArgument
		}
		env, err := m.projectEnvironmentBySlugLocked(m.apps[app].ProjectID, scope)
		if err != nil || env.AccountID != account {
			return out, ErrNotFound
		}
		out.ProjectID, out.EnvironmentID = env.ProjectID, env.ID
	}
	parent.CustomerGroupBy, parent.OnViolation = "", ""
	out.AppRevision, out.Config = parent.Revision, parent
	saved, ok := m.environmentBudgetConfigs[memoryBudgetKey(app, scope, out.EnvironmentID)]
	if !ok {
		return out, nil
	}
	out.Revision = saved.Revision
	if !saved.Inherit {
		out.BudgetSource = "environment_policy"
		out.Config = api.RouteMonitorConfig{AppID: app, Enabled: saved.Enabled, Revision: saved.Revision, Routes: routemonitor.CloneRoutes(saved.Routes), UpdatedAt: &saved.UpdatedAt}
	} else if out.Config.UpdatedAt == nil || saved.UpdatedAt.After(*out.Config.UpdatedAt) {
		out.Config.UpdatedAt = &saved.UpdatedAt
	}
	return out, nil
}
func (m *MemStore) GetEnvironmentRouteBudgets(_ context.Context, account, app, scope string) (api.EnvironmentRouteBudgets, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.environmentBudgetsLocked(account, app, scope)
}
func (m *MemStore) SetEnvironmentRouteBudgets(_ context.Context, account, app, scope string, req api.SetEnvironmentRouteBudgetsRequest) (api.EnvironmentRouteBudgets, error) {
	if routemonitor.ValidateEnvironmentBudgets(req) != nil || req.AppID != app {
		return api.EnvironmentRouteBudgets{}, ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	current, err := m.environmentBudgetsLocked(account, app, scope)
	if err != nil {
		return current, err
	}
	if current.EnvironmentID != req.EnvironmentID || current.ProjectID != req.ProjectID {
		return current, ErrRouteHealthRevision
	}
	raw, err := json.Marshal(struct {
		Scope   string
		Request api.SetEnvironmentRouteBudgetsRequest
	}{scope, req})
	if err != nil {
		return current, err
	}
	digest := sha256.Sum256(raw)
	hash := hex.EncodeToString(digest[:])
	key := account + "\x00" + req.RequestID
	if saved, ok := m.environmentBudgetReceipts[key]; ok {
		if saved.Hash != hash {
			return current, ErrConflict
		}
		return cloneEnvironmentBudgets(saved.Response), nil
	}
	if current.Revision != *req.ExpectedRevision || current.AppRevision != *req.ExpectedAppRevision || current.Revision >= api.RouteRequirementsMaxRevision {
		return current, ErrRouteHealthRevision
	}
	a := m.accounts[account]
	if !a.MayDeploy() || (req.Enabled || req.Inherit && current.Config.Enabled) && !a.Plan.DebugTelemetryEnabled() {
		return current, ErrRouteInvestigationPlan
	}
	if m.environmentBudgetConfigs == nil {
		m.environmentBudgetConfigs = map[string]scopedBudgetRow{}
		m.environmentBudgetReceipts = map[string]memoryBudgetReceipt{}
	}
	m.environmentBudgetConfigs[memoryBudgetKey(app, scope, current.EnvironmentID)] = scopedBudgetRow{Inherit: req.Inherit, Enabled: req.Enabled, Revision: current.Revision + 1, Routes: routemonitor.CloneRoutes(req.Routes), UpdatedAt: time.Now().UTC()}
	updated, err := m.environmentBudgetsLocked(account, app, scope)
	if err != nil {
		return current, err
	}
	m.environmentBudgetReceipts[key] = memoryBudgetReceipt{Hash: hash, Response: cloneEnvironmentBudgets(updated)}
	return updated, nil
}

func (m *MemStore) deleteEnvironmentBudgetsForAppLocked(app string) {
	for key := range m.environmentBudgetConfigs {
		if strings.HasPrefix(key, app+"\x00") {
			delete(m.environmentBudgetConfigs, key)
		}
	}
	for key, receipt := range m.environmentBudgetReceipts {
		if receipt.Response.AppID == app {
			delete(m.environmentBudgetReceipts, key)
		}
	}
}
func (m *MemStore) deleteEnvironmentBudgetConfigsLocked(environment string) {
	for key := range m.environmentBudgetConfigs {
		if strings.HasSuffix(key, "\x00"+environment) {
			delete(m.environmentBudgetConfigs, key)
		}
	}
}
