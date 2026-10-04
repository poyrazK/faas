package main

// adr: 480
import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/globaldirectory"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) putGlobalImageRelease(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if !s.globalDirectoryAvailable(w) {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), api.GlobalImageReleaseRequestTimeout)
	defer cancel()
	var req api.PutGlobalImageReleaseRequest
	if !decodeGlobalImageRelease(w, r, &req) {
		return
	}
	slug, releaseID := r.PathValue("slug"), r.PathValue("release_id")
	if !globaldirectory.ValidReleaseInput(releaseID, req.Image) || !validGlobalReleaseKeys(req.RegionalAPIKeys) {
		api.WriteProblem(w, api.ErrValidation("use a canonical release UUID, a digest-pinned image and regional API keys indexed by canonical region"))
		return
	}
	if _, err := s.globalDirectory.PlanRelease(ctx, acct.ID, slug, releaseID, req.Image); err != nil {
		s.globalImageReleaseError(w, r, acct, err)
		return
	}
	coordinator := globaldirectory.Reconciler{Store: s.globalDirectory, Verifier: s.globalVerifier}
	release, err := coordinator.Resume(ctx, acct.ID, slug, releaseID, req.RegionalAPIKeys)
	if err != nil {
		s.globalImageReleaseError(w, r, acct, err)
		return
	}
	s.audit.Emit(ctx, "global_image_release.reconciled", &acct.ID, map[string]any{"global_app_id": release.GlobalAppID, "release_id": release.ReleaseID, "generation": release.Generation, "status": release.Status()})
	writeJSON(w, http.StatusAccepted, globalImageReleaseResponse(release))
}

func (s *server) getGlobalImageRelease(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if !s.globalDirectoryAvailable(w) {
		return
	}
	if !globaldirectory.ValidID(r.PathValue("release_id")) {
		api.WriteProblem(w, api.ErrValidation("use a canonical nonzero release UUID"))
		return
	}
	release, err := s.globalDirectory.GetRelease(r.Context(), acct.ID, r.PathValue("slug"), r.PathValue("release_id"))
	if err != nil {
		s.globalImageReleaseError(w, r, acct, err)
		return
	}
	writeJSON(w, http.StatusOK, globalImageReleaseResponse(release))
}

func decodeGlobalImageRelease(w http.ResponseWriter, r *http.Request, out *api.PutGlobalImageReleaseRequest) bool {
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, api.GlobalImageReleaseRequestMaxBytes))
	decoder.DisallowUnknownFields()
	if decoder.Decode(out) != nil || decoder.Decode(&struct{}{}) != io.EOF {
		api.WriteProblem(w, api.ErrValidation("send one bounded global image release object"))
		return false
	}
	return true
}

func validGlobalReleaseKeys(keys map[string]string) bool {
	if len(keys) > api.GlobalImageReleaseTargetsMax {
		return false
	}
	for region, key := range keys {
		if region == "" || !api.ValidComputeRegion(region) || !api.ValidAPIKeyFormat(key) {
			return false
		}
	}
	return true
}

func (s *server) globalImageReleaseError(w http.ResponseWriter, r *http.Request, acct state.Account, err error) {
	var bound *globaldirectory.ReleaseLimitError
	switch {
	case errors.Is(err, globaldirectory.ErrNoTargets):
		api.WriteProblem(w, api.NewProblem(http.StatusConflict, api.CodeGlobalImageReleaseNoTargets, "No selected regions", "Attach at least one verified regional app before allocating a release."))
	case errors.As(err, &bound):
		api.WriteProblem(w, api.NewProblem(http.StatusConflict, api.CodeGlobalImageReleaseLimit, "Global release bound reached", "The selected target count or global generation exceeds the documented protocol bounds.").WithLimit(bound.Limit, bound.Observed).WithDocs("https://gregale.dev/docs/global-app-directory#global-image-releases"))
	default:
		s.globalDirectoryError(w, r, acct, err)
	}
}

func globalImageReleaseResponse(release globaldirectory.Release) api.GlobalImageReleaseResponse {
	out := api.GlobalImageReleaseResponse{GlobalAppID: release.GlobalAppID, ReleaseID: release.ReleaseID, Generation: release.Generation, DesiredGeneration: release.DesiredGeneration, Image: release.Image, Status: release.Status(), CreatedAt: release.CreatedAt, Targets: make([]api.GlobalImageReleaseTargetResponse, 0, len(release.Targets))}
	for _, target := range release.Targets {
		var deploymentID *string
		if target.DeploymentID != "" {
			id := target.DeploymentID
			deploymentID = &id
		}
		out.Targets = append(out.Targets, api.GlobalImageReleaseTargetResponse{CellID: target.CellID, Region: target.Region, CellGeneration: target.CellGeneration, RegionalAccountID: target.RegionalAccountID, RegionalAppID: target.RegionalAppID, RegionalAppSlug: target.RegionalAppSlug, Outcome: target.Outcome, Diagnostic: target.Diagnostic, DeploymentID: deploymentID, UpdatedAt: target.UpdatedAt})
	}
	return out
}
