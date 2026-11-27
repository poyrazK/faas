package apidserver

import (
	"context"
	"errors"
	"slices"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

const sandboxSuiteAutoClaim = 30 * time.Second

func (s *server) processAPISandboxSuiteAutomatic(ctx context.Context, store state.APISandboxSuiteStore, automatic state.APISandboxSuiteAutomaticStore, now time.Time) error {
	work, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	req, err := automatic.ClaimAPISandboxSuiteAutomatic(work, now, sandboxSuiteAutoClaim)
	if err != nil {
		return err
	}
	status, err := s.startAPISandboxSuiteAutomatic(work, store, req, now)
	if err != nil {
		return err
	}
	var next *time.Time
	switch status {
	case "started", "disabled", "approval_ended":
	default:
		retry := now.Add(sandboxSuiteAutoClaim / 2)
		next = &retry
	}
	save, stop := context.WithTimeout(ctx, 3*time.Second)
	defer stop()
	return automatic.CheckpointAPISandboxSuiteAutomatic(save, req, status, next)
}

func (s *server) startAPISandboxSuiteAutomatic(ctx context.Context, store state.APISandboxSuiteStore, req state.APISandboxSuiteAutomaticRequest, now time.Time) (string, error) {
	if existing, err := store.GetAPISandboxSuiteRun(ctx, req.RunID); err == nil {
		if existing.Automatic && existing.CandidateDeploymentID == req.DeploymentID {
			return "started", nil
		}
		return "", state.ErrConflict
	} else if !errors.Is(err, state.ErrNotFound) {
		return "", err
	}
	candidate, err := s.store.DeploymentByID(ctx, req.DeploymentID)
	if errors.Is(err, state.ErrNotFound) {
		return "approval_ended", nil
	}
	if err != nil {
		return "", err
	}
	app, err := s.store.AppByID(ctx, candidate.AppID)
	if errors.Is(err, state.ErrNotFound) {
		return "approval_ended", nil
	}
	if err != nil {
		return "", err
	}
	suite, err := store.GetAPISandboxSuite(ctx, app.ID)
	if errors.Is(err, state.ErrNotFound) || (err == nil && len(suite.ScenarioIDs) == 0) {
		return "disabled", nil
	}
	if err != nil {
		return "", err
	}
	if !suite.RunBeforeApproval {
		approvals, ok := s.store.(state.ReleaseApprovalStore)
		if !ok {
			return "disabled", nil
		}
		policy, err := approvals.ReleaseApprovalPolicy(ctx, app.ID)
		if err != nil && !errors.Is(err, state.ErrNotFound) {
			return "", err
		}
		if !policy.RequiresSandboxSuite() {
			return "disabled", nil
		}
	}
	in, err := s.sandboxApprovalInputs(ctx, app, candidate, false)
	if err != nil {
		return "", err
	}
	switch in.availability {
	case api.APISandboxApprovalNotWaiting:
		return "approval_ended", nil
	case api.APISandboxApprovalNoBaseline:
		return "waiting_for_baseline", nil
	case api.APISandboxApprovalNoScenario:
		return "waiting_for_scenarios", nil
	case api.APISandboxApprovalReady:
	default:
		return "unavailable", nil
	}
	for _, id := range suite.ScenarioIDs {
		if !slices.ContainsFunc(in.library, func(entry api.APISandboxLibraryScenarioSummary) bool { return entry.ID == id }) {
			return "waiting_for_scenarios", nil
		}
	}
	acct, err := s.store.AccountByID(ctx, app.AccountID)
	if errors.Is(err, state.ErrNotFound) {
		return "unavailable", nil
	}
	if err != nil {
		return "", err
	}
	allowed, err := s.sandboxSuiteEntitled(ctx, acct, app)
	if err != nil {
		return "", err
	}
	if !allowed {
		return "unavailable", nil
	}
	run, err := store.CreateAPISandboxSuiteRun(ctx, newSandboxSuiteRun(app.ID, candidate.ID, req.RunID, suite.Revision, in, now, true))
	switch {
	case errors.Is(err, state.ErrAPISandboxSuiteAlreadyActive):
		return "waiting_for_capacity", nil
	case errors.Is(err, state.ErrConflict):
		return "queued", nil
	case err != nil:
		return "", err
	}
	s.auditSandboxSuiteQueued(ctx, acct.ID, run)
	return "started", nil
}
