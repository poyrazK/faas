package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/oci"
	"github.com/onebox-faas/faas/pkg/state"
)

// publishAppImage accepts the immutable artifact after CI has published it.
// Authentication uses the existing app-bound deploy token / deploy:write path.
func (s *server) publishAppImage(w http.ResponseWriter, r *http.Request, acct state.Account) {
	app, ok, limits := s.loadAppAndPreflight(w, r, acct)
	if !ok {
		return
	}
	var req api.CreateDeploymentRequest
	if err := decodeJSON(r, &req); err != nil {
		api.WriteProblem(w, api.ErrValidation(err.Error()))
		return
	}
	if problem := lifecycleProblem(acct.Plan, apiManifestFromState(app.Manifest), app.MaxConcurrency); problem != nil {
		api.WriteProblem(w, problem)
		return
	}
	s.createImageDeployment(w, r, acct, app, limits, req, true)
}

func validatePublishedImage(app state.App, image string) (string, *api.Problem) {
	if !api.ValidProjectImage(app.Manifest.ProjectImage) {
		return "", api.ErrValidation("image-published requires a project workload configured with image:")
	}
	if !api.ValidDeploymentImage(image) || !api.ValidProjectImage(image) {
		return "", api.ErrValidation("image-published requires a full digest-pinned image reference")
	}
	declared, err := oci.ParseReference(app.Manifest.ProjectImage)
	if err != nil {
		return "", api.ErrValidation("invalid project image configuration")
	}
	published, err := oci.ParseReference(image)
	if err != nil || declared.Registry != published.Registry || declared.Repository != published.Repository {
		return "", api.ErrValidation("published image must match the workload's configured registry and repository")
	}
	if declared.Digest != "" && declared.Digest != published.Digest {
		return "", api.ErrValidation("workload pins a different digest; update its image declaration before publishing")
	}
	return published.String(), nil
}

func publishedImageScope(scope string) string {
	if scope == "" {
		return api.DefaultEnvScope
	}
	return scope
}

func publishedImageDeploymentID(appID, scope, image string) string {
	identity := "gregale:image-published:v1\x00" + appID + "\x00" + publishedImageScope(scope) + "\x00" + image
	return uuid.NewSHA1(uuid.NameSpaceURL, []byte(identity)).String()
}

// Returning the durable row also covers terminal deliveries. A publisher
// retry must never resurrect a failed/cancelled release or undo a rollback.
func (s *server) replyPublishedImageReplay(w http.ResponseWriter, r *http.Request, app state.App, req api.CreateDeploymentRequest) bool {
	id := publishedImageDeploymentID(app.ID, req.Scope, req.Image)
	d, err := s.store.DeploymentByID(r.Context(), id)
	if errors.Is(err, state.ErrNotFound) {
		return false
	}
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not inspect published image delivery"))
		return true
	}
	if d.AppID != app.ID || d.Kind != state.DeploymentKindImage || d.Scope != publishedImageScope(req.Scope) || d.ImageDigest != req.Image {
		api.WriteProblem(w, api.ErrCapacity("published image delivery identity mismatch"))
		return true
	}
	writeJSON(w, http.StatusOK, s.deploymentResponse(d, app))
	return true
}

func (s *server) preparePublishedImage(ctx context.Context, app state.App, req *api.CreateDeploymentRequest) (state.Deployment, *api.Problem) {
	reader, ok := s.store.(interface {
		LatestDeploymentForScope(context.Context, string, string) (state.Deployment, error)
	})
	if !ok {
		return state.Deployment{}, api.ErrCapacity("scope-aware deployment lookup unavailable")
	}
	previous, err := reader.LatestDeploymentForScope(ctx, app.ID, publishedImageScope(req.Scope))
	if err != nil && !errors.Is(err, state.ErrNotFound) {
		return state.Deployment{}, api.ErrCapacity("could not read image workload configuration")
	}
	if previous.Kind != state.DeploymentKindImage {
		previous = state.Deployment{}
	}
	if req.Workflows == nil && len(previous.Workflows) > 0 {
		if err := json.Unmarshal(previous.Workflows, &req.Workflows); err != nil {
			return state.Deployment{}, api.ErrCapacity("could not read image workflow definitions")
		}
	}
	if app.Manifest.ProjectImagePort != 0 {
		if req.Overrides == nil {
			req.Overrides = &api.CreateDeploymentOverrides{}
		}
		if req.Overrides.Port == 0 {
			req.Overrides.Port = app.Manifest.ProjectImagePort
		}
	}
	if req.Reason == nil {
		reason := "image-published"
		req.Reason = &reason
	}
	return previous, nil
}
