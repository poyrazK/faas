package state

import (
	"errors"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/financial"
)

// workloadProject mirrors PgStore: an app's current project; jobs have none.
func (t *memFinancialComputeTxn) workloadProject(key string) (string, error) {
	if _, ok := computeJobKey(key); ok || key == "" {
		return "", nil
	}
	app, ok := t.m.apps[key]
	if !ok || app.AccountID != t.account {
		return "", nil
	}
	return app.ProjectID, nil
}

// projectLeaseInScope reports whether an unsettled attempt belongs to a
// project's book: funded by it, or an app now in the project.
func (t *memFinancialComputeTxn) projectLeaseInScope(l financial.ComputeLease, project string) bool {
	if l.ProjectID == project {
		return true
	}
	app, ok := t.m.apps[l.AppID]
	return ok && l.AppID != "" && app.ProjectID == project
}

// projectReady mirrors PgStore.projectReady: capable owners, complete
// sampling since the project or month started, no unsettled attempt, and
// with quiet, no running instance or task of a project app.
func (t *memFinancialComputeTxn) projectReady(project string, quiet bool) bool {
	m := t.m
	p, ok := m.projects[project]
	if !ok || p.AccountID != t.account {
		return false
	}
	since := maxComputeTime(financial.ComputeMonth(t.now), p.CreatedAt)
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
			if app, ok := m.apps[i.AppID]; ok && app.ProjectID == project && State(i.State).CountsForRAM() {
				return false
			}
		}
		for _, task := range m.appTasks {
			if app, ok := m.apps[task.AppID]; ok && app.ProjectID == project && (task.Status == "restoring" || task.Status == "running") {
				return false
			}
		}
	}
	active, err := t.active(financial.ComputeProjectKey(project))
	return err == nil && len(active) == 0
}

// initializeProject seeds the month's project book with the project's
// retained gross charges, as initialize does for one app.
func (t *memFinancialComputeTxn) initializeProject(project string, quiet bool) error {
	if !t.projectReady(project, quiet) {
		return financial.ErrComputeUnavailable
	}
	month := financial.ComputeMonth(t.now)
	key := financial.ComputeProjectKey(project)
	b, err := t.book(key, month)
	if errors.Is(err, ErrNotFound) {
		b = financial.ComputeBook{AccountID: t.account, ProjectID: project, PeriodStart: month, PeriodEnd: month.AddDate(0, 1, 0)}
	} else if err != nil {
		return err
	}
	total := int64(0)
	for _, row := range t.m.financialEvidence {
		e := row.Evidence
		if e.AccountID != t.account || e.Attribution.ProjectID != project || e.Meter != "compute" || e.Quantity <= 0 || e.Start.Before(month) || !e.Start.Before(b.PeriodEnd) {
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

// ensureProject opens the month's project book for a capped project.
func (t *memFinancialComputeTxn) ensureProject(project string, policies []FinancialBudget) error {
	for _, p := range policies {
		if financial.StrictProjectCompute(p.Spec) && p.Spec.Scope.ID == project {
			if _, err := t.book(financial.ComputeProjectKey(project), financial.ComputeMonth(t.now)); !errors.Is(err, ErrNotFound) {
				return err
			}
			return t.initializeProject(project, false)
		}
	}
	return nil
}
