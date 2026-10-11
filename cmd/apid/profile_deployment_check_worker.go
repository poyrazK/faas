package main

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/profiling"
	"github.com/onebox-faas/faas/pkg/state"
)

func startProfileDeploymentChecks(ctx context.Context, s *server, log *slog.Logger) {
	_, hasDeploymentChecks := s.store.(state.ProfileDeploymentCheckStore)
	_, hasCanaryChecks := s.store.(state.ProfileCanaryCheckStore)
	_, hasPeriodicChecks := s.store.(state.ProfilePeriodicStore)
	if !hasDeploymentChecks && !hasCanaryChecks && !hasPeriodicChecks {
		return
	}
	go func() {
		ticker := time.NewTicker(api.ProfileAutoTickInterval)
		defer ticker.Stop()
		for {
			if err := s.runProfileDeploymentChecksOnce(ctx); err != nil && ctx.Err() == nil {
				log.Warn("automatic profile checks failed", "err", err)
			}
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
}

func (s *server) runProfileDeploymentChecksOnce(ctx context.Context) error {
	if store, ok := s.store.(state.ProfileDeploymentCheckStore); ok && s.profileBackend != nil {
		if err := store.MaintainProfileDeploymentChecks(ctx, time.Now()); err != nil {
			return err
		}
		if _, err := store.DiscoverProfileDeploymentChecks(ctx, time.Now()); err != nil {
			return err
		}
		for range api.ProfileAutoBatchSize {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			work, err := store.ClaimProfileDeploymentCheck(ctx, time.Now())
			if errors.Is(err, state.ErrNotFound) {
				break
			}
			if err != nil {
				return err
			}
			assessment, retry := s.assessProfileDeploymentCheck(ctx, work)
			out, err := store.FinishProfileDeploymentCheck(ctx, work, assessment, retry, time.Now())
			if errors.Is(err, state.ErrProfileCheckLease) || errors.Is(err, state.ErrNotFound) {
				continue
			}
			if err != nil {
				return err
			}
			if out.CompletedAt != nil {
				s.audit.Emit(ctx, "profile_deployment.checked", &work.AccountID, map[string]any{"app_id": out.AppID, "deployment_id": out.DeploymentID, "status": out.Status, "investigation_id": out.InvestigationID})
			}
		}
	}
	if store, ok := s.store.(state.ProfileCanaryCheckStore); ok {
		if err := s.runProfileCanaryChecksOnce(ctx, store); err != nil {
			return err
		}
	}
	return s.runProfilePeriodicChecksOnce(ctx)
}

func (s *server) runProfileCanaryChecksOnce(ctx context.Context, store state.ProfileCanaryCheckStore) error {
	if err := store.MaintainProfileCanaryChecks(ctx, time.Now()); err != nil {
		return err
	}
	if _, err := store.DiscoverProfileCanaryChecks(ctx, time.Now()); err != nil {
		return err
	}
	for range api.ProfileAutoBatchSize {
		if err := ctx.Err(); err != nil {
			return err
		}
		work, err := store.ClaimProfileCanaryCheck(ctx, time.Now())
		if errors.Is(err, state.ErrNotFound) {
			break
		}
		if err != nil {
			return err
		}
		assessment, retry := s.assessProfileCanaryCheck(ctx, work)
		if !retry || work.Check.Attempts >= api.ProfileAutoMaxAttempts {
			assessment.RequestMix = s.captureProfileRequestMix(ctx, work.AccountID, work.AppID, assessment)
		}
		if _, err := store.FinishProfileCanaryCheck(ctx, work, assessment, retry, time.Now()); errors.Is(err, state.ErrProfileCheckLease) || errors.Is(err, state.ErrNotFound) {
			continue
		} else if err != nil {
			return err
		}
	}
	return nil
}

func (s *server) assessProfileCanaryCheck(ctx context.Context, work state.ProfileCanaryCheckWork) (api.ProfileRegressionAssessment, bool) {
	check := work.Check
	input := api.ProfileInvestigation{Revision: 1}
	if check.Candidate != nil {
		input.Investigation.Candidate = *check.Candidate
	}
	if check.Baseline != nil {
		input.Investigation.Baseline = *check.Baseline
	}
	assessment := profiling.NewRegressionAssessment(input, check.Options, time.Now())
	if check.Baseline == nil || check.Candidate == nil {
		assessment.Reason = check.Reason
		return assessment, false
	}
	app, err := s.store.AppByID(ctx, work.AppID)
	if err != nil || app.AccountID != work.AccountID || app.Status == state.AppDeleted {
		assessment.Reason = "The app is unavailable."
		return assessment, false
	}
	acct, err := s.store.AccountByID(ctx, work.AccountID)
	if err != nil {
		assessment.Reason = "The account is unavailable."
		return assessment, false
	}
	var candidateDeployment state.Deployment
	for _, q := range []api.ProfileQuery{*check.Baseline, *check.Candidate} {
		dep, err := s.store.DeploymentByID(ctx, q.DeploymentID)
		if err != nil || dep.AppID != app.ID || dep.DeletedAt != nil {
			assessment.Reason = "A selected deployment is unavailable or its environment does not match."
			return assessment, false
		}
		if q.DeploymentID == check.Candidate.DeploymentID {
			candidateDeployment = dep
		}
		window := s.profileInvestigationWindow(ctx, acct, app, q)
		if window.Status != "retained" {
			assessment.Reason = window.Detail
			return assessment, window.Status == "backend_unavailable" && check.Attempts < api.ProfileAutoMaxAttempts
		}
	}
	baselineDeployment, err := s.store.DeploymentByID(ctx, check.Baseline.DeploymentID)
	if err != nil || candidateDeployment.ID == "" || profileDeploymentScope(baselineDeployment.Scope) != profileDeploymentScope(candidateDeployment.Scope) {
		assessment.Reason = "The stable and canary deployments are not in the same environment."
		return assessment, false
	}
	assessment = s.assessOwnedProfileRegression(ctx, acct, app, input, check.Options)
	return assessment, assessment.Status == "inconclusive" && check.Attempts < api.ProfileAutoMaxAttempts
}

func (s *server) assessProfileDeploymentCheck(ctx context.Context, work state.ProfileDeploymentCheckWork) (api.ProfileRegressionAssessment, bool) {
	check := work.Check
	input := api.ProfileInvestigation{Revision: 1, Investigation: api.ProfileInvestigationInput{Candidate: check.Candidate}}
	if check.Baseline != nil {
		input.Investigation.Baseline = *check.Baseline
	}
	out := profiling.NewRegressionAssessment(input, check.Config.Options, time.Now())
	if check.Baseline == nil {
		out.Reason = "No previous successful deployment exists in this environment before deployment creation."
		return out, false
	}
	app, err := s.store.AppByID(ctx, check.AppID)
	if err != nil || app.AccountID != work.AccountID || app.Status == state.AppDeleted {
		out.Reason = "The app is unavailable."
		return out, false
	}
	acct, err := s.store.AccountByID(ctx, work.AccountID)
	if err != nil {
		out.Reason = "The account is unavailable."
		return out, false
	}
	for _, q := range []api.ProfileQuery{out.Baseline, out.Candidate} {
		dep, err := s.store.DeploymentByID(ctx, q.DeploymentID)
		if err != nil || dep.AppID != app.ID || dep.DeletedAt != nil || dep.Scope != "" && dep.Scope != check.Scope {
			out.Reason = "A selected deployment is unavailable or its environment does not match."
			return out, false
		}
		status := s.profileInvestigationWindow(ctx, acct, app, q)
		if status.Status != "retained" {
			out.Reason = status.Detail
			return out, status.Status == "backend_unavailable"
		}
	}
	out = s.assessOwnedProfileRegression(ctx, acct, app, input, check.Config.Options)
	return out, out.Status == "inconclusive" && check.Attempts < api.ProfileAutoMaxAttempts
}
