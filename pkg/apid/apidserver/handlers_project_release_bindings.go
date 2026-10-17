package apidserver

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/google/uuid"
	"maps"
	"net/http"
	"sort"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/bindings"
	"github.com/onebox-faas/faas/pkg/managedpostgres"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) prepareCheckedProjectRelease(w http.ResponseWriter, r *http.Request, acct state.Account) {
	project, _, ok := s.projectReleaseReadContext(w, r, acct)
	if !ok {
		return
	}
	var request api.PublishProjectReleaseSetRequest
	if err := decodeJSON(r, &request); err != nil {
		api.WriteProblem(w, api.ErrValidation(err.Error()))
		return
	}
	catalog, ok := s.store.(bindings.ProjectReleaseCatalog)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("checked graph preparation is unavailable"))
		return
	}
	manifest, problem := bindings.PrepareProjectRelease(r.Context(), catalog, acct.ID, project, r.PathValue("environment"), request)
	if problem != nil {
		api.WriteProblem(w, problem)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, manifest)
}

func (s *server) activateCheckedProjectRelease(w http.ResponseWriter, r *http.Request, acct state.Account) {
	project, _, ok := s.projectReleaseReadContext(w, r, acct)
	if !ok {
		return
	}
	var manifest api.CheckedProjectReleaseManifest
	if err := decodeJSON(r, &manifest); err != nil {
		api.WriteProblem(w, api.ErrValidation(err.Error()))
		return
	}
	if manifest.RollbackReleaseID != "" || manifest.Deactivate {
		api.WriteProblem(w, api.ErrValidation("use the matching checked recovery endpoint for rollback or deactivation manifests"))
		return
	}
	response, problem := s.executeCheckedProjectRelease(r, acct, project, manifest)
	if problem != nil {
		api.WriteProblem(w, problem)
		return
	}
	s.audit.Emit(r.Context(), "project.release_set_published", &acct.ID, map[string]any{"project_id": project.ID, "environment": manifest.Environment, "release_id": response.Release.ID, "require_bindings": true})
	writeJSON(w, http.StatusCreated, response)
}

// A private context marks which frozen graph selectors must still match the
// app configuration reloaded after capturing its promotion revision.
type bindingGraphSelectionKey struct{}
type bindingGraphSelection struct{ deployments, targets map[string]string }

func (s *server) executeCheckedProjectRelease(r *http.Request, acct state.Account, project state.Project, manifest api.CheckedProjectReleaseManifest) (api.CheckedProjectReleaseResponse, *api.Problem) {
	response := api.CheckedProjectReleaseResponse{BindingsChecks: map[string]api.BindingCheckReport{}}
	store, ok := s.store.(state.CheckedProjectReleaseStore)
	if !ok || s.managedPostgresBindings == nil {
		return response, api.ErrCapacity("checked graph activation is unavailable")
	}
	apps, err := s.store.AppsForProject(r.Context(), acct.ID, project.ID)
	if err != nil {
		return response, api.ErrCapacity("could not read project workloads")
	}
	members, problem := bindings.ValidateCheckedProjectRelease(manifest, project.Slug, r.PathValue("environment"), apps)
	if problem != nil {
		return response, problem
	}
	for i, member := range members {
		app, err := s.store.AppByID(r.Context(), member.AppID)
		if err != nil {
			return response, api.ErrCapacity("could not resolve graph member")
		}
		deployment, problem := s.selectBindingVerificationDeployment(r.Context(), app, member.DeploymentID)
		if problem != nil {
			return response, problem
		}
		members[i].DeploymentID = deployment.ID
	}
	if err := s.durable.CheckProjectValidatorMembers(r.Context(), members); err != nil {
		return response, api.ErrCapacity("validator artifact preflight blocked graph activation")
	}
	sort.Slice(apps, func(i, j int) bool { return apps[i].ID < apps[j].ID })
	var fences []state.BindingPromotionFence
	var domains []managedpostgres.PromotionSelection
	var fallback []state.ProjectReleaseMember
	for _, app := range apps {
		deployment, problem := s.selectBindingVerificationDeployment(r.Context(), app, manifest.Deployments[app.Slug])
		if problem != nil {
			return response, problem
		}
		if deployment.Scope != manifest.Environment || deployment.EnvironmentWorkloadHeld() {
			return response, api.ErrValidation("candidate deployment must belong to the graph environment and be eligible")
		}
		selection := bindingGraphSelection{deployments: manifest.Deployments, targets: manifest.ServiceTargets[app.Slug]}
		request := r.Clone(context.WithValue(r.Context(), bindingGraphSelectionKey{}, selection))
		observation, problem := s.observeBindingPromotion(request, acct, app, deployment, api.BindingPromotionRequest{}, api.DefaultBindingVerificationAge)
		response.BindingsChecks[app.Slug] = observation.report
		if problem != nil {
			return response, problem
		}
		if observation.fence.PolicyRevision != manifest.ExpectedPolicyRevisions[app.Slug] {
			return response, api.NewProblem(http.StatusConflict, "bindings_check_changed", "Release policy changed", "Prepare and qualify the new graph policy before activating.")
		}
		fences = append(fences, observation.fence)
		domains = append(domains, managedpostgres.PromotionSelection{AccountID: acct.ID, AppID: app.ID, Fence: observation.domain})
		if id := manifest.ExpectedFallbackDeployments[app.Slug]; id != "" {
			current, err := bindings.ProjectReleaseDeployment(r.Context(), s.store, id)
			if err != nil || current.AppID != app.ID {
				return response, api.NewProblem(http.StatusConflict, "release_graph_changed", "Weighted routing changed", "Prepare and qualify again before activating.")
			}
			fallback = append(fallback, state.ProjectReleaseMember{AppID: app.ID, DeploymentID: current.ID})
		}
	}
	if len(fallback) != len(manifest.ExpectedFallbackDeployments) {
		return response, api.ErrValidation("fallback selectors must name project members")
	}
	root := s.store.(state.BindingPromotionStore)
	var release state.ProjectReleaseSet
	// Both catalogs hold all selected member fences through the graph write.
	err = s.managedPostgresBindings.GuardPromotions(r.Context(), domains, root.BindingPromotionBackend(), func(ctx context.Context) error {
		var err error
		if manifest.Deactivate {
			release, err = store.DeactivateProjectReleaseWithBindings(ctx, acct.ID, project.ID, manifest.Environment, *manifest.ExpectedActiveReleaseID, fallback, fences)
		} else if manifest.RollbackReleaseID != "" {
			if len(fallback) != 0 {
				return state.ErrInvalidArgument
			}
			release, err = store.RollbackProjectReleaseWithBindings(ctx, acct.ID, project.ID, manifest.Environment, *manifest.ExpectedActiveReleaseID, manifest.RollbackReleaseID, manifest.TTLSeconds, members, fences)
		} else {
			release, err = store.ActivateProjectReleaseWithBindings(ctx, acct.ID, project.ID, manifest.Environment, *manifest.ExpectedActiveReleaseID, fallback, manifest.TTLSeconds, members, fences)
		}
		return err
	})
	if err != nil {
		if errors.Is(err, state.ErrConflict) || errors.Is(err, state.ErrServiceRolloutNotReady) {
			return response, api.NewProblem(http.StatusConflict, "release_graph_changed", "Release graph changed", "The active graph, membership, candidate eligibility or service readiness changed; prepare and qualify again.")
		}
		return response, bindingPromotionWriteProblem(err, api.BindingCheckReport{}, api.BindingCheckFinding{Code: "verification_expired", Message: "Graph qualification expired before activation; verify again."})
	}
	response.Release = projectReleaseSetResponse(release)
	return response, nil
}

func bindingGraphInventoryRequest(r *http.Request, app state.App) (*http.Request, *api.Problem) {
	selection, ok := r.Context().Value(bindingGraphSelectionKey{}).(bindingGraphSelection)
	if !ok {
		return r, nil
	}
	if !maps.Equal(selection.targets, bindings.ProjectServiceTargets(app, selection.deployments)) {
		return r, api.NewProblem(http.StatusConflict, "bindings_check_changed", "Service graph changed", "Prepare and qualify the updated service graph before activating.")
	}
	encoded, err := json.Marshal(selection.targets)
	if err != nil {
		return r, api.ErrInternal("could not encode service target selection")
	}
	request := r.Clone(r.Context())
	query := request.URL.Query()
	query.Set("service_targets", string(encoded))
	request.URL.RawQuery = query.Encode()
	return request, nil
}

func (s *server) prepareCheckedProjectRollback(w http.ResponseWriter, r *http.Request, acct state.Account) {
	project, _, ok := s.projectReleaseReadContext(w, r, acct)
	if !ok {
		return
	}
	var request api.PrepareProjectRollbackRequest
	if err := decodeJSON(r, &request); err != nil {
		api.WriteProblem(w, api.ErrValidation(err.Error()))
		return
	}
	catalog, ok := s.store.(bindings.ProjectRollbackCatalog)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("checked rollback preparation is unavailable"))
		return
	}
	manifest, problem := bindings.PrepareProjectRollback(r.Context(), catalog, acct.ID, project, r.PathValue("environment"), request)
	if problem != nil {
		api.WriteProblem(w, problem)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, manifest)
}

func (s *server) rollbackCheckedProjectRelease(w http.ResponseWriter, r *http.Request, acct state.Account) {
	project, _, ok := s.projectReleaseReadContext(w, r, acct)
	if !ok {
		return
	}
	var manifest api.CheckedProjectReleaseManifest
	if err := decodeJSON(r, &manifest); err != nil {
		api.WriteProblem(w, api.ErrValidation(err.Error()))
		return
	}
	id, err := uuid.Parse(manifest.RollbackReleaseID)
	if manifest.Deactivate || err != nil || id == uuid.Nil || id.String() != manifest.RollbackReleaseID || manifest.ExpectedActiveReleaseID == nil || *manifest.ExpectedActiveReleaseID == "" || *manifest.ExpectedActiveReleaseID == manifest.RollbackReleaseID || len(manifest.ExpectedFallbackDeployments) != 0 {
		api.WriteProblem(w, api.ErrValidation("rollback requires explicit different historical and current releases without fallback selectors"))
		return
	}
	response, problem := s.executeCheckedProjectRelease(r, acct, project, manifest)
	if problem != nil {
		api.WriteProblem(w, problem)
		return
	}
	s.audit.Emit(r.Context(), "project.release_set_rolled_back", &acct.ID, map[string]any{"project_id": project.ID, "environment": manifest.Environment, "release_id": response.Release.ID, "source_release_id": manifest.RollbackReleaseID, "previous_release_id": *manifest.ExpectedActiveReleaseID, "require_bindings": true})
	writeJSON(w, http.StatusCreated, response)
}

func (s *server) prepareCheckedProjectDeactivation(w http.ResponseWriter, r *http.Request, acct state.Account) {
	project, _, ok := s.projectReleaseReadContext(w, r, acct)
	if !ok {
		return
	}
	catalog, ok := s.store.(bindings.ProjectReleaseCatalog)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("checked deactivation preparation is unavailable"))
		return
	}
	manifest, problem := bindings.PrepareProjectDeactivation(r.Context(), catalog, acct.ID, project, r.PathValue("environment"))
	if problem != nil {
		api.WriteProblem(w, problem)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, manifest)
}
func (s *server) deactivateCheckedProjectRelease(w http.ResponseWriter, r *http.Request, acct state.Account) {
	project, _, ok := s.projectReleaseReadContext(w, r, acct)
	if !ok {
		return
	}
	var manifest api.CheckedProjectReleaseManifest
	if err := decodeJSON(r, &manifest); err != nil {
		api.WriteProblem(w, api.ErrValidation(err.Error()))
		return
	}
	if !manifest.Deactivate || manifest.RollbackReleaseID != "" {
		api.WriteProblem(w, api.ErrValidation("deactivation requires explicit deactivation intent"))
		return
	}
	response, problem := s.executeCheckedProjectRelease(r, acct, project, manifest)
	if problem != nil {
		api.WriteProblem(w, problem)
		return
	}
	s.audit.Emit(r.Context(), "project.release_set_deactivated", &acct.ID, map[string]any{"project_id": project.ID, "environment": manifest.Environment, "release_id": response.Release.ID, "deactivation": response.Release.Deactivation, "require_bindings": true})
	writeJSON(w, http.StatusOK, response)
}
