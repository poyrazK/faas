package state

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// ADR-20261023215834: a settled receipt is kept for its owner's plan window,
// which also bounds duplicate acceptance, event recovery and replay ranges.

// eventSettledRetentionForPlan is the plan's settled-receipt window. An
// unknown plan keeps the longest window, so no receipt is pruned or
// reported expiring earlier than any plan allows.
func eventSettledRetentionForPlan(plan api.Plan) time.Duration {
	if l, ok := api.LimitsFor(plan); ok {
		return l.EventStorage.SettledRetention()
	}
	return api.EventSettledRetentionMax()
}

// eventSettledRetention resolves an event owner's window: a customer account
// or a resource-owning organization.
func eventSettledRetention(ctx context.Context, q *sqlc.Queries, db sqlc.DBTX, owner string) (time.Duration, error) {
	plan, err := q.ObjectUsageAccountPlan(ctx, db, mustPgUUID(owner))
	if errors.Is(err, pgx.ErrNoRows) {
		return api.EventSettledRetentionMax(), nil
	}
	if err != nil {
		return 0, fmt.Errorf("read event owner plan: %w", err)
	}
	return eventSettledRetentionForPlan(api.Plan(plan)), nil
}

func (m *MemStore) eventSettledRetentionLocked(owner string) time.Duration {
	plan, ok := m.invocationOwnerPlanLocked(owner)
	if !ok {
		return api.EventSettledRetentionMax()
	}
	return eventSettledRetentionForPlan(plan)
}

// memEventFanoutOwner is the owner part of a MemStore receipt key.
func memEventFanoutOwner(key string) string {
	owner, _, _ := strings.Cut(key, "\x00")
	return owner
}

// eventSettledRetentionPlans is every plan's window in seconds, in the
// parallel-array form plan-aware SQL takes.
func eventSettledRetentionPlans() (plans []string, seconds []int64) {
	for _, p := range api.Plans {
		plans = append(plans, string(p))
		seconds = append(seconds, int64(eventSettledRetentionForPlan(p)/time.Second))
	}
	return plans, seconds
}
