package main

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) getDeploymentRuntime(w http.ResponseWriter, r *http.Request, acct state.Account) {
	dep, app, ok := s.loadCanaryDeployment(w, r, acct)
	if !ok {
		return
	}
	store, ok := s.store.(state.RuntimeReleaseStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("runtime release evidence unavailable"))
		return
	}
	response, err := s.deploymentRuntimeEvidence(r, store, acct, app, dep)
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("runtime release evidence unavailable"))
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, response)
}
func (s *server) previewRuntimeUpgrade(w http.ResponseWriter, r *http.Request, acct state.Account) {
	dep, app, ok := s.loadCanaryDeployment(w, r, acct)
	if !ok {
		return
	}
	store, ok := s.store.(state.RuntimeReleaseStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("runtime release evidence unavailable"))
		return
	}
	targetID := r.URL.Query().Get("target")
	if len(targetID) != 64 || strings.Trim(targetID, "0123456789abcdef") != "" {
		api.WriteProblem(w, api.ErrValidation("target must be a runtime release ID"))
		return
	}
	target, err := store.RuntimeReleaseByID(r.Context(), targetID)
	if errors.Is(err, state.ErrNotFound) {
		s.notFound(w, "runtime release")
		return
	}
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("runtime release evidence unavailable"))
		return
	}
	current, err := s.deploymentRuntimeEvidence(r, store, acct, app, dep)
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("runtime release evidence unavailable"))
		return
	}
	preview := runtimeUpgradePreview(current, runtimeReleaseResponse(target))
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, preview)
}
func (s *server) deploymentRuntimeEvidence(r *http.Request, store state.RuntimeReleaseStore, acct state.Account, app state.App, dep state.Deployment) (api.DeploymentRuntimeResponse, error) {
	out := api.DeploymentRuntimeResponse{DeploymentID: dep.ID, Status: "unknown", Reason: "This artifact has no recorded runtime base binding. Its exact current base cannot be inferred from its declared language or builder image.", Releases: []api.RuntimeReleaseResponse{}}
	resolved, err := state.ResolveAppForDeployment(r.Context(), s.store, app, dep)
	if err != nil {
		return out, err
	}
	if !managedRuntimePreviewWorkload(resolved, dep) {
		out.Status = "unsupported"
		out.Reason = "Runtime release pinning currently covers Gregale-managed function runtimes. Customer images and Dockerfiles retain their own runtime selection."
		return out, nil
	}
	release, err := store.RuntimeReleaseForArtifact(r.Context(), acct.ID, dep.RootfsKey)
	if errors.Is(err, state.ErrNotFound) {
		return out, nil
	}
	if err != nil {
		return out, err
	}
	out.Status = "pinned"
	out.Reason = "The artifact is bound to immutable runtime base bytes. The host kernel and function runner are separate components."
	current := runtimeReleaseResponse(release)
	out.Current = &current
	catalog, err := store.ListRuntimeReleases(r.Context(), release.Runtime, release.Architecture)
	if err != nil {
		return out, err
	}
	for _, candidate := range catalog {
		out.Releases = append(out.Releases, runtimeReleaseResponse(candidate))
	}
	return out, nil
}
func runtimeReleaseResponse(r state.RuntimeRelease) api.RuntimeReleaseResponse {
	_, digest, _ := strings.Cut(r.SourceRef, "@")
	return api.RuntimeReleaseResponse{ID: r.ID, Runtime: r.Runtime, Architecture: r.Architecture, SourceDigest: digest, GuestInitDigest: "sha256:" + r.GuestInitSHA256, BaseDigest: "sha256:" + r.BaseSHA256, LayoutVersion: r.LayoutVersion, PublishedAt: r.CreatedAt.UTC().Format(time.RFC3339Nano), Qualification: "not_evaluated"}
}
func runtimeUpgradePreview(current api.DeploymentRuntimeResponse, target api.RuntimeReleaseResponse) api.RuntimeUpgradePreviewResponse {
	p := api.RuntimeUpgradePreviewResponse{DeploymentID: current.DeploymentID, Current: current.Current, Target: target, Disposition: "blocked", Changes: []string{}, Blockers: []string{}, RequiredSteps: []string{}}
	if current.Current == nil {
		p.Blockers = append(p.Blockers, current.Reason)
		return p
	}
	c := current.Current
	if c.Runtime != target.Runtime {
		p.Blockers = append(p.Blockers, "Changing runtime families requires an explicit application migration.")
	}
	if c.Architecture != target.Architecture {
		p.Blockers = append(p.Blockers, "Target architecture differs from the current artifact.")
	}
	if len(p.Blockers) > 0 {
		return p
	}
	if c.ID == target.ID {
		p.Disposition = "no_change"
		return p
	}
	if c.SourceDigest != target.SourceDigest {
		p.Changes = append(p.Changes, "runtime_source")
	}
	if c.GuestInitDigest != target.GuestInitDigest {
		p.Changes = append(p.Changes, "guest_init")
	}
	if c.LayoutVersion != target.LayoutVersion {
		p.Changes = append(p.Changes, "base_layout")
	}
	if c.BaseDigest != target.BaseDigest {
		p.Changes = append(p.Changes, "base_bytes")
	}
	p.Disposition = "review_required"
	p.RebuildRequired = true
	p.ColdStartRequired = true
	p.Blockers = append(p.Blockers, "Target publication has not established upgrade compatibility or readiness.", "Runtime update execution is not available in this preview.")
	p.RequiredSteps = []string{"Qualify the target runtime and guest-init on native hardware.", "Rebuild the same source against the target base and produce a new artifact.", "Cold-boot the candidate and collect fresh readiness evidence; existing snapshots cannot be reused across different backing bytes.", "Use the existing guarded rollout and rollback controls after candidate qualification."}
	return p
}

func managedRuntimePreviewWorkload(app state.App, dep state.Deployment) bool {
	if app.Type != state.AppTypeFunction || dep.Kind == state.DeploymentKindImage || dep.Kind == state.DeploymentKindDockerfile {
		return false
	}
	frozen, err := dep.ScopedWorkloadRuntime()
	if err != nil {
		return false
	}
	if frozen != nil && frozen.Source != nil {
		return frozen.Source.Kind != "dockerfile" && frozen.Source.Dockerfile == ""
	}
	return app.Manifest.BuildDockerfile == ""
}
