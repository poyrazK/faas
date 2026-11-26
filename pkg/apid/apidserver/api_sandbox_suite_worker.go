package apidserver

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

const (
	apiSandboxSuiteTimeout   = 45 * time.Minute
	apiSandboxSuiteWorkLease = 45 * time.Second
	apiSandboxSuitePoll      = 2 * time.Second
	apiSandboxSuiteQuotaPoll = 5 * time.Second
)

func (s *server) runAPISandboxSuites(ctx context.Context) {
	store, ok := s.store.(state.APISandboxSuiteStore)
	checks, supportsChecks := s.store.(state.APISandboxApprovalCheckStore)
	if !ok || !supportsChecks || !s.apiSandboxWorkerEnabled() {
		return
	}
	ticker := time.NewTicker(apiSandboxSuitePoll)
	defer ticker.Stop()
	automatic, _ := s.store.(state.APISandboxSuiteAutomaticStore)
	for {
		err := s.processAPISandboxSuite(ctx, store, checks, time.Now().UTC())
		if err != nil && !errors.Is(err, state.ErrNotFound) && !errors.Is(err, state.ErrConflict) && ctx.Err() == nil {
			s.log.WarnContext(ctx, "process API sandbox regression suite", "err", err)
		}
		if automatic != nil {
			err := s.processAPISandboxSuiteAutomatic(ctx, store, automatic, time.Now().UTC())
			if err != nil && !errors.Is(err, state.ErrNotFound) && !errors.Is(err, state.ErrConflict) && ctx.Err() == nil {
				s.log.WarnContext(ctx, "queue automatic sandbox regression suite", "err", err)
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (s *server) processAPISandboxSuite(ctx context.Context, store state.APISandboxSuiteStore, checks state.APISandboxApprovalCheckStore, now time.Time) error {
	clockStart := time.Now()
	work, cancel := context.WithTimeout(ctx, apiSandboxSuiteWorkLease-10*time.Second)
	defer cancel()
	run, err := store.ClaimAPISandboxSuiteRun(work, now, apiSandboxSuiteWorkLease)
	if err != nil {
		return err
	}
	if err := s.tickAPISandboxSuite(work, store, checks, &run, now); err != nil {
		return err
	}
	save, stop := context.WithTimeout(ctx, 3*time.Second)
	defer stop()
	return store.CheckpointAPISandboxSuiteRun(save, run, now.Add(time.Since(clockStart)))
}

func (s *server) tickAPISandboxSuite(ctx context.Context, store state.APISandboxSuiteStore, checks state.APISandboxApprovalCheckStore, run *state.APISandboxSuiteRun, now time.Time) error {
	run.WaitingForCapacity = false
	item, err := store.GetAPISandboxSuiteScenario(ctx, run.ID, run.CompletedCount)
	if err != nil {
		return err
	}
	// This comparison UUID is allocated when the suite is queued. If a worker
	// stops after reservation, its replacement finds the same pair here.
	evidence, err := checks.GetAPISandboxApprovalCheck(ctx, item.ComparisonID)
	if err == nil && evidence.Comparison.Verdict != api.APISandboxComparisonRunning {
		// A delayed poll still accepts evidence that finished in time.
		if completed, ok := evidence.Comparison.CompletedAt(); ok && completed.Before(run.DeadlineAt) {
			run.CompletedCount++
			if run.CompletedCount == run.ScenarioCount {
				finishSandboxSuite(run, completed, "")
				return nil
			}
			if now.Before(run.DeadlineAt) {
				run.NextAttemptAt = &now
				return nil
			}
		}
	}
	if err != nil && !errors.Is(err, state.ErrNotFound) {
		return err
	}
	if !now.Before(run.DeadlineAt) {
		finishSandboxSuite(run, now, "timed_out")
		return nil
	}
	next := now.Add(apiSandboxSuitePoll)
	run.NextAttemptAt = &next
	if err == nil {
		return nil
	}
	return s.startAPISandboxSuiteComparison(ctx, checks, run, item, now)
}

func (s *server) startAPISandboxSuiteComparison(ctx context.Context, checks state.APISandboxApprovalCheckStore, run *state.APISandboxSuiteRun, item state.APISandboxSuiteScenario, now time.Time) error {
	app, candidate, reason, err := s.sandboxSuiteCurrentRelease(ctx, *run)
	if err != nil {
		return err
	}
	if reason != "" {
		finishSandboxSuite(run, now, reason)
		return nil
	}
	acct, err := s.store.AccountByID(ctx, app.AccountID)
	if errors.Is(err, state.ErrNotFound) {
		finishSandboxSuite(run, now, "entitlement_changed")
		return nil
	}
	if err != nil {
		return err
	}
	eligible, err := s.sandboxSuiteEntitled(ctx, acct, app)
	if err != nil {
		return err
	}
	if !eligible {
		finishSandboxSuite(run, now, "entitlement_changed")
		return nil
	}
	scenario, err := item.Scenario()
	if err != nil {
		return err
	}
	apps, problem := s.buildAPISandboxComparisonApps(acct, app, run.BaselineDeploymentID, candidate.ID, &scenario)
	if problem != nil {
		return fmt.Errorf("build sandbox suite comparison: %s", problem.Code)
	}
	created, err := checks.CreateAPISandboxApprovalCheck(ctx, item.ComparisonID, apps[0], apps[1], api.MustLimitsFor(acct.Plan), run.Pins)
	var quota *state.QuotaError
	if errors.As(err, &quota) || errors.Is(err, state.ErrConflict) {
		next := now.Add(apiSandboxSuiteQuotaPoll)
		run.NextAttemptAt = &next
		run.WaitingForCapacity = quota != nil
		return nil
	}
	if err != nil {
		return err
	}
	for _, sandbox := range created {
		s.emitAppCreated(ctx, sandbox)
	}
	return nil
}

func (s *server) sandboxSuiteCurrentRelease(ctx context.Context, run state.APISandboxSuiteRun) (state.App, state.Deployment, string, error) {
	app, err := s.store.AppByID(ctx, run.AppID)
	if errors.Is(err, state.ErrNotFound) {
		return app, state.Deployment{}, "approval_inputs_changed", nil
	}
	if err != nil {
		return app, state.Deployment{}, "", err
	}
	candidate, err := s.store.DeploymentByID(ctx, run.CandidateDeploymentID)
	if errors.Is(err, state.ErrNotFound) || (err == nil && candidate.AppID != app.ID) {
		return app, candidate, "approval_inputs_changed", nil
	}
	if err != nil {
		return app, candidate, "", err
	}
	in, err := s.sandboxApprovalInputs(ctx, app, candidate, false)
	if err != nil {
		return app, candidate, "", err
	}
	if (in.availability != api.APISandboxApprovalReady && in.availability != api.APISandboxApprovalNoScenario) ||
		in.baselineID != run.BaselineDeploymentID || in.pins != run.Pins {
		return app, candidate, "approval_inputs_changed", nil
	}
	return app, candidate, "", nil
}

func (s *server) sandboxSuiteEntitled(ctx context.Context, acct state.Account, app state.App) (bool, error) {
	limits, _ := api.ManagedPostgresLimitsFor(acct.Plan)
	if acct.Plan.ConsumerKeysPerApp() == 0 || managedPostgresNotInPlan(acct.Plan) || !limits.DevelopmentAllowed {
		return false, nil
	}
	if app.OrgID == "" {
		return true, nil
	}
	org, err := s.store.OrgByPersonalAccount(ctx, acct.ID)
	if errors.Is(err, state.ErrNotFound) {
		return false, nil
	}
	return org.ID == app.OrgID, err
}

func finishSandboxSuite(run *state.APISandboxSuiteRun, now time.Time, failure string) {
	run.FinishedAt, run.NextAttemptAt, run.FailureCode = &now, nil, failure
}
