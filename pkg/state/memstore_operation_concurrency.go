// adr: 20261018210500
package state

import (
	"context"
	"sort"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
)

type memOperationConcurrencyPolicy struct {
	accountID string
	policy    api.OperationConcurrencyPolicy
}

type memQueuedOperation struct {
	operationID, accountID, appID, scope, operation, tenantID string
	queuedAt                                                  time.Time
}

type memOperationQueueKey struct {
	accountID, appID, scope, operation, tenantID string
}

func (q memQueuedOperation) key() memOperationQueueKey {
	return memOperationQueueKey{q.accountID, q.appID, q.scope, q.operation, q.tenantID}
}

func (m *MemStore) operationConcurrencyLimitLocked(accountID, appID, scope, name, tenantID string) (int, bool) {
	if !api.OperationConcurrencyEnabled(accountID) {
		return 0, false
	}
	limit, limited := 0, false
	for _, p := range m.operationMemoryLocked().concurrencyPolicies {
		if p.policy.AppID != appID || p.policy.Scope != scope || p.policy.Operation != name {
			continue
		}
		if p.policy.PlatformTenantID == tenantID {
			return p.policy.MaxRunning, true
		}
		if p.policy.PlatformTenantID == "" {
			limit, limited = p.policy.MaxRunning, true
		}
	}
	return limit, limited
}

func (m *MemStore) operationInFlightLocked(appID, scope, name, tenantID string) int64 {
	data := m.operationMemoryLocked()
	var n int64
	for id, op := range data.operations {
		def := data.definitions[op.DefinitionID]
		if _, queued := data.queue[id]; queued || op.AppID != appID || op.PlatformTenantID != tenantID || def.Scope != scope || def.Spec.Name != name {
			continue
		}
		if operationInFlight(op.State) {
			n++
		}
	}
	return n
}

func (m *MemStore) operationQueuedLocked(k memOperationQueueKey) []memQueuedOperation {
	var rows []memQueuedOperation
	for _, q := range m.operationMemoryLocked().queue {
		if q.key() == k {
			rows = append(rows, q)
		}
	}
	sort.Slice(rows, func(i, j int) bool {
		if !rows[i].queuedAt.Equal(rows[j].queuedAt) {
			return rows[i].queuedAt.Before(rows[j].queuedAt)
		}
		return rows[i].operationID < rows[j].operationID
	})
	return rows
}

func (m *MemStore) operationConcurrencyHoldLocked(op Operation, def OperationDefinition) bool {
	if !operationConcurrencyApplies(def) {
		return false
	}
	limit, limited := m.operationConcurrencyLimitLocked(op.AccountID, def.AppID, def.Scope, def.Spec.Name, op.PlatformTenantID)
	queued := len(m.operationQueuedLocked(memOperationQueueKey{op.AccountID, def.AppID, def.Scope, def.Spec.Name, op.PlatformTenantID}))
	return operationConcurrencyHold(limit, limited, m.operationInFlightLocked(def.AppID, def.Scope, def.Spec.Name, op.PlatformTenantID), int64(queued))
}

func (m *MemStore) queueOperationLocked(op Operation, def OperationDefinition, now time.Time) {
	data := m.operationMemoryLocked()
	if data.queue == nil {
		data.queue = map[string]memQueuedOperation{}
	}
	data.queue[op.ID] = memQueuedOperation{operationID: op.ID, accountID: op.AccountID, appID: def.AppID, scope: def.Scope,
		operation: def.Spec.Name, tenantID: op.PlatformTenantID, queuedAt: now}
}

func (m *MemStore) PutOperationConcurrencyPolicy(_ context.Context, in OperationConcurrencyPolicyInput) (api.OperationConcurrencyPolicy, bool, error) {
	scope, err := validateOperationConcurrencyRequest(in.Request)
	if err != nil {
		return api.OperationConcurrencyPolicy{}, false, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	acct, ok := m.accounts[in.AccountID]
	if !ok {
		return api.OperationConcurrencyPolicy{}, false, ErrNotFound
	}
	if app, ok := m.apps[in.AppID]; !ok || app.AccountID != in.AccountID {
		return api.OperationConcurrencyPolicy{}, false, ErrNotFound
	}
	if in.Request.PlatformTenantID != "" {
		if tenant, ok := m.platformTenants[in.Request.PlatformTenantID]; !ok || tenant.AccountID != in.AccountID {
			return api.OperationConcurrencyPolicy{}, false, ErrNotFound
		}
	}
	data := m.operationMemoryLocked()
	if data.concurrencyPolicies == nil {
		data.concurrencyPolicies = map[string]memOperationConcurrencyPolicy{}
	}
	now := in.Now.UTC().Truncate(time.Microsecond)
	count := 0
	for id, p := range data.concurrencyPolicies {
		if p.policy.AppID != in.AppID {
			continue
		}
		count++
		if p.policy.Scope == scope && p.policy.Operation == in.Request.Operation && p.policy.PlatformTenantID == in.Request.PlatformTenantID {
			p.policy.MaxRunning, p.policy.UpdatedAt = in.Request.MaxRunning, now
			data.concurrencyPolicies[id] = p
			return p.policy, false, nil
		}
	}
	if limit := api.MustLimitsFor(acct.Plan).Operations.ConcurrencyPoliciesPerApp; count >= limit {
		return api.OperationConcurrencyPolicy{}, false, NewOperationLimitError("concurrency_policies_per_app", int64(limit), int64(count)+1)
	}
	policy := api.OperationConcurrencyPolicy{ID: uuid.NewString(), AppID: in.AppID, Scope: scope, Operation: in.Request.Operation,
		PlatformTenantID: in.Request.PlatformTenantID, MaxRunning: in.Request.MaxRunning, CreatedAt: now, UpdatedAt: now}
	data.concurrencyPolicies[policy.ID] = memOperationConcurrencyPolicy{accountID: in.AccountID, policy: policy}
	return policy, true, nil
}

func (m *MemStore) DeleteOperationConcurrencyPolicy(_ context.Context, accountID, appID, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	data := m.operationMemoryLocked()
	p, ok := data.concurrencyPolicies[id]
	if !ok || p.accountID != accountID || p.policy.AppID != appID {
		return ErrNotFound
	}
	delete(data.concurrencyPolicies, id)
	return nil
}

func (m *MemStore) OperationConcurrency(_ context.Context, accountID, appID string) (api.OperationConcurrencyResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	data := m.operationMemoryLocked()
	out := api.OperationConcurrencyResponse{Policies: []api.OperationConcurrencyPolicy{}, Queues: []api.OperationConcurrencyUsage{}}
	for _, p := range data.concurrencyPolicies {
		if p.accountID == accountID && p.policy.AppID == appID {
			out.Policies = append(out.Policies, p.policy)
		}
	}
	sort.Slice(out.Policies, func(i, j int) bool {
		a, b := out.Policies[i], out.Policies[j]
		if a.Scope != b.Scope || a.Operation != b.Operation {
			return a.Scope+"\x00"+a.Operation < b.Scope+"\x00"+b.Operation
		}
		return a.PlatformTenantID < b.PlatformTenantID
	})
	counts := map[memOperationQueueKey]int64{}
	for _, q := range data.queue {
		if q.accountID == accountID && q.appID == appID {
			counts[q.key()]++
		}
	}
	for k, queued := range counts {
		limit, limited := m.operationConcurrencyLimitLocked(accountID, appID, k.scope, k.operation, k.tenantID)
		if !limited {
			limit = 0
		}
		out.Queues = append(out.Queues, api.OperationConcurrencyUsage{Scope: k.scope, Operation: k.operation, PlatformTenantID: k.tenantID,
			Limit: limit, InFlight: m.operationInFlightLocked(appID, k.scope, k.operation, k.tenantID), Queued: queued})
	}
	sort.Slice(out.Queues, func(i, j int) bool {
		a, b := out.Queues[i], out.Queues[j]
		return a.Scope+"\x00"+a.Operation+"\x00"+a.PlatformTenantID < b.Scope+"\x00"+b.Operation+"\x00"+b.PlatformTenantID
	})
	return out, nil
}

func (m *MemStore) CustomerOperationQueue(_ context.Context, accountID, tenantID string) ([]api.OperationQueueEntry, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	keys := map[memOperationQueueKey]bool{}
	for _, q := range m.operationMemoryLocked().queue {
		if q.accountID == accountID && q.tenantID == tenantID {
			keys[q.key()] = true
		}
	}
	out := []api.OperationQueueEntry{}
	for k := range keys {
		for i, q := range m.operationQueuedLocked(k) {
			out = append(out, api.OperationQueueEntry{OperationID: q.operationID, AppID: q.appID, Scope: q.scope, Operation: q.operation, Position: int64(i + 1), QueuedAt: q.queuedAt})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].QueuedAt.Equal(out[j].QueuedAt) {
			return out[i].QueuedAt.Before(out[j].QueuedAt)
		}
		return out[i].OperationID < out[j].OperationID
	})
	return out, nil
}

func (m *MemStore) ReleaseQueuedOperations(_ context.Context, now time.Time) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	data := m.operationMemoryLocked()
	keys := map[memOperationQueueKey]bool{}
	for _, q := range data.queue {
		keys[q.key()] = true
	}
	released := 0
	for k := range keys {
		limit, limited := m.operationConcurrencyLimitLocked(k.accountID, k.appID, k.scope, k.operation, k.tenantID)
		rows := m.operationQueuedLocked(k)
		slots := operationQueueSlots(limit, limited, m.operationInFlightLocked(k.appID, k.scope, k.operation, k.tenantID), len(rows))
		started := 0
		for _, q := range rows {
			op := data.operations[q.operationID]
			if op.State != api.OperationAccepted {
				delete(data.queue, q.operationID)
				continue
			}
			if started >= slots {
				continue
			}
			m.releaseOperationDispatchLocked(op, now)
			delete(data.queue, q.operationID)
			started++
		}
		released += started
	}
	return released, nil
}

func (m *MemStore) releaseOperationDispatchLocked(op Operation, now time.Time) {
	if op.WorkflowRunID != "" {
		if run, ok := m.workflowRuns[op.WorkflowRunID]; ok && run.Status == WorkflowRunStatusPending && run.ScheduledFor.After(now) {
			run.ScheduledFor = now
			m.workflowRuns[run.ID] = run
		}
		return
	}
	if inv, ok := m.invocations[op.CurrentInvocationID]; ok && inv.State == InvocationPending && inv.DueAt.After(now) {
		inv.DueAt = now
		m.invocations[inv.ID] = inv
	}
}
