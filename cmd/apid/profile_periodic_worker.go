package main

import (
	"context"
	"errors"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/profiling"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) runProfilePeriodicChecksOnce(ctx context.Context) error {
	store, ok := s.store.(state.ProfilePeriodicStore)
	if !ok || s.profileBackend == nil {
		return nil
	}
	if err := store.DiscoverProfilePeriodicMonitors(ctx, time.Now()); err != nil {
		return err
	}
	for range api.ProfileAutoBatchSize {
		if err := ctx.Err(); err != nil {
			return err
		}
		work, err := store.ClaimProfilePeriodicMonitor(ctx, time.Now())
		if errors.Is(err, state.ErrNotFound) {
			break
		}
		if err != nil {
			return err
		}
		result := s.assessProfilePeriodicMonitor(ctx, work)
		err = store.FinishProfilePeriodicMonitor(ctx, work, result, time.Now())
		if errors.Is(err, state.ErrProfileCheckLease) || errors.Is(err, state.ErrNotFound) {
			continue
		}
		if err != nil {
			return err
		}
	}
	return nil
}
func (s *server) assessProfilePeriodicMonitor(ctx context.Context, work state.ProfilePeriodicWork) state.ProfilePeriodicResult {
	check := state.PeriodicProfileCheck(work.Monitor)
	input := api.ProfileInvestigation{Revision: 1, Investigation: api.ProfileInvestigationInput{Baseline: *check.Baseline, Candidate: check.Candidate}}
	out := state.ProfilePeriodicResult{Assessment: profiling.NewRegressionAssessment(input, check.Config.Options, time.Now())}
	if check.Attempts > api.ProfileAutoMaxAttempts {
		out.Assessment.Reason = "Periodic check leases exhausted; waiting for a fresh window."
		return out
	}
	if work.Monitor.Baseline != nil {
		acct, err := s.store.AccountByID(ctx, work.AccountID)
		if err == nil && api.MustLimitsFor(acct.Plan).Profiling.Enabled && profiling.ValidateQuery(*check.Baseline, time.Now(), api.MustLimitsFor(acct.Plan).Profiling.RetentionDays) != nil {
			out.BaselineExpired = true
			out.Assessment.Reason = "Pinned baseline expired; a new evidence-qualified baseline is required."
			return out
		}
	}
	out.Assessment, out.Retry = s.assessProfileDeploymentCheck(ctx, state.ProfileDeploymentCheckWork{Check: check, AccountID: work.AccountID})
	for _, route := range out.Assessment.RouteChecks {
		if route.Status == "insufficient_data" {
			out.Retry = check.Attempts < api.ProfileAutoMaxAttempts
		}
	}
	return out
}
