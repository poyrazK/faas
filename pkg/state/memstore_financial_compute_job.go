package state

import (
	"context"
	"errors"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/financial"
)

func (t *memFinancialComputeTxn) jobCurrent(l financial.ComputeLease) (bool, error) {
	a, ok := t.m.financialComputeJobAttempts[l.InstanceID]
	if !ok || a.AccountID != t.account {
		return false, nil
	}
	run := t.m.jobRuns[a.RunID]
	task := t.m.jobTasks[a.RunID][a.TaskIndex]
	i := t.m.instances[a.InstanceID]
	return run.AccountID == a.AccountID && run.JobID == a.JobID && t.m.jobs[a.JobID].AccountID == a.AccountID && (run.AggregateStatus == "queued" || run.AggregateStatus == "running") && t.m.jobs[a.JobID].Status == "active" &&
		task.Status == "claimed" && task.InstanceID != nil && *task.InstanceID == a.InstanceID &&
		task.LeaseToken != nil && *task.LeaseToken == a.LeaseToken && task.LeaseExpiresAt != nil && task.LeaseExpiresAt.After(t.now) &&
		i.JobID == a.JobID && i.NodeID == a.NodeID && i.RAMMB == a.RAMMB && State(i.State).CountsForRAM(), nil
}

func (m *MemStore) recordFinancialJobClaimLocked(a financialComputeJobAttempt) error {
	if m.jobs[a.JobID].AccountID != a.AccountID || m.jobRuns[a.RunID].AccountID != a.AccountID || m.jobRuns[a.RunID].JobID != a.JobID {
		return financial.ErrComputeFence
	}
	a.Incarnation = m.computeNodeRuntimeIncarnations[a.NodeID]
	if old, ok := m.financialComputeJobAttempts[a.InstanceID]; ok && old != a {
		return financial.ErrComputeFence
	}
	policies, _ := newMemComputeTxn(m, a.AccountID).policies("job:" + a.JobID)
	if len(strictComputePolicies(policies, "job:"+a.JobID)) > 0 {
		o, ok := m.financialComputeOwners[a.NodeID]
		if !ok || !m.computeNodes[a.NodeID].Active || o.Incarnation != m.computeNodeRuntimeIncarnations[a.NodeID] {
			return financial.ErrComputeUnavailable
		}
	}
	if m.financialComputeJobAttempts == nil {
		m.financialComputeJobAttempts = map[string]financialComputeJobAttempt{}
	}
	m.financialComputeJobAttempts[a.InstanceID] = a
	return nil
}

func (t *memFinancialComputeTxn) jobReady(job string) bool {
	m := t.m
	j, ok := m.jobs[job]
	if !ok || j.AccountID != t.account || j.Status == "deleted" {
		return false
	}
	since := maxComputeTime(financial.ComputeMonth(t.now), j.CreatedAt)
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
	for _, i := range m.instances {
		if i.JobID == job && State(i.State).CountsForRAM() {
			return false
		}
	}
	for run, tasks := range m.jobTasks {
		if m.jobRuns[run].JobID == job {
			for _, task := range tasks {
				if task.Status == "claimed" {
					return false
				}
			}
		}
	}
	for at := since.Truncate(time.Minute); at.Before(t.now.Truncate(time.Minute)); at = at.Add(time.Minute) {
		if !m.financialSamplingWindows[at].compute {
			return false
		}
	}
	active, err := t.active("job:" + job)
	return err == nil && len(active) == 0
}

func (t *memFinancialComputeTxn) initializeJob(job string) error {
	if !t.jobReady(job) {
		return financial.ErrComputeUnavailable
	}
	month := financial.ComputeMonth(t.now)
	b, err := t.book("job:"+job, month)
	if errors.Is(err, ErrNotFound) {
		b = financial.ComputeBook{AccountID: t.account, JobID: job, PeriodStart: month, PeriodEnd: month.AddDate(0, 1, 0)}
	} else if err != nil {
		return err
	}
	var total int64
	for _, row := range t.m.financialEvidence {
		e := row.Evidence
		if e.AccountID != t.account || e.Attribution.JobID != job || e.Meter != "compute" || e.Quantity <= 0 || e.Start.Before(month) || !e.Start.Before(b.PeriodEnd) {
			continue
		}
		var price financial.Price
		for _, p := range t.m.financialPrices {
			if p.AccountID == t.account && p.Price.Version == row.PriceVersion && p.Price.Meter == "compute" && p.PeriodStart.Equal(month) {
				price = p.Price
				break
			}
		}
		amount, err := computeBaselineAmount(e.Quantity, price)
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

func (m *MemStore) PrepareFinancialJobCompute(ctx context.Context, account, job string) error {
	return m.computeTransaction(ctx, account, func(t *memFinancialComputeTxn) error {
		return prepareJobCompute(t, job, func() error { return t.initializeJob(job) }, t.ensureAccount)
	})
}

func (m *MemStore) DeferFinancialJobClaim(ctx context.Context, run string, index int, instance, token string, next time.Time) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	task, ok := m.jobTasks[run][index]
	if !ok || task.Status != "claimed" || task.InstanceID == nil || *task.InstanceID != instance || task.LeaseToken == nil || *task.LeaseToken != token {
		return ErrNotFound
	}
	task.Status = "queued"
	task.InstanceID = nil
	task.LeaseToken = nil
	task.LeaseExpiresAt = nil
	task.LastLeaseNode = nil
	task.StartedAt = nil
	task.NextAttemptAt = &next
	task.ErrorClass = nil
	task.ErrorMessage = nil
	m.jobTasks[run][index] = task
	return nil
}
