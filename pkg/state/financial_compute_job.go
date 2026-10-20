// adr: 1747 — immutable job claims bind funding to one physical attempt.
package state

import (
	"errors"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/financial"
)

type financialComputeJobAttempt struct {
	AccountID, JobID, RunID, InstanceID, NodeID, Incarnation, LeaseToken string
	TaskIndex, RAMMB                                                     int
}

func (a financialComputeJobAttempt) request(incarnation string) financial.ComputeRequest {
	return financial.ComputeRequest{AccountID: a.AccountID, JobID: a.JobID, JobRunID: a.RunID,
		JobTaskIndex: a.TaskIndex, InstanceID: a.InstanceID, NodeID: a.NodeID,
		Incarnation: incarnation, AttemptID: financial.ComputeJobAttemptID(a.RunID, a.TaskIndex, a.LeaseToken),
		Fenced: true, BillableMB: api.BillableRAMMBWithSidecars(a.RAMMB, nil)}
}

func computeJobClaimMatches(a financialComputeJobAttempt, r financial.ComputeRequest) bool {
	return a.AccountID == r.AccountID && (r.JobID == "" || a.JobID == r.JobID) && a.RunID == r.JobRunID &&
		a.TaskIndex == r.JobTaskIndex && a.NodeID == r.NodeID && a.Incarnation == r.Incarnation && a.InstanceID == r.InstanceID &&
		a.request(r.Incarnation).AttemptID == r.AttemptID && a.request(r.Incarnation).BillableMB == r.BillableMB && r.Fenced
}

func computeJobKey(key string) (string, bool) { return strings.CutPrefix(key, "job:") }

func computePolicyMatches(p FinancialBudget, key string) bool {
	return p.Spec.Scope.Kind == "account" || financial.ComputeScopeKey(p.Spec.Scope) == key
}

func prepareJobCompute(t financialComputeTxn, job string, initialize func() error, ensureAccount func([]FinancialBudget) error) error {
	key := "job:" + job
	policies, err := t.policies(key)
	if err != nil {
		return err
	}
	policies = strictComputePolicies(policies, key)
	if len(policies) == 0 {
		return nil
	}
	if err := ensureAccount(policies); err != nil {
		return err
	}
	month := financial.ComputeMonth(t.clock())
	b, err := t.book(key, month)
	if errors.Is(err, ErrNotFound) {
		if err = initialize(); err != nil {
			return err
		}
		b, err = t.book(key, month)
	}
	if err != nil {
		return err
	}
	if err := computeCheckPolicies(t, b, policies); err != nil {
		return err
	}
	for _, p := range policies {
		if financial.StrictAccountCompute(p.Spec) {
			b, err := t.book("", month)
			if err != nil {
				return err
			}
			if err := computeCheckPolicies(t, b, policies); err != nil {
				return err
			}
			break
		}
	}
	return nil
}
