package main

import (
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/bindingcheck"
	"time"
)

func bindingPolicyVerificationPlan(i api.AppBindingInventory, p api.BindingReleasePolicy, now time.Time) ([]bindingPolicyProbePlan, []bindingcheck.Finding, *bindingcheck.Report, error) {
	return bindingcheck.VerificationPlan(i, p, now)
}
