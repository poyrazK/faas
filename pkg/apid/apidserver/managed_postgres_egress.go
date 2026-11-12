package apidserver

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/billing"
	"github.com/onebox-faas/faas/pkg/managedpostgres"
	"github.com/onebox-faas/faas/pkg/state"
)

// managedPostgresEgressAllowance is each of a subject's managed PostgreSQL
// databases' own monthly egress allowance (ADR-20261110160000 amendment
// 2026-11-11): Neon includes 500 GB of transfer per project and every
// database is its own project, so a database is held once it sent its own
// 500 GB, and apps keep the plan's egress allowance to themselves. An elected
// egress overage that the provider bills live lifts the databases' allowance
// too (slice 3): egress past it is billed at Neon's price plus 5%, and the
// hold moves to twice the allowance only while managed PostgreSQL charges are
// delivered live. A deleted subject, or a plan without databases, has none.
func (s *server) managedPostgresEgressAllowance(ctx context.Context, subjectID string, now time.Time) (managedpostgres.EgressAllowance, error) {
	account, err := state.BillingResourceAccount(ctx, s.store, subjectID)
	if errors.Is(err, state.ErrNotFound) {
		return managedpostgres.EgressAllowance{}, nil
	}
	if err != nil {
		return managedpostgres.EgressAllowance{}, fmt.Errorf("managed postgres egress: load subject: %w", err)
	}
	if !api.ManagedPostgresIncluded(account.Plan) {
		return managedpostgres.EgressAllowance{}, nil
	}
	allowance := managedpostgres.EgressAllowance{CeilingBytes: api.ManagedPostgresDatabaseEgressAllowanceBytes}
	election, err := billing.EgressOverageElectionFor(ctx, s.store, subjectID)
	if err != nil && !errors.Is(err, state.ErrNotFound) {
		return managedpostgres.EgressAllowance{}, fmt.Errorf("managed postgres egress: overage election: %w", err)
	}
	allowance.OverageLifted = err == nil && billing.EgressOverageLifted(s.billingProvider, account.Plan, election, now)
	return allowance, nil
}
