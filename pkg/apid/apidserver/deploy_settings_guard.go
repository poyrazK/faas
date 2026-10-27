package apidserver

import (
	"errors"
	"net/http"
	"sort"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func validateDeploySettingsCondition(req api.UpdateAppRequest, environment state.ProjectEnvironment) *api.Problem {
	if req.DeploySettingsCondition == nil {
		return nil
	}
	if environment.Slug != "" {
		return api.ErrValidation("deploy_settings_condition is only supported for single-app settings updates")
	}
	if err := req.DeploySettingsCondition.Validate(req); err != nil {
		return api.ErrValidation(err.Error())
	}
	return nil
}

func guardedDeploySettingsParams(req api.UpdateAppRequest, acct state.Account, params state.UpdateAppParams) state.UpdateAppParams {
	condition := req.DeploySettingsCondition
	if condition == nil {
		return params
	}
	params.DeploySettingsGuard = &state.DeploySettingsGuard{
		AppID: condition.ExpectedAppID, AccountID: acct.ID, DeploymentID: condition.ExpectedDeploymentID,
		Prepare: func(current state.App) (state.UpdateAppParams, error) {
			values := api.DeploySettingsSnapshot(api.AppResponse{Manifest: apiManifestFromState(current.Manifest),
				ScalingPolicy: statePolicyToDTO(current.ScalingPolicy), RetryPolicy: retryPolicyDTOFromJSON(current.RetryPolicyJSON)})
			var changed []string
			for field, expected := range condition.ExpectedSettings {
				if !api.DeploySettingEqual(field, values[field], expected) {
					changed = append(changed, field)
				}
			}
			if len(changed) > 0 {
				sort.Strings(changed)
				return params, &state.DeploySettingsConflictError{Reason: "settings_changed", Fields: changed}
			}
			copyOfReq := req
			if problem := validateUpdateApp(&copyOfReq, acct, api.MustLimitsFor(acct.Plan), current); problem != nil {
				return params, &api.APIError{Problem: *problem}
			}
			prepared := params
			prepared.Manifest, _ = stateManifestForUpdate(current, &copyOfReq)
			return prepared, nil
		},
	}
	return params
}

func deploySettingsUpdateProblem(err error) *api.Problem {
	var conflict *state.DeploySettingsConflictError
	if errors.As(err, &conflict) {
		problem := api.NewProblem(http.StatusConflict, api.CodeDeploySettingsConflict, "Deploy settings changed", conflict.Error())
		problem.Hint = "Saved deploy settings remain pending. Inspect the current app settings and deployment before continuing."
		for _, field := range conflict.Fields {
			problem.Errors = append(problem.Errors, api.FieldError{Field: field, Expected: "unchanged since the recovery read"})
		}
		return problem
	}
	var validation *api.APIError
	if errors.As(err, &validation) {
		return &validation.Problem
	}
	return nil
}
