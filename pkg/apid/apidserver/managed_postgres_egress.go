package apidserver

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/managedpostgres"
	"github.com/onebox-faas/faas/pkg/meter"
	"github.com/onebox-faas/faas/pkg/state"
)

// managedPostgresEgressAllowance is a subject's monthly egress allowance as
// its apps and its managed PostgreSQL databases share it (ADR-20261110160000):
// the plan's monthly egress, and the egress its apps sent this UTC month. The
// cap sweep adds the databases' own egress and holds them once the two reach
// the allowance. Database egress is not billed as overage yet, so egress
// overage does not lift the databases' ceiling: an account that elected it
// keeps paying for its apps' egress past the allowance while its databases
// stop at it. A deleted subject, or a plan without an egress allowance, has
// no ceiling.
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
	return managedpostgres.EgressAllowance{CeilingBytes: limits.MonthlyEgressGiB << 30, AppEgressBytes: meter.MonthlyEgressBytes(usages)}, nil
}
