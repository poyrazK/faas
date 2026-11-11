package apidserver

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/billing"
	"github.com/onebox-faas/faas/pkg/managedpostgres"
	"github.com/onebox-faas/faas/pkg/meter"
	"github.com/onebox-faas/faas/pkg/state"
)

// managedPostgresEgressAllowance is a subject's monthly egress allowance as
// its apps and its managed PostgreSQL databases share it (ADR-20261110160000):
// the plan's monthly egress, and the egress its apps sent this UTC month. The
// cap sweep adds the databases' own egress and holds them once the two reach
// the allowance. An elected egress overage that the provider bills live lifts
// the allowance for the databases too (slice 3): their egress past it is billed
// at Neon's price plus 5%, and the hold moves to twice the allowance only while
// managed PostgreSQL charges are delivered live. A deleted subject, or a plan
// without an egress allowance, has no ceiling.
func (s *server) managedPostgresEgressAllowance(ctx context.Context, subjectID string, now time.Time) (managedpostgres.EgressAllowance, error) {
	account, err := state.BillingResourceAccount(ctx, s.store, subjectID)
	if errors.Is(err, state.ErrNotFound) {
		return managedpostgres.EgressAllowance{}, nil
	}
	if err != nil {
		return managedpostgres.EgressAllowance{}, fmt.Errorf("managed postgres egress: load subject: %w", err)
	}
	limits, ok := api.LimitsFor(account.Plan)
	if !ok || limits.MonthlyEgressGiB <= 0 {
		return managedpostgres.EgressAllowance{}, nil
	}
	usages, err := meter.MonthUsageForAccount(ctx, s.store, subjectID, now)
	if err != nil {
		return managedpostgres.EgressAllowance{}, fmt.Errorf("managed postgres egress: app usage: %w", err)
	}
	allowance := managedpostgres.EgressAllowance{CeilingBytes: limits.MonthlyEgressGiB << 30, AppEgressBytes: meter.MonthlyEgressBytes(usages)}
	election, err := billing.EgressOverageElectionFor(ctx, s.store, subjectID)
	if err != nil && !errors.Is(err, state.ErrNotFound) {
		return managedpostgres.EgressAllowance{}, fmt.Errorf("managed postgres egress: overage election: %w", err)
	}
	allowance.OverageLifted = err == nil && billing.EgressOverageLifted(s.billingProvider, account.Plan, election, now)
	return allowance, nil
}
