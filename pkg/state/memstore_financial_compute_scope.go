package state

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/financial"
)

// workloadScopes mirrors PgStore: an app's current project and the
// environment its deployment runs in; jobs have neither.
func (t *memFinancialComputeTxn) workloadScopes(key, instance, deployment string) (computeScopes, error) {
	if _, ok := computeJobKey(key); ok || key == "" {
		return computeScopes{}, nil
	}
	if _, ok := computeSharedScopeOf(key); ok {
		return computeScopes{}, nil
	}
	app, ok := t.m.apps[key]
	if !ok || app.AccountID != t.account {
		return computeScopes{}, nil
	}
	if ins, ok := t.m.instances[instance]; ok && ins.AppID == app.ID && ins.DeploymentID != "" {
		deployment = ins.DeploymentID
	}
	if d, ok := t.m.deployments[deployment]; !ok || d.AppID != app.ID {
		deployment = ""
	}
	return computeScopes{Project: app.ProjectID, Deployment: deployment, Environment: t.m.computeDeploymentEnvironmentLocked(deployment)}, nil
}

// computeDeploymentEnvironmentLocked mirrors the SQL function
// financial_compute_deployment_environment: the deployment's runtime owner,
// else the project's environment of the same name.
func (m *MemStore) computeDeploymentEnvironmentLocked(deployment string) string {
	d, ok := m.deployments[deployment]
	if !ok {
		return ""
	}
	if owner := m.deploymentRuntimeEnvironmentOwners[deployment]; owner != "" {
		return owner
	}
	app, ok := m.apps[d.AppID]
	if !ok || app.ProjectID == "" {
		return ""
	}
	slug := workloadEnvironmentSlug(d.Scope)
	for _, env := range m.projectEnvironments {
		if env.ProjectID == app.ProjectID && env.Slug == slug {
			return env.ID
		}
	}
	return ""
}

func (t *memFinancialComputeTxn) projectEnvironments(project string) ([]string, error) {
	out := []string{}
	for _, env := range t.m.projectEnvironments {
		if project != "" && env.ProjectID == project && env.AccountID == t.account {
			out = append(out, env.ID)
		}
	}
	return out, nil
}

// scopeLeaseInScope reports whether an unsettled attempt belongs to a shared
// book: funded by it, or an app now in the project, or a deployment now in
// the environment.
func (t *memFinancialComputeTxn) scopeLeaseInScope(l financial.ComputeLease, s computeSharedScope) bool {
	if leaseScopes(l).id(s.kind) == s.id {
		return true
	}
	app, ok := t.m.apps[l.AppID]
	if !ok || l.AppID == "" {
		return false
	}
	if s.kind == "project" {
		return app.ProjectID == s.id
	}
	deployment := l.DeploymentID
	if ins, ok := t.m.instances[l.InstanceID]; ok && ins.DeploymentID != "" {
		deployment = ins.DeploymentID
	}
	return t.m.computeDeploymentEnvironmentLocked(deployment) == s.id
}

// scopeOwned reports whether a scope's project or environment exists in the
// account, with the time its sampling must cover from.
func (t *memFinancialComputeTxn) scopeOwned(s computeSharedScope) (time.Time, string, bool) {
	if s.kind == "project" {
		p, ok := t.m.projects[s.id]
		return p.CreatedAt, s.id, ok && p.AccountID == t.account
	}
	env, ok := t.m.projectEnvironments[s.id]
	if !ok || env.AccountID != t.account {
		return time.Time{}, "", false
	}
	since := env.CreatedAt
	for _, app := range t.m.apps {
		if app.ProjectID == env.ProjectID && app.CreatedAt.Before(since) {
			since = app.CreatedAt
		}
	}
	return since, env.ProjectID, true
}

// inScope reports whether a project app instance or task with this
// deployment counts toward a shared book; an unknown deployment counts
// toward every environment of its project.
func (t *memFinancialComputeTxn) inScope(s computeSharedScope, project, appID, deployment string) bool {
	app, ok := t.m.apps[appID]
	if !ok || app.ProjectID != project {
		return false
	}
	return s.kind == "project" || deployment == "" || t.m.computeDeploymentEnvironmentLocked(deployment) == s.id
}

// scopeReady mirrors PgStore.scopeReady: capable owners, complete sampling
// since the scope or month started, no unsettled attempt, and with quiet,
// no running instance or task in the scope.
func (t *memFinancialComputeTxn) scopeReady(s computeSharedScope, quiet bool) bool {
	m := t.m
	created, project, ok := t.scopeOwned(s)
	if !ok {
		return false
	}
	since := maxComputeTime(financial.ComputeMonth(t.now), created)
	if m.financialRetainedFrom.After(since) {
		return false
	}
	owners := 0
	for id, n := range m.computeNodes {
		if !n.Active {
			continue
		}
		owners++
		o, ok := m.financialComputeOwners[id]
		if !ok || o.Incarnation != m.computeNodeRuntimeIncarnations[id] || t.now.Sub(o.ObservedAt) > 3*api.FinancialComputeOwnerHeartbeat {
			return false
		}
	}
	if owners == 0 {
		return false
	}
	for minute := since.Truncate(time.Minute); minute.Before(t.now.Truncate(time.Minute)); minute = minute.Add(time.Minute) {
		if !m.financialSamplingWindows[minute].compute {
			return false
		}
	}
	if quiet {
		for _, i := range m.instances {
			if State(i.State).CountsForRAM() && t.inScope(s, project, i.AppID, i.DeploymentID) {
				return false
			}
		}
		for _, task := range m.appTasks {
			if (task.Status == "restoring" || task.Status == "running") && task.DeploymentID != "" && t.inScope(s, project, task.AppID, task.DeploymentID) {
				return false
			}
		}
	}
	active, err := t.active(s.key())
	return err == nil && len(active) == 0
}

// initializeScope seeds the month's shared book with the scope's retained
// gross charges, as initialize does for one app.
func (t *memFinancialComputeTxn) initializeScope(s computeSharedScope, quiet bool) error {
	if !t.scopeReady(s, quiet) {
		return financial.ErrComputeUnavailable
	}
	month := financial.ComputeMonth(t.now)
	b, err := t.book(s.key(), month)
	if errors.Is(err, ErrNotFound) {
		b = financial.ComputeBook{AccountID: t.account, PeriodStart: month, PeriodEnd: month.AddDate(0, 1, 0)}
		if s.kind == "project" {
			b.ProjectID = s.id
		} else {
			b.EnvironmentID = s.id
		}
	} else if err != nil {
		return err
	}
	total := int64(0)
	for _, row := range t.m.financialEvidence {
		e := row.Evidence
		if e.AccountID != t.account || e.Meter != "compute" || e.Quantity <= 0 || e.Start.Before(month) || !e.Start.Before(b.PeriodEnd) {
			continue
		}
		if s.kind == "project" && e.Attribution.ProjectID != s.id || s.kind == "environment" && e.Attribution.EnvironmentID != s.id && t.m.computeDeploymentEnvironmentLocked(e.Attribution.DeploymentID) != s.id {
			continue
		}
		var p FinancialPriceSnapshot
		for _, candidate := range t.m.financialPrices {
			if candidate.AccountID == t.account && candidate.Price.Version == row.PriceVersion && candidate.Price.Meter == "compute" {
				p = candidate
				break
			}
		}
		amount, err := computeBaselineAmount(e.Quantity, p.Price)
		if err != nil {
			return err
		}
		if amount > api.FinancialBudgetMoneyMax-total {
			return financial.ErrOverflow
		}
		total += amount
	}
	b.Committed = max(b.Committed, total)
	return t.putBook(b)
}

func (t *memFinancialComputeTxn) ensureScopes(scopes computeScopes, policies []FinancialBudget) error {
	return computeEnsureScopes(t, scopes, policies, t.initialize)
}

// FinancialComputeDeploymentEnvironment mirrors PgStore.
func (m *MemStore) FinancialComputeDeploymentEnvironment(ctx context.Context, account, app, deployment string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if uuid.Validate(account) != nil || uuid.Validate(app) != nil || uuid.Validate(deployment) != nil {
		return "", ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	d, ok := m.deployments[deployment]
	a, appOK := m.apps[app]
	if !ok || !appOK || d.AppID != app || a.AccountID != account {
		return "", ErrNotFound
	}
	return m.computeDeploymentEnvironmentLocked(deployment), nil
}
