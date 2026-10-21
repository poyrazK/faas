package state

import (
	"context"
	"slices"
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/financial"
)

var _ FinancialBudgetStore = (*MemStore)(nil)

func (m *MemStore) ValidateFinancialBudgetScope(_ context.Context, account string, spec financial.BudgetSpec) error {
	if uuid.Validate(account) != nil || ValidateFinancialBudgetSpec(spec) != nil {
		return ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.billingResourceAccountLocked(account); !ok {
		return ErrNotFound
	}
	spec = m.resolveFinancialBudgetScopeLocked(spec)
	return m.financialBudgetScopeOwnedLocked(account, spec)
}

// The memory adapter uses compact UUID keys for some legacy resources. Resolve
// equivalent UUID spellings to the actual key before applying ownership checks.
func (m *MemStore) resolveFinancialBudgetScopeLocked(p financial.BudgetSpec) financial.BudgetSpec {
	match := func(id string) bool {
		a, e1 := uuid.Parse(id)
		b, e2 := uuid.Parse(p.Scope.ID)
		return e1 == nil && e2 == nil && a == b
	}
	switch p.Scope.Kind {
	case "app":
		for id := range m.apps {
			if match(id) {
				p.Scope.ID = id
				break
			}
		}
	case "job":
		for id := range m.jobs {
			if match(id) {
				p.Scope.ID = id
				break
			}
		}
	case "project":
		for id := range m.projects {
			if match(id) {
				p.Scope.ID = id
				break
			}
		}
	case "environment":
		for id := range m.projectEnvironments {
			if match(id) {
				p.Scope.ID = id
				break
			}
		}
	}
	return p
}

func (m *MemStore) financialBudgetScopeOwnedLocked(account string, p financial.BudgetSpec) error {
	s := p.Scope
	switch s.Kind {
	case "account":
		return nil
	case "app":
		app, ok := m.apps[s.ID]
		if !ok || app.AccountID != account || app.Status == AppDeleted {
			return ErrNotFound
		}
		if p.Action == "stop_previews" && app.PreviewOfSlug == "" {
			return ErrInvalidArgument
		}
		if p.Action == "suspend_background" && app.WorkloadClass != WorkloadClassWorker && app.WorkloadClass != WorkloadClassJob {
			return ErrInvalidArgument
		}
		return nil
	case "job":
		if job, ok := m.jobs[s.ID]; ok && job.AccountID == account && job.Status != "deleted" {
			return nil
		}
	case "project":
		if project, ok := m.projects[s.ID]; ok && project.AccountID == account {
			return nil
		}
	case "environment":
		for _, environment := range m.projectEnvironments {
			if environment.ID == s.ID && environment.AccountID == account {
				return nil
			}
		}
	}
	return ErrNotFound
}

func (m *MemStore) CreateFinancialBudget(_ context.Context, account, id, actor string, spec financial.BudgetSpec) (FinancialBudget, error) {
	spec = NormalizeFinancialBudgetSpec(spec)
	if !validFinancialBudgetMutation(account, id, actor, 0) || ValidateFinancialBudgetSpec(spec) != nil {
		return FinancialBudget{}, ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.billingResourceAccountLocked(account); !ok {
		return FinancialBudget{}, ErrNotFound
	}
	spec = m.resolveFinancialBudgetScopeLocked(spec)
	if err := m.financialBudgetScopeOwnedLocked(account, spec); err != nil {
		return FinancialBudget{}, err
	}
	if _, ok := m.financialBudgets[id]; ok {
		return FinancialBudget{}, ErrConflict
	}
	var count int
	for _, p := range m.financialBudgets {
		if p.AccountID == account && p.DeletedAt == nil {
			count++
		}
	}
	if count >= api.FinancialBudgetsPerAccount {
		return FinancialBudget{}, ErrFinancialBudgetLimit
	}
	compute := newMemComputeTxn(m, account)
	if err := memFinancialComputeActivation(compute, spec, false); err != nil {
		return FinancialBudget{}, err
	}
	if m.financialBudgets == nil {
		m.financialBudgets = map[string]FinancialBudget{}
		m.financialBudgetRevisions = map[string][]FinancialBudgetRevision{}
	}
	now := time.Now().UTC()
	p := FinancialBudget{ID: id, AccountID: account, Revision: 1, Spec: cloneFinancialBudgetSpec(spec), CreatedAt: now, UpdatedAt: now}
	m.recordFinancialBudgetLocked(p, actor, "created")
	compute.commit()
	return cloneFinancialBudget(p), nil
}

func (m *MemStore) UpdateFinancialBudget(_ context.Context, account, id string, expected int64, actor string, spec financial.BudgetSpec) (FinancialBudget, error) {
	spec = NormalizeFinancialBudgetSpec(spec)
	if expected < 1 || !validFinancialBudgetMutation(account, id, actor, expected) || ValidateFinancialBudgetSpec(spec) != nil {
		return FinancialBudget{}, ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	p, ok := m.financialBudgets[id]
	if !ok || p.AccountID != account {
		return FinancialBudget{}, ErrNotFound
	}
	if p.Revision != expected || p.DeletedAt != nil {
		return FinancialBudget{}, ErrConflict
	}
	spec = m.resolveFinancialBudgetScopeLocked(spec)
	if err := m.financialBudgetScopeOwnedLocked(account, spec); err != nil {
		return FinancialBudget{}, err
	}
	compute := newMemComputeTxn(m, account)
	continuing := p.Spec.Enabled && financial.StrictCompute(p.Spec) && p.Spec.Scope == spec.Scope && slices.Equal(p.Spec.Meters, spec.Meters)
	if err := memFinancialComputeActivation(compute, spec, continuing); err != nil {
		return FinancialBudget{}, err
	}
	p.Spec, p.Revision, p.UpdatedAt = cloneFinancialBudgetSpec(spec), p.Revision+1, time.Now().UTC()
	m.recordFinancialBudgetLocked(p, actor, "updated")
	compute.commit()
	return cloneFinancialBudget(p), nil
}

func memFinancialComputeActivation(t *memFinancialComputeTxn, spec financial.BudgetSpec, continuing bool) error {
	if !spec.Enabled || spec.Mode != "strict" {
		return nil
	}
	if !financial.StrictCompute(spec) {
		return ErrInvalidArgument
	}
	if !continuing {
		if err := t.initialize(financial.ComputeScopeKey(spec.Scope), true); err != nil {
			return err
		}
	}
	if financial.StrictAccountEgress(spec) && !continuing {
		if err := t.initializeEgress(); err != nil {
			return err
		}
	}
	b, err := t.book(financial.ComputeScopeKey(spec.Scope), financial.ComputeMonth(t.now))
	if err == nil {
		b.Committed, b.Reserved, err = computePolicyMoney(b, spec)
	}
	return validateComputePolicyFunding(b, err, continuing, spec.LimitMillicents)
}

func (m *MemStore) DeleteFinancialBudget(_ context.Context, account, id string, expected int64, actor string) (FinancialBudget, error) {
	if expected < 1 || !validFinancialBudgetMutation(account, id, actor, expected) {
		return FinancialBudget{}, ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	p, ok := m.financialBudgets[id]
	if !ok || p.AccountID != account {
		return FinancialBudget{}, ErrNotFound
	}
	if p.Revision != expected || p.DeletedAt != nil {
		return FinancialBudget{}, ErrConflict
	}
	now := time.Now().UTC()
	p.Revision, p.UpdatedAt, p.DeletedAt = p.Revision+1, now, &now
	m.recordFinancialBudgetLocked(p, actor, "deleted")
	return cloneFinancialBudget(p), nil
}

func (m *MemStore) recordFinancialBudgetLocked(p FinancialBudget, actor, mutation string) {
	m.financialBudgets[p.ID] = cloneFinancialBudget(p)
	m.financialBudgetRevisions[p.ID] = append(m.financialBudgetRevisions[p.ID], FinancialBudgetRevision{AccountID: p.AccountID, PolicyID: p.ID, Revision: p.Revision, Actor: actor, Mutation: mutation, Spec: cloneFinancialBudgetSpec(p.Spec), RecordedAt: p.UpdatedAt})
}

func (m *MemStore) GetFinancialBudget(_ context.Context, account, id string) (FinancialBudget, error) {
	if uuid.Validate(account) != nil || uuid.Validate(id) != nil {
		return FinancialBudget{}, ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	p, ok := m.financialBudgets[id]
	if !ok || p.AccountID != account {
		return FinancialBudget{}, ErrNotFound
	}
	return cloneFinancialBudget(p), nil
}

func (m *MemStore) ListFinancialBudgets(_ context.Context, account string) ([]FinancialBudget, error) {
	if uuid.Validate(account) != nil {
		return nil, ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	out := []FinancialBudget{}
	for _, p := range m.financialBudgets {
		if p.AccountID == account && p.DeletedAt == nil {
			out = append(out, cloneFinancialBudget(p))
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

func (m *MemStore) ListFinancialBudgetRevisions(_ context.Context, account, id string, after int64, limit int) ([]FinancialBudgetRevision, error) {
	if uuid.Validate(account) != nil || uuid.Validate(id) != nil || after < 0 || after > api.FinancialBudgetRevisionMax || limit < 1 || limit > api.FinancialBudgetHistoryMax {
		return nil, ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	out := []FinancialBudgetRevision{}
	for _, row := range m.financialBudgetRevisions[id] {
		if row.AccountID == account && row.Revision > after {
			row.Spec = cloneFinancialBudgetSpec(row.Spec)
			out = append(out, row)
			if len(out) == limit {
				break
			}
		}
	}
	return out, nil
}
