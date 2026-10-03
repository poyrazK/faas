package routerequirements

import (
	"encoding/json"
	"errors"

	"github.com/onebox-faas/faas/pkg/api"
)

// ConfigurationFingerprint shares planning/check provenance without evaluation.
func ConfigurationFingerprint(context Context, planName string) string {
	return configurationDigest(context, planName)
}

// BoundAutomaticCheck preserves an incomplete verdict rather than retrying an
// oversized deterministic result indefinitely or persisting a partial pass.
func BoundAutomaticCheck(check api.RouteRequirementsCheck) (api.RouteRequirementsCheck, error) {
	body, err := json.Marshal(check)
	if err != nil || len(body) <= api.RouteCheckMaxResultBytes {
		return check, err
	}
	check.Report = api.RouteRequirementsReport{Version: 2, SHA256: check.RequirementsSHA256, Status: "unknown", PolicyScope: "current_app",
		Scope: "Automatic route evidence exceeded the result limit; no partial passing evidence is retained.", Routes: []api.RouteRequirementsResult{},
		Coverage: &api.RouteCoverageInventory{Status: "unavailable", Code: "automatic_result_limit", Source: "captured_candidate_contract", Deployment: check.DeploymentID}}
	return check, nil
}

// BuildSavedCheck is read-only and requires complete captured coverage even
// when all declared checks happen to pass for an individual concrete route.
func BuildSavedCheck(saved api.SavedRouteRequirements, context Context, planName, deploymentID string, inventory CoverageInventory) (api.RouteRequirementsCheck, error) {
	config, digest, err := NormalizeCoverage(saved.Requirements)
	if err != nil {
		return api.RouteRequirementsCheck{}, err
	}
	if config.Version != 2 || digest != saved.SHA256 || saved.AppID != context.App.ID || saved.Revision < 1 || saved.Revision > api.RouteRequirementsMaxRevision {
		return api.RouteRequirementsCheck{}, errors.New("saved route requirements identity or integrity is unavailable")
	}
	return api.RouteRequirementsCheck{Version: 1, App: context.App.Slug, AppID: context.App.ID,
		DeploymentID: deploymentID, RequirementsRevision: saved.Revision, RequirementsSHA256: digest,
		ConfigurationSHA256: configurationDigest(context, planName), Report: EvaluatePreview(config, digest, context, inventory).Report}, nil
}
