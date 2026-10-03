package state

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

var ErrRouteRequirementsRevision = errors.New("saved route requirements revision is stale")

// RouteRequirementsChecker evaluates isolated inputs inside a consistent read.
// It must be pure: no state-store calls, network requests or other side effects.
type RouteRequirementsChecker func(RoutePolicySnapshot, api.SavedRouteRequirements) (api.RouteRequirementsCheck, error)

type RouteRequirementsStore interface {
	SaveRouteRequirements(context.Context, string, string, api.SaveRouteRequirementsRequest) (api.SavedRouteRequirements, error)
	GetSavedRouteRequirements(context.Context, string, string) (api.SavedRouteRequirements, error)
	CheckRouteRequirements(context.Context, string, string, api.CheckRouteRequirementsRequest, RouteRequirementsChecker) (api.RouteRequirementsCheck, error)
}

var (
	_ RouteRequirementsStore = (*MemStore)(nil)
	_ RouteRequirementsStore = (*PgStore)(nil)
)

func NormalizeSavedRouteRequirements(request api.SaveRouteRequirementsRequest) (api.RouteRequirementsConfig, string, error) {
	if request.ExpectedRevision == nil || *request.ExpectedRevision < 0 || *request.ExpectedRevision > api.RouteRequirementsMaxRevision {
		return api.RouteRequirementsConfig{}, "", errors.New("expected_revision must be explicitly supplied within the supported range; use 0 for the first save")
	}
	config := request.Requirements
	if config.Version != 2 {
		return config, "", errors.New("saved deployment requirements need version 2 groups or exact assignments for complete inventory coverage")
	}
	body, err := json.Marshal(config)
	if err != nil {
		return config, "", fmt.Errorf("encode saved requirements: %w", err)
	}
	if len(body) > api.RouteRequirementsMaxBytes || len(config.Groups) > api.RouteCoverageMaxGroups || len(config.Routes) > api.RouteRequirementsMaxRoutes || len(config.Public) > api.RouteRequirementsMaxRoutes || len(config.Groups)+len(config.Routes)+len(config.Public) == 0 {
		return config, "", errors.New("saved route requirements are empty or exceed the supported limits")
	}
	// The API validates semantics before calling the store. Keep canonicalization
	// and privacy defense here without introducing an evaluator/state cycle.
	return api.CanonicalRouteRequirements(config)
}

func validateSavedRouteRequirements(saved api.SavedRouteRequirements, appID string) error {
	if saved.AppID != appID || saved.Revision < 1 || saved.Revision > api.RouteRequirementsMaxRevision {
		return errors.New("invalid saved route requirements identity or revision")
	}
	for _, exception := range saved.Requirements.Public {
		if exception.Reason != api.RoutePublicExceptionMarker {
			return errors.New("saved route requirements commentary is not normalized")
		}
	}
	config, digest, err := api.CanonicalRouteRequirements(saved.Requirements)
	if err != nil || config.Version != 2 || digest != saved.SHA256 {
		return errors.New("saved route requirements integrity could not be established")
	}
	return nil
}

func copySavedRouteRequirements(saved api.SavedRouteRequirements) (api.SavedRouteRequirements, error) {
	body, err := json.Marshal(saved)
	if err != nil {
		return api.SavedRouteRequirements{}, fmt.Errorf("copy saved route requirements: %w", err)
	}
	var isolated api.SavedRouteRequirements
	err = json.Unmarshal(body, &isolated)
	return isolated, err
}

func checkSavedRevision(saved api.SavedRouteRequirements, expected *int64) error {
	if expected != nil && *expected != saved.Revision {
		return ErrRouteRequirementsRevision
	}
	return nil
}

func ValidateCheckRouteRequirements(request api.CheckRouteRequirementsRequest) error {
	if id, err := uuid.Parse(request.DeploymentID); err != nil || id.String() != request.DeploymentID {
		return errors.New("deployment_id must be a canonical deployment UUID")
	}
	if request.ExpectedRevision != nil && (*request.ExpectedRevision < 1 || *request.ExpectedRevision > api.RouteRequirementsMaxRevision) {
		return errors.New("expected_revision must name an existing saved revision within the supported range")
	}
	return nil
}
