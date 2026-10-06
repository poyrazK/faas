// adr: 623 — promotion workers prepare leased candidates and activate an exact checked graph.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/managedpostgres"
	"github.com/onebox-faas/faas/pkg/state"
)

func projectPromotionBindingsReport(p state.ProjectEnvironmentPromotion) *api.ProjectReleaseCheckResponse {
	if len(p.BindingsCheck) == 0 {
		return nil
	}
	var report api.ProjectReleaseCheckResponse
	if json.Unmarshal(p.BindingsCheck, &report) != nil {
		return nil
	}
	return &report
}
func writeBindingProjectPromotionAdmission(w http.ResponseWriter, p state.ProjectEnvironmentPromotion, workloads []state.ProjectEnvironmentPromotionWorkload) {
	status := http.StatusAccepted
	if p.Status == "succeeded" || p.Status == "failed" {
		status = http.StatusOK
	}
	writeJSON(w, status, projectEnvironmentPromotionResponse(p, workloads))
}

func (s *server) stageBindingProjectPromotion(ctx context.Context, claim state.BindingProjectPromotionClaim, p state.ProjectEnvironmentPromotion, workloads []state.ProjectEnvironmentPromotionWorkload, plan projectEnvironmentPromotionPlan) error {
	store := s.store.(state.BindingProjectPromotionStore)
	changes := map[string]api.ProjectEnvironmentPromotionChange{}
	for _, change := range plan.Preview.Changes {
		changes[change.WorkloadSlug] = change
	}
	for _, w := range workloads {
		if w.Status == "promoted" || w.Status == "unchanged" {
			continue
		}
		id, err := store.ReserveBindingProjectPromotionTarget(ctx, claim, w.ID)
		if err != nil {
			return err
		}
		targetCtx := state.WithBindingProjectPromotionTarget(ctx, claim, id)
		source := plan.Sources[w.WorkloadSlug]
		if p.SyncConfig {
			change := changes[w.WorkloadSlug]
			input := state.ProjectEnvironmentPromotionWorkloadSpecInput{PromotionID: p.ID, SourceDeploymentID: w.SourceDeploymentID, SourceHash: change.SourceWorkloadConfigHash, PreviousTargetHash: change.TargetWorkloadConfigHash}
			_, err = promoteProjectEnvironmentDeploymentWithTraffic(targetCtx, s.store, source, p.ToEnvironment, p.ID, true, input)
		} else {
			_, err = promoteProjectEnvironmentDeploymentDark(targetCtx, s.store, source, p.ToEnvironment, p.ID)
		}
		if err != nil {
			return err
		}
		if err := store.CheckpointBindingProjectPromotionTarget(ctx, claim, w.ID); err != nil {
			return err
		}
	}
	return nil
}

func (s *server) bindingProjectPromotionCheck(ctx context.Context, claim state.BindingProjectPromotionClaim) error {
	store := s.store.(state.BindingProjectPromotionStore)
	ctx = state.WithBindingProjectPromotionClaim(ctx, claim)
	ctx = context.WithValue(ctx, bindingReleaseWorkerReadsKey{}, true)
	p, workloads, err := s.store.ProjectEnvironmentPromotionByID(ctx, claim.AccountID, claim.ProjectSlug, claim.Environment, claim.PromotionID)
	if err != nil {
		return err
	}
	report := api.ProjectReleaseCheckResponse{ProjectID: p.ProjectID, Environment: p.ToEnvironment, TTLSeconds: p.ReleaseTTLSeconds, ExpectedActiveReleaseID: p.PreviousTargetReleaseSetID, CheckedAt: time.Now().UTC()}
	blocked := func(code, message string, terminal bool) error {
		report.Passed = false
		report.Blockers = append(report.Blockers, api.BindingCheckFinding{Code: code, Message: message})
		return store.UpdateBindingProjectPromotionCheck(ctx, claim, report, message, terminal)
	}
	acct, err := s.store.AccountByID(ctx, p.AccountID)
	if err != nil {
		return blocked("promotion_check_unavailable", "The account could not be read; the worker will retry.", false)
	}
	if !acct.MayDeploy() {
		return blocked("account_deploy_blocked", "The account must permit deployments before activation.", false)
	}
	plan, problem := s.buildProjectEnvironmentPromotionResumePlan(ctx, acct, p, workloads)
	if problem != nil {
		return blocked(problem.Code, problem.Detail, problem.Status < 500)
	}
	if err := s.stageBindingProjectPromotion(ctx, claim, p, workloads, plan); err != nil {
		if errors.Is(err, state.ErrBindingProjectPromotionLease) {
			return err
		}
		return blocked("promotion_preparation_unavailable", "Candidate preparation could not complete; the same reserved candidates will be retried.", errors.Is(err, state.ErrConflict))
	}
	p, workloads, err = s.store.ProjectEnvironmentPromotionByID(ctx, claim.AccountID, claim.ProjectSlug, claim.Environment, claim.PromotionID)
	if err != nil {
		return err
	}
	project, err := s.store.ProjectBySlug(ctx, acct.ID, p.ProjectSlug)
	if err != nil {
		return blocked("promotion_check_unavailable", "The project could not be read; the worker will retry.", false)
	}
	expected := p.PreviousTargetReleaseSetID
	candidate := projectReleaseCandidate{project: project, environment: p.ToEnvironment, request: api.PublishProjectReleaseSetRequest{ExpectedActiveReleaseID: &expected, TTLSeconds: p.ReleaseTTLSeconds, Deployments: map[string]string{}}}
	for _, w := range workloads {
		id := w.TargetDeploymentID
		if w.Status == "unchanged" {
			id = w.PreviousTargetDeploymentID
		}
		candidate.request.Deployments[w.WorkloadSlug] = id
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, "/", nil)
	if err != nil {
		return err
	}
	if problem := s.validateProjectReleaseCandidate(request, acct, &candidate); problem != nil {
		return blocked(problem.Code, problem.Detail, problem.Status < 500)
	}
	observationsReport, observations, problem := s.observeProjectRelease(request, acct, candidate)
	if problem != nil {
		return blocked(problem.Code, problem.Detail, problem.Status < 500)
	}
	report = observationsReport
	if !report.Passed {
		terminal := false
		for _, b := range report.Blockers {
			terminal = terminal || b.Code == "project_release_changed"
		}
		return store.UpdateBindingProjectPromotionCheck(ctx, claim, report, "Resolve the saved binding blockers; the worker rechecks without running probes.", terminal)
	}
	if s.managedPostgresBindings == nil || len(observations) != len(candidate.members) || len(observations) == 0 {
		return blocked("promotion_check_unavailable", "Complete binding fences are unavailable; the worker will retry.", false)
	}
	domains := make([]managedpostgres.AppPromotionFence, 0, len(observations))
	fences := make([]state.BindingPromotionFence, 0, len(observations))
	for _, o := range observations {
		domains = append(domains, managedpostgres.AppPromotionFence{AppID: o.fence.AppID, Fence: o.domain})
		fences = append(fences, o.fence)
	}
	err = s.managedPostgresBindings.GuardPromotions(ctx, acct.ID, domains, observations[0].store.BindingPromotionBackend(), func(writeCtx context.Context) error {
		_, publishErr := store.PublishBindingCheckedEnvironmentPromotion(state.WithBindingReleaseFences(writeCtx, fences), claim, p.ReleaseTTLSeconds, candidate.members, report)
		return publishErr
	})
	if err == nil || errors.Is(err, state.ErrBindingProjectPromotionLease) {
		return err
	}
	return blocked("promotion_observations_changed", "Graph, configuration, policy or evidence changed before activation; inspect the saved report before starting another promotion.", errors.Is(err, state.ErrConflict))
}

func (s *server) bindingProjectPromotionSweep(ctx context.Context) error {
	store, ok := s.store.(state.BindingProjectPromotionStore)
	if !ok {
		return nil
	}
	for i := 0; i < api.ProjectBindingPromotionCheckBatchSize; i++ {
		claim, err := store.ClaimBindingProjectPromotion(ctx)
		if errors.Is(err, state.ErrNotFound) {
			return nil
		}
		if err != nil {
			return err
		}
		bounded, cancel := context.WithTimeout(ctx, time.Duration(api.ProjectBindingPromotionLeaseSeconds/2)*time.Second)
		err = s.bindingProjectPromotionCheck(bounded, claim)
		cancel()
		if err != nil && !errors.Is(err, state.ErrBindingProjectPromotionLease) {
			return err
		}
	}
	return ctx.Err()
}
func (s *server) runBindingProjectPromotionWorker(ctx context.Context) {
	ticker := time.NewTicker(time.Duration(api.ProjectBindingPromotionCheckIntervalSeconds) * time.Second)
	defer ticker.Stop()
	for {
		if err := s.bindingProjectPromotionSweep(ctx); err != nil && ctx.Err() == nil {
			s.log.Warn("binding project promotion sweep", "err", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
