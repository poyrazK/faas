package state

import (
	"context"
)

// DeploymentsByIDsReader reads many deployment rows in one query (ADR
// 20261102205122). schedd's reaper resolved each live deployment's policy with
// a DeploymentByID per deployment on every tick. A deployment that does not
// exist is absent from the map; callers fall back to DeploymentByID when the
// store does not implement it.
type DeploymentsByIDsReader interface {
	DeploymentsByIDs(ctx context.Context, ids []string) (map[string]Deployment, error)
}

// FinancialBudgetAccountsReader reports which of a set of accounts hold any
// budget policy (ADR 20261102205122). FinancialComputePolicies reads only an
// account's undeleted policies, so an account outside the result has none and
// the reaper's strict compute sweep has no hold to apply to its apps.
type FinancialBudgetAccountsReader interface {
	AccountsWithFinancialBudgets(ctx context.Context, accountIDs []string) (map[string]bool, error)
}

// DeploymentsByIDs reads the deployments with the columns DeploymentByID reads.
func (s *PgStore) DeploymentsByIDs(ctx context.Context, ids []string) (map[string]Deployment, error) {
	valid := make([]string, 0, len(ids))
	for _, id := range ids {
		if validUUID(id) {
			valid = append(valid, id)
		}
	}
	out := make(map[string]Deployment, len(valid))
	if len(valid) == 0 {
		return out, nil
	}
	rows, err := s.pool.Query(ctx,
		`select `+deploymentSelectColumnsWithRootfs+`
		 from deployments where id = any($1::uuid[])`, valid)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		d, err := scanDeploymentWithRootfs(rows)
		if err != nil {
			return nil, err
		}
		out[d.ID] = d
	}
	return out, rows.Err()
}

// AccountsWithFinancialBudgets reads, through
// financial_budget_policies_account_idx, the accounts with an undeleted budget
// policy.
func (s *PgStore) AccountsWithFinancialBudgets(ctx context.Context, accountIDs []string) (map[string]bool, error) {
	valid := make([]string, 0, len(accountIDs))
	for _, id := range accountIDs {
		if validUUID(id) {
			valid = append(valid, id)
		}
	}
	out := make(map[string]bool)
	if len(valid) == 0 {
		return out, nil
	}
	rows, err := s.pool.Query(ctx,
		`select distinct account_id::text from financial_budget_policies
		  where account_id = any($1::uuid[]) and deleted_at is null`, valid)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out[id] = true
	}
	return out, rows.Err()
}

// DeploymentsByIDs mirrors PgStore.DeploymentsByIDs.
func (m *MemStore) DeploymentsByIDs(_ context.Context, ids []string) (map[string]Deployment, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make(map[string]Deployment, len(ids))
	for _, id := range ids {
		if d, ok := m.deployments[id]; ok {
			out[id] = d
		}
	}
	return out, nil
}

// AccountsWithFinancialBudgets mirrors PgStore.AccountsWithFinancialBudgets.
func (m *MemStore) AccountsWithFinancialBudgets(_ context.Context, accountIDs []string) (map[string]bool, error) {
	want := make(map[string]struct{}, len(accountIDs))
	for _, id := range accountIDs {
		want[id] = struct{}{}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make(map[string]bool)
	for _, p := range m.financialBudgets {
		if _, ok := want[p.AccountID]; ok && p.DeletedAt == nil {
			out[p.AccountID] = true
		}
	}
	return out, nil
}
