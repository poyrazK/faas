package main

import (
	"encoding/json"
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/workpolicy"
)

func validateWorkPolicies(schedule *workpolicy.SchedulePolicy, failure *workpolicy.FailureRules) *api.Problem {
	invalid := func(err error) *api.Problem {
		return api.NewProblem(http.StatusBadRequest, api.CodeValidation, "Invalid work policy", err.Error())
	}
	if schedule != nil {
		if err := schedule.Validate(); err != nil {
			return invalid(err)
		}
		if schedule.StartDeadlineSeconds > api.WorkPolicyMaxStartDeadlineSeconds {
			return api.NewProblem(http.StatusBadRequest, api.CodeValidation, "Invalid start deadline", "start_deadline_seconds must be at most 2592000")
		}
	}
	if failure != nil {
		if err := failure.Validate(); err != nil {
			return invalid(err)
		}
		encoded, _ := json.Marshal(failure)
		if len(encoded) > api.WorkPolicyMaxBytes || len(failure.Rules) > api.WorkPolicyMaxRules {
			return api.NewProblem(http.StatusBadRequest, api.CodeValidation, "Failure policy too large", "failure_rules may contain at most 64 rules and 16384 bytes")
		}
	}
	return nil
}
