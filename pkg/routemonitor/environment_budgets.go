package routemonitor

import (
	"errors"
	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

func ValidateEnvironmentBudgets(req api.SetEnvironmentRouteBudgetsRequest) error {
	if _, err := uuid.Parse(req.AppID); err != nil {
		return errors.New("app_id must be a UUID")
	}
	if id, err := uuid.Parse(req.RequestID); err != nil || id == uuid.Nil {
		return errors.New("request_id must be a nonzero UUID")
	}
	if req.ExpectedAppRevision == nil || *req.ExpectedAppRevision < 0 || *req.ExpectedAppRevision > api.RouteRequirementsMaxRevision {
		return errors.New("expected_app_revision is required")
	}
	if (req.ProjectID == "") != (req.EnvironmentID == "") {
		return errors.New("project_id and environment_id are required together")
	}
	if req.EnvironmentID != "" {
		if _, err := uuid.Parse(req.ProjectID); err != nil {
			return errors.New("project_id must be a UUID")
		}
		if _, err := uuid.Parse(req.EnvironmentID); err != nil {
			return errors.New("environment_id must be a UUID")
		}
	}
	if req.Inherit && (req.Enabled || len(req.Routes) != 0) {
		return errors.New("inherit requires enabled=false and empty routes")
	}
	return Validate(api.SetRouteMonitorRequest{Enabled: req.Enabled, ExpectedRevision: req.ExpectedRevision, Routes: req.Routes})
}
