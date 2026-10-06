package main

import (
	"context"
	"errors"
	"net/http"
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/managedpostgres"
	"github.com/onebox-faas/faas/pkg/state"
)

type projectReleaseCandidate struct {
	project     state.Project
	environment string
	request     api.PublishProjectReleaseSetRequest
	apps        []state.App
	members     []state.ProjectReleaseMember
}

func (s *server) readProjectReleaseCandidate(w http.ResponseWriter, r *http.Request, acct state.Account) (projectReleaseCandidate, bool) {
	var c projectReleaseCandidate
	project, ok := s.loadProject(w, r, acct)
	if !ok {
		return c, false
	}
	c.project, c.environment = project, r.PathValue("environment")
	if !api.ValidProjectEnvironmentSlug(c.environment) {
		api.WriteProblem(w, api.ErrValidation("invalid project environment"))
		return c, false
	}
	if err := decodeJSON(r, &c.request); err != nil {
		api.WriteProblem(w, api.ErrValidation(err.Error()))
		return c, false
	}
	p := s.validateProjectReleaseCandidate(r, acct, &c)
	if p != nil {
		api.WriteProblem(w, p)
		return c, false
	}
	return c, true
}

func (s *server) validateProjectReleaseCandidate(r *http.Request, acct state.Account, c *projectReleaseCandidate) *api.Problem {
	req := &c.request
	if req.TTLSeconds <= 0 || req.TTLSeconds > api.RevisionPinMaxTTLSeconds || len(req.Deployments) == 0 || len(req.Deployments) > api.ProjectReleaseSetMaxMembers {
		return api.ErrValidation("release set requires 1-100 workloads and ttl_seconds within the revision pin window")
	}
	if req.ExpectedActiveReleaseID != nil && *req.ExpectedActiveReleaseID != "" {
		id, err := uuid.Parse(*req.ExpectedActiveReleaseID)
		if err != nil || id == uuid.Nil {
			return api.ErrValidation("expected_active_release_id must be a release UUID or empty for no active graph")
		}
		canonical := id.String()
		req.ExpectedActiveReleaseID = &canonical
	}
	apps, err := s.store.AppsForProject(r.Context(), acct.ID, c.project.ID)
	if err != nil {
		return api.ErrCapacity("could not load project workloads")
	}
	if len(apps) != len(req.Deployments) {
		return api.NewProblem(http.StatusConflict, api.CodeConflict, "Incomplete release set", "every project workload must have exactly one deployment")
	}
	sort.Slice(apps, func(i, j int) bool { return apps[i].ID < apps[j].ID })
	for _, app := range apps {
		id, err := uuid.Parse(req.Deployments[app.Slug])
		if err != nil || id == uuid.Nil {
			return api.ErrValidation("every workload must select a deployment UUID")
		}
		req.Deployments[app.Slug] = id.String()
		c.members = append(c.members, state.ProjectReleaseMember{AppID: app.ID, DeploymentID: id.String()})
	}
	c.apps = apps
	return nil
}

func (s *server) checkProjectReleaseSet(w http.ResponseWriter, r *http.Request, acct state.Account) {
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	r = r.WithContext(ctx)
	c, ok := s.readProjectReleaseCandidate(w, r, acct)
	if !ok {
		return
	}
	if c.request.ExpectedActiveReleaseID == nil {
		api.WriteProblem(w, api.ErrValidation("expected_active_release_id is required for a checked release"))
		return
	}
	report, _, problem := s.observeProjectRelease(r, acct, c)
	if problem != nil {
		api.WriteProblem(w, problem)
		return
	}
	writeJSON(w, http.StatusOK, report)
}

func (s *server) observeProjectRelease(r *http.Request, acct state.Account, c projectReleaseCandidate) (api.ProjectReleaseCheckResponse, []bindingPromotionObservation, *api.Problem) {
	report := api.ProjectReleaseCheckResponse{ProjectID: c.project.ID, Environment: c.environment, TTLSeconds: c.request.TTLSeconds, ExpectedActiveReleaseID: *c.request.ExpectedActiveReleaseID, CheckedAt: time.Now().UTC(), Passed: true, Checks: []api.BindingCheckReport{}, Members: []api.ProjectReleaseSetMemberResponse{}}
	guard, ok := s.store.(state.BindingPromotionStore)
	if !ok {
		return report, nil, api.ErrCapacity("binding revisions are unavailable")
	}
	revisions := map[string]string{}
	for i, app := range c.apps {
		revision, err := guard.ReadBindingPromotionRevision(r.Context(), acct.ID, app.ID)
		if err != nil {
			return report, nil, api.ErrCapacity("could not capture graph binding revisions")
		}
		revisions[app.ID] = revision
		current, err := s.store.AppByID(r.Context(), app.ID)
		if err != nil || current.AccountID != acct.ID || current.ProjectID != c.project.ID || current.PreviewOfSlug != "" || current.Status == state.AppDeleted || c.request.Deployments[current.Slug] != c.members[i].DeploymentID {
			return report, nil, api.NewProblem(http.StatusConflict, api.CodeConflict, "Project membership changed", "Rebuild the exact workload deployment map and retry.")
		}
		c.apps[i] = current
	}
	targets := map[string]string{}
	for i, app := range c.apps {
		targets[app.Slug] = c.members[i].DeploymentID
		report.Members = append(report.Members, api.ProjectReleaseSetMemberResponse{AppID: app.ID, DeploymentID: c.members[i].DeploymentID})
	}
	report.GraphDigest = api.ProjectReleaseGraphDigest(report)
	reader, ok := s.store.(state.ProjectReleaseSetReader)
	if !ok {
		return report, nil, api.ErrCapacity("project release sets are unavailable")
	}
	if _, err := s.store.ProjectEnvironmentBySlug(r.Context(), acct.ID, c.project.ID, c.environment); err != nil {
		return report, nil, api.NewProblem(http.StatusNotFound, api.CodeNotFound, "Environment not found", "No such project environment.")
	}
	active, err := reader.ActiveProjectReleaseSet(r.Context(), acct.ID, c.project.ID, c.environment)
	if err != nil && !errors.Is(err, state.ErrNotFound) {
		return report, nil, api.ErrCapacity("could not read active project release")
	}
	if active.ID != report.ExpectedActiveReleaseID {
		report.Passed = false
		report.Blockers = append(report.Blockers, api.BindingCheckFinding{Code: "project_release_changed", Message: "The active release differs from expected_active_release_id."})
	}
	eligibility, ok := s.store.(state.CheckedProjectReleaseSetStore)
	if !ok {
		return report, nil, api.ErrCapacity("checked project releases are unavailable")
	}
	if err := eligibility.CheckProjectReleaseEligibility(r.Context(), acct.ID, c.project.ID, c.environment, c.request.TTLSeconds, c.members); err != nil {
		report.Passed = false
		report.Blockers = append(report.Blockers, api.BindingCheckFinding{Code: "release_members_ineligible", Message: "Every selected member must remain live, retained or explicitly dark, with sufficient revision retention."})
	}
	request := r.WithContext(context.WithValue(r.Context(), bindingGraphTargetsKey{}, targets))
	var observations []bindingPromotionObservation
	for i, app := range c.apps {
		dep, depProblem := s.selectBindingVerificationDeployment(r.Context(), app, c.members[i].DeploymentID)
		if depProblem != nil || dep.AppID != app.ID || dep.Scope != c.environment || dep.Status != state.DeployLive || dep.RootfsKey == "" || dep.ImageDigest == "" || app.Manifest.RevisionPinTTLSeconds < c.request.TTLSeconds {
			report.Passed = false
			report.Blockers = append(report.Blockers, api.BindingCheckFinding{Code: "release_member_unavailable", DeploymentID: c.members[i].DeploymentID, Message: "The selected member must be a materialized live deployment in this environment with sufficient revision retention."})
			continue
		}
		graphComplete := true
		for _, binding := range app.Manifest.ServiceBindings {
			if targets[binding.Service] == "" {
				graphComplete = false
				report.Passed = false
				report.Blockers = append(report.Blockers, api.BindingCheckFinding{Code: "graph_service_target_missing", Type: api.BindingTypeService, Name: binding.Service, DeploymentID: dep.ID, Message: "The declared service must select a target in this release graph."})
			}
		}
		if !graphComplete {
			continue
		}
		o, problem := s.observeBindingPromotion(request, acct, app, dep, api.BindingPromotionRequest{}, api.DefaultBindingVerificationAge)
		if o.report.DeploymentID != "" {
			report.Checks = append(report.Checks, o.report)
		}
		if problem != nil {
			report.Passed = false
			if problem.BindingsCheck != nil {
				report.Blockers = append(report.Blockers, problem.BindingsCheck.Blockers...)
			} else {
				report.Blockers = append(report.Blockers, api.BindingCheckFinding{Code: problem.Code, DeploymentID: dep.ID, Message: problem.Detail})
			}
			continue
		}
		if o.fence.Revision != revisions[app.ID] {
			report.Passed = false
			report.Blockers = append(report.Blockers, api.BindingCheckFinding{Code: "graph_configuration_changed", DeploymentID: dep.ID, Message: "Graph configuration changed while checking; retry."})
			continue
		}
		observations = append(observations, o)
	}
	return report, observations, nil
}

func (s *server) publishCheckedProjectRelease(r *http.Request, acct state.Account, c projectReleaseCandidate) (api.ProjectReleaseSetResponse, *api.Problem) {
	report, observations, problem := s.observeProjectRelease(r, acct, c)
	if problem != nil {
		return api.ProjectReleaseSetResponse{}, problem
	}
	if !report.Passed {
		return api.ProjectReleaseSetResponse{}, projectReleaseCheckProblem(report, api.CodeProjectReleaseCheckFailed, "Resolve the per-deployment blockers before publishing this graph.")
	}
	store, ok := s.store.(state.CheckedProjectReleaseSetStore)
	if !ok || s.managedPostgresBindings == nil {
		return api.ProjectReleaseSetResponse{}, api.ErrCapacity("checked project releases are unavailable")
	}
	var release state.ProjectReleaseSet
	domains := make([]managedpostgres.AppPromotionFence, 0, len(observations))
	fences := make([]state.BindingPromotionFence, 0, len(observations))
	for _, o := range observations {
		domains = append(domains, managedpostgres.AppPromotionFence{AppID: o.fence.AppID, Fence: o.domain})
		fences = append(fences, o.fence)
	}
	err := s.managedPostgresBindings.GuardPromotions(r.Context(), acct.ID, domains, observations[0].store.BindingPromotionBackend(), func(ctx context.Context) error {
		var err error
		release, err = store.PublishProjectReleaseSetWithBindings(state.WithBindingReleaseFences(ctx, fences), acct.ID, c.project.ID, c.environment, *c.request.ExpectedActiveReleaseID, c.request.TTLSeconds, c.members)
		return err
	})
	if err != nil {
		report.Passed = false
		code := "project_release_observations_changed"
		if errors.Is(err, state.ErrBindingPromotionExpired) {
			code = "verification_expired"
		}
		report.Blockers = append(report.Blockers, api.BindingCheckFinding{Code: code, Message: "Graph, policy, eligibility or binding evidence changed before activation; recheck and retry."})
		if errors.Is(err, state.ErrConflict) || state.IsBindingReleaseRequired(err) || errors.Is(err, state.ErrBindingPromotionChanged) || errors.Is(err, state.ErrBindingPromotionExpired) || errors.Is(err, managedpostgres.ErrConflict) {
			return api.ProjectReleaseSetResponse{}, projectReleaseCheckProblem(report, api.CodeProjectReleaseCheckChanged, "The checked graph could not be activated.")
		}
		return api.ProjectReleaseSetResponse{}, api.ErrCapacity("could not publish checked project release")
	}
	report.CheckedAt = time.Now().UTC()
	response := projectReleaseSetResponse(release)
	response.BindingsCheck = &report
	return response, nil
}

func projectReleaseCheckProblem(report api.ProjectReleaseCheckResponse, code, detail string) *api.Problem {
	p := api.NewProblem(http.StatusConflict, code, "Project release check blocked activation", detail)
	p.ProjectReleaseCheck = &report
	return p
}
