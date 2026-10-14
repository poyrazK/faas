// Package trafficguard validates optional conditions on traffic updates.
package trafficguard

import (
	"context"
	"regexp"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

var deploymentIDPattern = regexp.MustCompile(`^(?:[0-9a-fA-F]{32}|[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12})$`)

// Prepare binds the public concurrency condition to authorized account/app IDs.
func Prepare(ctx context.Context, accountID, appID string, req *api.UpdateDeploymentTrafficRequest) (context.Context, *api.Problem) {
	if req.ExpectedServingDeploymentID != nil && *req.ExpectedServingDeploymentID != "" {
		parsed, err := uuid.Parse(*req.ExpectedServingDeploymentID)
		if err != nil || !deploymentIDPattern.MatchString(*req.ExpectedServingDeploymentID) {
			return ctx, api.ErrValidation("expected_serving_deployment_id must be a deployment id")
		}
		canonical := parsed.String()
		req.ExpectedServingDeploymentID = &canonical
	}
	if req.ExpectedMCPAccessPolicy != nil {
		if err := req.ExpectedMCPAccessPolicy.Validate(); err != nil {
			return ctx, api.ErrValidation(err.Error())
		}
		ctx = state.WithMCPAccessPolicyExpectation(ctx, accountID, appID, *req.ExpectedMCPAccessPolicy)
	}
	return ctx, nil
}
