package apidserver

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/deployreadiness"
	"github.com/onebox-faas/faas/pkg/apid/deploystaging"
	"github.com/onebox-faas/faas/pkg/apid/serviceadditions"
	"github.com/onebox-faas/faas/pkg/reposcan"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) prepareSafeProjectRelease(r *http.Request, acct state.Account, req *scanPlanRequest, apply bool) *api.Problem {
	if problem := s.validateProjectAdditionRequest(r, req); problem != nil {
		return problem
	}
	mode := r.Header.Get(api.ProjectSafeReleaseHeader)
	workers := strings.HasSuffix(mode, "-workers")
	health := r.Header.Get(api.ProjectWorkerHealthHeader)
	progressive := r.Header.Get(api.ProjectProgressiveHeader)
	stabilization := r.Header.Get(api.ProjectStabilizationHeader)
	if stabilization != "" && (stabilization != "required" || len(r.Header.Values(api.ProjectStabilizationHeader)) != 1 || progressive != "required") {
		return api.ErrValidation("stabilization requires a progressive verified release")
	}
	if progressive != "" {
		if progressive != "required" || len(r.Header.Values(api.ProjectProgressiveHeader)) != 1 || mode == "" || mode == "atomic" || len(req.ReleaseConfig) == 0 || workers && health != "required" {
			return api.ErrValidation("progressive releases require an atomic verified paired configuration release and worker health when workers are included")
		}
		if !s.projectCanariesEnabled {
			return api.ErrCapacity("progressive project canaries are disabled pending native qualification")
		}
	}
	if health != "" && (health != "required" || !workers) {
		return api.ErrValidation("worker health requires an atomic verified worker release")
	}
	if workers {
		mode = strings.TrimSuffix(mode, "-workers")
		if !s.atomicWorkerReleaseEnabled || mode == "atomic" {
			return api.ErrCapacity("atomic worker releases are disabled pending native qualification")
		}
	}
	if problem := s.validateReleaseSecretRequest(r, req); problem != nil {
		return problem
	}
	if mode == "" {
		if len(req.ReleaseConfig) > 0 {
			return api.ErrValidation("release_config requires atomic-config-verify mode")
		}
		return nil
	}
	if req.Environment == "" {
		req.Environment = "production"
	}
	if mode != "atomic" && mode != "atomic-verify" && mode != "atomic-config-verify" && mode != "atomic-migrate-verify" && mode != "atomic-config-migrate-verify" || !req.NoTriggers || len(req.Secrets) > 0 || len(req.InitialSecrets) > 0 || len(req.Only) > 0 || len(req.Exclude) > 0 || req.PersistExclude || len(req.RetryTargets) > 0 || req.Migration || req.Environment != "" && req.Environment != "production" {
		return api.ErrValidation("safe project deploy requires production, --no-triggers, and the complete project without secret or exclusion changes")
	}
	if apply && deploystaging.ProjectSubmissionFromContext(r.Context()) == nil {
		return api.ErrValidation("safe project deploy requires a durable submission attempt")
	}
	if _, ok := s.store.(state.ProjectSafeReleaseStore); !ok {
		return api.ErrCapacity("safe project release orchestration is unavailable")
	}
	project, err := s.store.ProjectBySlug(r.Context(), acct.ID, req.ProjectSlug)
	if err != nil {
		return api.ErrValidation("safe deploy requires an existing project")
	}
	reader, ok := s.store.(state.ProjectReleaseSetReader)
	if !ok {
		return api.ErrCapacity("project release sets are unavailable")
	}
	release, err := reader.ActiveProjectReleaseSet(r.Context(), acct.ID, project.ID, "production")
	if err != nil {
		return api.ErrValidation("safe deploy requires an active production release set and revision retention on every workload")
	}
	req.SafeRelease = &api.ProjectSafeRelease{Status: "building", PreviousReleaseID: release.ID, TTLSeconds: release.TTLSeconds, Deadline: time.Now().UTC().Add(api.ProjectSafeReleaseGracePeriod + time.Duration(len(release.Members)*api.BuildTimeoutSeconds)*time.Second)}
	req.SafeRelease.AddServices = r.Header.Get(api.ProjectAddServicesHeader) == "required"
	if mode != "atomic" {
		verification, problem := s.prepareProjectReleaseVerification(r.Context(), acct.ID, release)
		if problem != nil {
			return problem
		}
		req.SafeRelease.Verification = verification
		req.SafeRelease.Verification.Progressive = progressive == "required"
		if stabilization == "required" {
			if time.Duration(release.TTLSeconds)*time.Second < api.ProjectReleaseStabilizationMinRetention {
				return api.ErrValidation(fmt.Sprintf("stabilization requires at least %s of existing revision retention", api.ProjectReleaseStabilizationMinRetention))
			}
			req.SafeRelease.Verification.Stabilization = &api.ProjectReleaseStabilization{Enabled: true, Status: "waiting"}
		}
	}
	configMode := mode == "atomic-config-verify" || mode == "atomic-config-migrate-verify"
	if configMode != (len(req.ReleaseConfig) > 0) {
		return api.ErrValidation("release_config and atomic-config-verify must be provided together")
	}
	if store, ok := s.store.(state.ProjectReleaseConfigurationStore); ok {
		configuration, err := store.PreviewProjectReleaseConfiguration(r.Context(), acct.ID, project.ID, release.ID, req.ReleaseConfig)
		if err != nil {
			return api.ErrValidation("release configuration could not be planned: " + err.Error())
		}
		if configuration != nil && configuration.Secrets != nil && (!s.projectReleaseSecretsEnabled || workers || req.SafeRelease.ReleaseCommand != nil || strings.Contains(mode, "migrate")) {
			return api.ErrValidation("release secret snapshots require the qualified HTTP-only verified release mode")
		}
		req.SafeRelease.Configuration = configuration
		if req.SafeRelease.AddServices && configuration != nil {
			return api.ErrValidation("service additions cannot change or inherit pinned release configuration yet")
		}
	} else if configMode {
		return api.ErrCapacity("paired release configuration is unavailable")
	}
	if mode == "atomic-migrate-verify" || mode == "atomic-config-migrate-verify" {
		if _, ok := s.store.(state.ProjectReleaseCommandStore); !ok {
			return api.ErrCapacity("project migration orchestration is unavailable")
		}
		if _, ok := s.store.(state.ProjectMigrationBaselineStore); !ok {
			return api.ErrCapacity("post-expansion baseline verification is unavailable")
		}
		req.SafeRelease.ReleaseCommand = &api.ProjectReleaseCommand{Compatibility: "backward-compatible", Status: "waiting", VerifyBaseline: true}
		req.SafeRelease.Deadline = req.SafeRelease.Deadline.Add(time.Duration(state.AppTaskDefaultTimeoutSeconds) * time.Second)
	}
	if workers {
		store, ok := s.store.(state.ProjectReleaseWorkerStore)
		if !ok {
			return api.ErrCapacity("worker release orchestration is unavailable")
		}
		worker, err := store.PreviewProjectReleaseWorker(r.Context(), acct.ID, project.ID, release.ID)
		if err != nil {
			return api.ErrValidation("--workers requires exactly one existing production worker with one unchanged enabled push binding and no other consumers")
		}
		req.SafeRelease.Worker = worker
		if health == "required" {
			req.SafeRelease.Verification.WorkerHealth = &api.ProjectWorkerHealth{Status: "insufficient_evidence"}
		}
	}
	if tracker := deploystaging.ProjectSubmissionFromContext(r.Context()); tracker != nil {
		tracker.Receipt.Apply.ProjectID = project.ID
		tracker.Receipt.Apply.SafeRelease = req.SafeRelease
	}
	return nil
}

func (s *server) safeProjectSourceApps(r *http.Request, acct state.Account, req *scanPlanRequest, scan reposcan.Result) (state.Project, []state.App, *api.Problem) {
	project, err := s.store.ProjectBySlug(r.Context(), acct.ID, req.ProjectSlug)
	if err != nil {
		return state.Project{}, nil, api.ErrValidation("safe deploy project changed")
	}
	apps, err := s.store.AppsForProject(r.Context(), acct.ID, project.ID)
	if err != nil {
		return state.Project{}, nil, api.ErrCapacity("safe deploy workloads could not be read")
	}
	selected, err := serviceadditions.Plan(r.Context(), s.store, acct, project, req.SafeRelease, apps, scan)
	if err != nil {
		return state.Project{}, nil, api.ErrValidation(err.Error())
	}
	if req.SafeRelease.ReleaseCommand != nil {
		if problem := s.prepareSafeProjectCommands(r.Context(), acct, project, req, selected, scan); problem != nil {
			return state.Project{}, nil, problem
		}
	}
	return project, selected, nil
}

func (s *server) runSafeProjectReleaseWorker(ctx context.Context) {
	store, ok := s.store.(deploystaging.SafeProjectReleaseStore)
	if !ok {
		return
	}
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		callCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		err := deploystaging.ReconcileSafeProjectReleases(callCtx, store, s.durable.CheckProjectValidatorMembers, s.probeProjectRelease)
		if err != nil && ctx.Err() == nil {
			s.log.Warn("safe project release reconciliation failed", "err", err)
		}
		cancel()
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func safeProjectReleaseID(safe *api.ProjectSafeRelease) string {
	return api.ProjectSafeReleasePolicyID(safe)
}
func (s *server) safeProjectApplySubmission(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if r.Header.Get(api.ProjectSafeReleaseHeader) == "" {
		r.Header.Set(api.ProjectSafeReleaseHeader, "atomic")
	}
	s.projectApplySubmission(w, r, acct)
}

func (s *server) prepareProjectReleaseVerification(ctx context.Context, accountID string, release state.ProjectReleaseSet) (*api.ProjectReleaseVerification, *api.Problem) {
	if s.routeProbes == nil || !s.routeProbes.Configured() || s.notif == nil {
		return nil, api.ErrCapacity("project verification requires a configured gateway probe sender")
	}
	if _, ok := s.routeProbes.(deploystaging.ProjectCriticalProbeSender); !ok {
		return nil, api.ErrCapacity("critical gateway probes are unavailable")
	}
	return deployreadiness.VerificationPolicy(ctx, s.store, accountID, release)
}

func (s *server) stageSafeProjectConfiguration(ctx context.Context, accountID, projectID string, req *scanPlanRequest) *api.Problem {
	if req.SafeRelease.Configuration == nil {
		return nil
	}
	tracker := deploystaging.ProjectSubmissionFromContext(ctx)
	store, ok := s.store.(state.ProjectReleaseConfigurationStore)
	if tracker == nil || !ok {
		return api.ErrCapacity("paired release configuration is unavailable")
	}
	if err := store.StageProjectReleaseConfiguration(ctx, accountID, tracker.Receipt.AttemptID, projectID, req.SafeRelease, req.ReleaseConfig); err != nil {
		return api.NewProblem(http.StatusConflict, api.CodeConflict, "Release configuration changed", "configuration changed before candidate admission; scan again")
	}
	return nil
}

func (s *server) WithAtomicWorkerReleaseEnabled(enabled bool) *server {
	s.atomicWorkerReleaseEnabled = enabled
	return s
}

func (s *server) WithProjectCanariesEnabled(enabled bool) *server {
	s.projectCanariesEnabled = enabled
	return s
}
