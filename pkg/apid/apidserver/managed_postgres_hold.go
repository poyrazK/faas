package apidserver

import (
	"context"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/managedpostgres"
	"github.com/onebox-faas/faas/pkg/state"
)

// managedPostgresHoldInputs reads what the cap sweep holds a subject's
// databases from (ADR-20261110160000 amendment 2026-11-10), so the API can say
// why a ready database refuses connections. ok is false while caps are not
// enforced: nothing is held, and nothing is read.
func (s *server) managedPostgresHoldInputs(ctx context.Context, acct state.Account) (managedpostgres.HoldInputs, bool, error) {
	if s.managedPostgres == nil || s.managedPostgresCapMode != managedpostgres.CapEnforcementOn {
		return managedpostgres.HoldInputs{}, false, nil
	}
	limits, ok := api.ManagedPostgresLimitsFor(acct.Plan)
	if !ok {
		return managedpostgres.HoldInputs{}, false, nil
	}
	subject := acct.ResourceOwnerID()
	summary, _, err := s.managedPostgresUsageFor(ctx, acct, subject, limits)
	if err != nil {
		return managedpostgres.HoldInputs{}, false, err
	}
	in, err := s.managedPostgresHoldInputsFrom(ctx, acct, subject, summary)
	return in, err == nil, err
}

// managedPostgresHoldInputsFrom is managedPostgresHoldInputs over a usage
// summary already read.
func (s *server) managedPostgresHoldInputsFrom(ctx context.Context, acct state.Account, subject string, summary managedpostgres.UsageSummary) (managedpostgres.HoldInputs, error) {
	in := managedpostgres.HoldInputs{Mode: s.managedPostgresCapMode, Inactive: !acct.Active(),
		ComputeUsed: summary.Snapshot.ComputeUnitSeconds, DatabaseEgress: summary.Snapshot.EgressBytes}
	if summary.PolicyEnabled {
		in.ComputeCeiling = summary.EffectivePolicy.MaxMonthlyComputeUnitSeconds
	}
	egress, err := s.managedPostgresEgressAllowance(ctx, subject, time.Now().UTC())
	if err != nil {
		return managedpostgres.HoldInputs{}, err
	}
	in.EgressCeiling, in.AppEgress = egress.CeilingBytes, egress.AppEgressBytes
	return in, nil
}

// withManagedPostgresHold sets a database view's hold; a failed read leaves it
// unset rather than failing the request, as plan excess does.
func (s *server) withManagedPostgresHold(view *api.ManagedPostgresDatabase, database managedpostgres.Database, in managedpostgres.HoldInputs, ok bool) {
	if ok {
		view.Hold = managedpostgres.DatabaseHold(database, view.PlanExcess != "", in)
	}
}

// managedPostgresUsageHoldView adds the shared egress allowance and the
// subject-wide hold to the usage view.
func managedPostgresUsageHoldView(view *api.ManagedPostgresUsageResponse, in managedpostgres.HoldInputs) {
	if in.EgressCeiling > 0 {
		view.EgressAllowanceBytes, view.AppEgressBytes = in.EgressCeiling, in.AppEgress
		view.EgressState = managedpostgres.EgressState(in)
	}
	view.Hold = managedpostgres.SubjectHold(in)
}

// managedPostgresHoldInputsOrLog is managedPostgresHoldInputs for views that
// must not fail when the usage read does.
func (s *server) managedPostgresHoldInputsOrLog(ctx context.Context, acct state.Account) (managedpostgres.HoldInputs, bool) {
	in, ok, err := s.managedPostgresHoldInputs(ctx, acct)
	if err != nil {
		s.log.Warn("managed postgres hold: read usage", "account", acct.ID, "err", err)
		return managedpostgres.HoldInputs{}, false
	}
	return in, ok
}
