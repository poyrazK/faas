package main

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/url"
	"reflect"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) getPreviewStatus(w http.ResponseWriter, r *http.Request, acct state.Account) {
	preview, ok := s.loadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return
	}
	if preview.PreviewOfSlug == "" {
		api.WriteProblem(w, previewNotFoundProblem())
		return
	}
	response, err := s.previewResource(r.Context(), acct, preview)
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not load preview resource"))
		return
	}
	writeJSON(w, http.StatusOK, response)
}

func (s *server) previewResource(ctx context.Context, acct state.Account, preview state.App) (api.PreviewResourceResponse, error) {
	app := s.appResponseWithContext(ctx, preview, acct.Plan)
	response := api.PreviewResourceResponse{
		App: app, Links: previewResourceLinks(app),
		Changes: api.PreviewProductionChangesResponse{ConfigurationChangedGroups: []string{}},
	}
	latest, err := s.previewDeployment(ctx, preview)
	if err != nil {
		return response, err
	}
	response.LatestDeployment = latest
	parent, err := s.store.AppBySlug(ctx, preview.PreviewOfSlug)
	if errors.Is(err, state.ErrNotFound) || (err == nil && parent.AccountID != acct.ID) {
		return response, nil
	}
	if err != nil {
		return response, err
	}
	parentResponse := s.appResponseWithContext(ctx, parent, acct.Plan)
	response.Parent = &parentResponse
	response.ProductionDeployment, err = s.previewDeployment(ctx, parent)
	if err != nil {
		return response, err
	}
	response.Changes = previewProductionChanges(parent, preview, response.ProductionDeployment, latest)
	return response, nil
}

func (s *server) previewDeployment(ctx context.Context, app state.App) (*api.DeploymentResponse, error) {
	deployment, err := s.store.LatestDeployment(ctx, app.ID)
	if errors.Is(err, state.ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	response := s.deploymentResponse(deployment, app)
	return &response, nil
}

func previewProductionChanges(parent, preview state.App, production, candidate *api.DeploymentResponse) api.PreviewProductionChangesResponse {
	before, after := previewArtifact(production), previewArtifact(candidate)
	return api.PreviewProductionChangesResponse{
		ArtifactChanged:            previewArtifactIdentity(before) != previewArtifactIdentity(after),
		PreviewArtifact:            after,
		ProductionArtifact:         before,
		ConfigurationChangedGroups: previewConfigurationChangedGroups(parent, preview),
	}
}

func previewArtifact(deployment *api.DeploymentResponse) api.PreviewArtifactResponse {
	if deployment == nil {
		return api.PreviewArtifactResponse{}
	}
	return api.PreviewArtifactResponse{
		DeploymentID: deployment.ID, Revision: deployment.Revision, Status: deployment.Status,
		ImageDigest: deployment.ImageDigest, SourceSHA256: deployment.SourceSHA256,
		CommitSHA: deployment.CommitSHA, BuildID: deployment.BuildID,
	}
}

func previewArtifactIdentity(artifact api.PreviewArtifactResponse) string {
	for _, identity := range []string{artifact.ImageDigest, artifact.SourceSHA256, artifact.CommitSHA, artifact.BuildID, artifact.DeploymentID} {
		if identity != "" {
			return identity
		}
	}
	return ""
}

func previewConfigurationChangedGroups(parent, preview state.App) []string {
	groups := make([]string, 0, 5)
	if parent.Type != preview.Type || parent.Runtime != preview.Runtime || parent.WorkloadClass != preview.WorkloadClass ||
		parent.AppProtocol != preview.AppProtocol || parent.StartCommand != preview.StartCommand || !reflect.DeepEqual(parent.Manifest, preview.Manifest) {
		groups = append(groups, "runtime")
	}
	if parent.RAMMB != preview.RAMMB || parent.CPUMillicores != preview.CPUMillicores {
		groups = append(groups, "resources")
	}
	if parent.IdleTimeoutS != preview.IdleTimeoutS || parent.MaxConcurrency != preview.MaxConcurrency || parent.MinInstances != preview.MinInstances ||
		parent.AutoscaleTargetRPS != preview.AutoscaleTargetRPS || parent.AutoscaleTargetCPUPct != preview.AutoscaleTargetCPUPct ||
		parent.WarmPoolSize != preview.WarmPoolSize || !reflect.DeepEqual(parent.ScalingPolicy, preview.ScalingPolicy) {
		groups = append(groups, "scaling")
	}
	if previewRoutingChanged(parent, preview) {
		groups = append(groups, "routing")
	}
	if previewSecurityChanged(parent, preview) || !bytes.Equal(parent.RetryPolicyJSON, preview.RetryPolicyJSON) {
		groups = append(groups, "policies")
	}
	return groups
}

func previewRoutingChanged(parent, preview state.App) bool {
	return parent.Visibility != preview.Visibility || parent.MaintenanceMode != preview.MaintenanceMode ||
		parent.OnlyAllowDeclaredRoutes != preview.OnlyAllowDeclaredRoutes || !reflect.DeepEqual(parent.DeclaredRoutes, preview.DeclaredRoutes) ||
		parent.StreamingEnabled != preview.StreamingEnabled || parent.WebSocketEnabled != preview.WebSocketEnabled ||
		parent.RouteMetricsEnabled != preview.RouteMetricsEnabled || !reflect.DeepEqual(parent.CORSDefaultEnabled, preview.CORSDefaultEnabled) ||
		!reflect.DeepEqual(parent.CORSDefaultOrigins, preview.CORSDefaultOrigins)
}

func previewSecurityChanged(parent, preview state.App) bool {
	return parent.RequireSigned != preview.RequireSigned || parent.SecurityPolicy != preview.SecurityPolicy ||
		parent.RequireAuthn != preview.RequireAuthn || parent.PublicAuthMode != preview.PublicAuthMode ||
		parent.ConsumerAuthMode != preview.ConsumerAuthMode || !reflect.DeepEqual(parent.EgressAllowlist, preview.EgressAllowlist) ||
		!reflect.DeepEqual(parent.StaticEgressIP, preview.StaticEgressIP) || !reflect.DeepEqual(parent.PublicAuthIPAllowlist, preview.PublicAuthIPAllowlist)
}

func previewResourceLinks(app api.AppResponse) api.PreviewResourceLinksResponse {
	publicURL := app.CanonicalURL
	if publicURL == "" {
		publicURL = app.URL
	}
	base := "/v1/apps/" + url.PathEscape(app.Slug)
	return api.PreviewResourceLinksResponse{
		URL: publicURL, Logs: base + "/logs", Metrics: base + "/metrics", Configuration: base,
	}
}

func previewNotFoundProblem() *api.Problem {
	return api.NewProblem(http.StatusNotFound, "preview_not_found", "Preview not found",
		"the slug does not identify a preview app; use the app endpoint for a production app")
}
