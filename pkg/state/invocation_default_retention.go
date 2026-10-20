package state

import (
	"context"
	"sort"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// InvocationDefaultRetentionStore stamps the plan-default retention horizon
// on finished invocations whose producer set none (ADR 20261019104819).
// Queue sends, the app inbox, crons, event deliveries, async edge routes and
// inbound webhooks enqueue without a horizon, and the reaper only prunes rows
// that have one, so those rows were kept for as long as the app lived.
type InvocationDefaultRetentionStore interface {
	StampDefaultInvocationRetention(ctx context.Context, limit int) (int, error)
}

// InvocationRetentionFailureFloor is the shortest horizon a stamped failed
// or dead-lettered row gets. The unified dead-letter projection and event
// execution recovery both read the row for this long, so short-retention
// plans must not lose it first.
const InvocationRetentionFailureFloor = api.DefaultDeadLetterRetention

// invocationTerminalStates are the states the stamp and the reaper act on.
var invocationTerminalStates = map[InvocationState]bool{
	InvocationCompleted: true, InvocationFailed: true, InvocationDeadLetter: true,
	InvocationCancelled: true, InvocationSuperseded: true, InvocationExpired: true,
}

func defaultInvocationRetention(plan api.Plan, state InvocationState) (time.Duration, bool) {
	limits, ok := api.LimitsFor(plan)
	if !ok {
		return 0, false
	}
	retention := time.Duration(limits.MaxAsyncResultRetentionSeconds) * time.Second
	if (state == InvocationFailed || state == InvocationDeadLetter) && retention < InvocationRetentionFailureFloor {
		retention = InvocationRetentionFailureFloor
	}
	return retention, true
}

func (s *PgStore) StampDefaultInvocationRetention(ctx context.Context, limit int) (int, error) {
	if limit <= 0 {
		limit = 500
	}
	params := sqlc.StampDefaultInvocationRetentionParams{
		FailureFloorSeconds: int64(InvocationRetentionFailureFloor / time.Second),
		BatchLimit:          int32(limit),
	}
	for _, plan := range api.Plans {
		params.Plans = append(params.Plans, string(plan))
		params.RetentionSeconds = append(params.RetentionSeconds, int64(api.MustLimitsFor(plan).MaxAsyncResultRetentionSeconds))
	}
	n, err := sqlc.New().StampDefaultInvocationRetention(ctx, s.pool, params)
	return int(n), err
}

func (m *MemStore) StampDefaultInvocationRetention(_ context.Context, limit int) (int, error) {
	if limit <= 0 {
		limit = 500
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	var due []Invocation
	for _, inv := range m.invocations {
		if inv.ResultRetentionUntil == nil && invocationTerminalStates[inv.State] {
			due = append(due, inv)
		}
	}
	sort.Slice(due, func(i, j int) bool { return due[i].CreatedAt.Before(due[j].CreatedAt) })
	stamped := 0
	for _, inv := range due {
		if stamped >= limit {
			break
		}
		plan, ok := m.invocationOwnerPlanLocked(inv.AccountID)
		if !ok {
			continue
		}
		retention, ok := defaultInvocationRetention(plan, inv.State)
		if !ok {
			continue
		}
		settled := inv.CreatedAt
		if inv.CompletedAt != nil {
			settled = *inv.CompletedAt
		}
		until := settled.Add(retention)
		inv.ResultRetentionUntil = &until
		m.setInvocationLocked(inv.ID, inv)
		stamped++
	}
	return stamped, nil
}

// invocationOwnerPlanLocked mirrors ObjectUsageAccountPlan: an invocation's
// account_id is a customer account or a resource-owning organization.
func (m *MemStore) invocationOwnerPlanLocked(ownerID string) (api.Plan, bool) {
	if account, ok := m.accounts[ownerID]; ok {
		return account.Plan, true
	}
	if org, ok := m.orgs[ownerID]; ok && !org.Personal && org.ResourceOwnershipEnabled {
		return org.Plan, true
	}
	return "", false
}
