// adr: 1750 — amendment 1: a strict project cap is a shared gross book that
// every app in the project funds alongside its own book.
package state

import (
	"errors"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/financial"
)

// computeProjectPolicies returns the enabled strict project policies of one
// project. A workload outside any project, and every job, has none.
func computeProjectPolicies(tx financialComputeTxn, project string) ([]FinancialBudget, error) {
	if project == "" {
		return nil, nil
	}
	rows, err := tx.policies(financial.ComputeProjectKey(project))
	if err != nil {
		return nil, err
	}
	out := []FinancialBudget{}
	for _, p := range rows {
		if p.DeletedAt == nil && p.Spec.Enabled && financial.StrictProjectCompute(p.Spec) && p.Spec.Scope.ID == project {
			out = append(out, p)
		}
	}
	return out, nil
}

// computeWorkloadPolicies is the policies FinancialComputePolicies reports for
// a workload: its own and the account's, plus its current project's. The
// scheduler, gateways and vmmd then treat a project cap like an app cap.
func computeWorkloadPolicies(tx financialComputeTxn, key string) ([]FinancialBudget, error) {
	rows, err := tx.policies(key)
	if err != nil {
		return nil, err
	}
	project, err := tx.workloadProject(key)
	if err != nil || project == "" {
		return rows, err
	}
	projectRows, err := tx.policies(financial.ComputeProjectKey(project))
	if err != nil {
		return nil, err
	}
	for _, p := range projectRows {
		if p.Spec.Scope.Kind == "project" {
			rows = append(rows, p)
		}
	}
	return rows, nil
}

// computeWorkloadStrictPolicies is every strict policy a new attempt of the
// workload must fund: its own, the account's and its current project's.
func computeWorkloadStrictPolicies(tx financialComputeTxn, key string) (string, []FinancialBudget, error) {
	rows, err := tx.policies(key)
	if err != nil {
		return "", nil, err
	}
	policies := strictComputePolicies(rows, key)
	project, err := tx.workloadProject(key)
	if err != nil {
		return "", nil, err
	}
	projectPolicies, err := computeProjectPolicies(tx, project)
	if err != nil {
		return "", nil, err
	}
	return project, append(policies, projectPolicies...), nil
}

// computeProjectFundingBook returns the project book a new strict attempt
// also funds. Every unsettled attempt in the project must already fund it:
// a mixed project means activation has not finished, which fails closed.
func computeProjectFundingBook(tx financialComputeTxn, l *financial.ComputeLease, project string, policies []FinancialBudget) (*financial.ComputeBook, error) {
	funded := false
	for _, p := range policies {
		funded = funded || financial.StrictProjectCompute(p.Spec) && p.Spec.Scope.ID == project
	}
	if !funded || project == "" {
		return nil, nil
	}
	key := financial.ComputeProjectKey(project)
	b, err := tx.book(key, l.PeriodStart)
	if err != nil {
		return nil, errors.Join(financial.ErrComputeUnavailable, err)
	}
	active, err := tx.active(key)
	if err != nil {
		return nil, err
	}
	for _, old := range active {
		if old.ProjectID != project || !old.PeriodStart.Equal(l.PeriodStart) {
			return nil, financial.ErrComputeUnavailable
		}
	}
	l.ProjectID = project
	return &b, nil
}

// computeFundProject moves the project book by the same gross amounts as
// the workload book: an attempt reserves wanted in place of its old reserve.
func computeFundProject(tx financialComputeTxn, b *financial.ComputeBook, l *financial.ComputeLease, wanted int64, policies []FinancialBudget) error {
	without := b.Reserved - l.Reserved
	if without < 0 {
		return financial.ErrComputeUnavailable
	}
	if wanted > api.FinancialBudgetMoneyMax-b.Committed || without > api.FinancialBudgetMoneyMax-b.Committed-wanted {
		return financial.ErrComputeBudget
	}
	b.Reserved = without + wanted
	return computeCheckPolicies(tx, *b, policies)
}

// computeLeaseProjectBook reads the project book an existing attempt funds.
func computeLeaseProjectBook(tx financialComputeTxn, l financial.ComputeLease) (*financial.ComputeBook, error) {
	if l.ProjectID == "" {
		return nil, nil
	}
	b, err := tx.book(financial.ComputeProjectKey(l.ProjectID), l.PeriodStart)
	if err != nil {
		return nil, err
	}
	return &b, nil
}

// computeBookPolicy reports whether a policy limits a book: the account book
// carries account policies, a project book its project's, and a workload
// book its app's or job's.
func computeBookPolicy(b financial.ComputeBook, p financial.BudgetSpec) bool {
	switch {
	case b.ProjectID != "":
		return financial.StrictProjectCompute(p) && p.Scope.ID == b.ProjectID
	case b.JobID != "":
		return financial.StrictJobCompute(p)
	case b.AppID != "":
		return financial.StrictAppCompute(p)
	}
	return financial.StrictAccountCompute(p)
}
