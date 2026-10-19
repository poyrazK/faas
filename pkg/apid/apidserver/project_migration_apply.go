package apidserver

import (
	"context"
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/projectmigration"
	"github.com/onebox-faas/faas/pkg/apid/projectsetup"
	"github.com/onebox-faas/faas/pkg/state"
)

func migrationSource(token string, account string) (planTokenWire, string, error) {
	t, err := decodePlanToken(token)
	if err != nil || t.AccountID != account {
		return t, "", api.NewProblem(http.StatusGone, "preview_expired", "Review source again", "Scan the original repository revision before continuing.")
	}
	if t.RepoFullName != "" || t.InstallID != 0 || !t.NoTriggers || (t.Environment != "" && t.Environment != api.DefaultEnvScope) {
		return t, "", api.ErrValidation("scan source in migration mode with automatic deployments disabled and the default environment")
	}
	source, err := lookupPlanCache(t.Hash, account)
	if err != nil {
		return t, "", api.NewProblem(http.StatusGone, "preview_expired", "Review source again", "The reviewed archive expired. Scan the original repository revision before resuming.")
	}
	return t, source, nil
}
func migrationReplay(r *http.Request, source string, t planTokenWire, in api.ApplyCachedProjectPlanRequest) (*http.Request, func(), error) {
	fields, err := cachedProjectApplyFields(t, in)
	if err != nil {
		return nil, nil, err
	}
	replay, cleanup, err := projectsetup.Replay(r, source, fields)
	if err != nil {
		return nil, nil, err
	}
	query := replay.URL.Query()
	query.Set("plan_token", in.PlanToken)
	replay.URL.RawQuery = query.Encode()
	return replay, cleanup, nil
}
func (s *server) previewMigration(r *http.Request, acct state.Account, t planTokenWire, source, token string, only []string) (api.PlanResponse, error) {
	replay, cleanup, err := migrationReplay(r, source, t, api.ApplyCachedProjectPlanRequest{PlanToken: token, Only: only})
	if err != nil {
		return api.PlanResponse{}, err
	}
	defer cleanup()
	plan, _, _, _, _, _, problem := s.scanService(replay, acct, token, false)
	if problem != nil {
		return api.PlanResponse{}, problem
	}
	return api.PlanResponse{CanApply: plan.CanApply, Workloads: plan.Workloads}, nil
}
func (s *server) startProjectMigration(r *http.Request, acct state.Account, b projectmigration.Backend, in api.CreateProjectMigrationRequest) (api.ProjectMigration, error) {
	if out, exists, err := b.Existing(r.Context(), in); err != nil || exists {
		return out, err
	}
	t, source, err := migrationSource(in.PlanToken, acct.ID)
	if err != nil {
		return api.ProjectMigration{}, err
	}
	if len(in.PublicServiceConnections) > 0 && t.PublicAppsDomain != s.domain {
		return api.ProjectMigration{}, api.ErrValidation("public app endpoints changed; scan and review the public URLs again")
	}
	plan, err := s.previewMigration(r, acct, t, source, in.PlanToken, in.Only)
	if err != nil {
		return api.ProjectMigration{}, err
	}
	record, err := b.Create(r.Context(), in, plan, t.Slug, t.Hash)
	if err != nil {
		return api.ProjectMigration{}, err
	}
	return s.continueProjectMigration(r, acct, b, in.ID, api.ResumeProjectMigrationRequest{PlanToken: in.PlanToken, InitialSecrets: in.InitialSecrets, ExpectedVersion: record.Version, Only: in.Only})
}
func (s *server) continueProjectMigration(r *http.Request, acct state.Account, b projectmigration.Backend, id string, in api.ResumeProjectMigrationRequest) (api.ProjectMigration, error) {
	t, source, err := migrationSource(in.PlanToken, acct.ID)
	if err != nil {
		return api.ProjectMigration{}, err
	}
	ctx := context.WithoutCancel(r.Context())
	if err = b.CheckAppSlugs(ctx, id, t.AppSlugs, in.Only); err != nil {
		return api.ProjectMigration{}, err
	}
	run, err := b.BeginReviewed(ctx, id, t.Hash, t.Slug, in.InitialSecrets, in.ExpectedVersion, in.Only)
	if err != nil {
		return api.ProjectMigration{}, err
	}
	applyCtx, cancel := context.WithDeadline(run.Context(ctx), run.Deadline())
	defer cancel()
	failed := true
	defer func() {
		if failed {
			_ = run.Finish(ctx, nil, true)
		}
	}()
	if err = run.ValidateAppSlugs(t.AppSlugs); err != nil {
		return api.ProjectMigration{}, err
	}
	plan, err := s.previewMigration(r.WithContext(applyCtx), acct, t, source, in.PlanToken, run.Names())
	if err != nil {
		return api.ProjectMigration{}, err
	}
	if err = projectmigration.ValidateSelection(api.CreateProjectMigrationRequest{ID: id, Only: run.Names()}, plan); err != nil {
		return api.ProjectMigration{}, err
	}
	reviewedSlugs := map[string]string{}
	for _, workload := range plan.Workloads {
		reviewedSlugs[workload.Name] = workload.AppSlug
	}
	if err = run.ValidateAppSlugs(reviewedSlugs); err != nil {
		return api.ProjectMigration{}, err
	}
	replay, cleanup, err := migrationReplay(r.WithContext(applyCtx), source, t, api.ApplyCachedProjectPlanRequest{PlanToken: in.PlanToken, Only: run.Names(), InitialSecrets: in.InitialSecrets})
	if err != nil {
		return api.ProjectMigration{}, err
	}
	defer cleanup()
	response := &projectmigration.Response{}
	s.applyProject(response, replay, acct)
	if err = run.Finish(ctx, response.Builds(), response.Code >= http.StatusBadRequest); err != nil {
		failed = false
		return api.ProjectMigration{}, err
	}
	failed = false
	return b.Get(ctx, id)
}
