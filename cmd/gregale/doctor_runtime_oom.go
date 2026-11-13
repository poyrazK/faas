package main

import (
	"context"
	"fmt"

	"github.com/onebox-faas/faas/pkg/api"
)

// doctorRuntimeOOMForApp is the runtime-oom check for a deployed app: the
// out-of-memory kills its operational summary reports for the last 24 hours.
// The check was always "skipped", so an OOM-killed app passed doctor while
// its callers saw timeouts (2026-11-12 agent feedback). Standalone doctor
// only: the deploy preflight stays local.
func doctorRuntimeOOMForApp(parent context.Context, app string) doctorCheck {
	if app == "" {
		return doctorCheckRuntimeOOM()
	}
	token := loadToken()
	if token == "" {
		return doctorCheck{Name: "runtime-oom", Status: "skipped", Reason: "not logged in"}
	}
	ctx, cancel := context.WithTimeout(parent, doctorAppEnvTimeout)
	defer cancel()
	summary, err := NewClient(apiBase(), token).GetAppOperationalSummary(ctx, app)
	if err != nil {
		return doctorCheck{Name: "runtime-oom", Status: "skipped",
			Reason: fmt.Sprintf("could not read %s's operational summary: %v", app, errorTextRedactor()(err.Error()))}
	}
	return doctorRuntimeOOMFromSummary(app, summary)
}

func doctorRuntimeOOMFromSummary(app string, summary api.AppOperationalSummary) doctorCheck {
	for _, recommendation := range summary.Recommendations {
		if recommendation.Code != api.CodeAppRuntimeOOM {
			continue
		}
		return doctorCheck{
			Name:   "runtime-oom",
			Status: "warn",
			Code:   api.CodeAppRuntimeOOM,
			Hint:   app + " was killed for running out of memory",
			Why:    recommendation.Message,
			Fix:    recommendation.Next,
		}
	}
	return doctorCheck{Name: "runtime-oom", Status: "ok", Reason: "no out-of-memory kills reported for " + app + " in the last 24 hours"}
}

// replaceDoctorCheck swaps the named check in place, keeping report order.
func replaceDoctorCheck(rep *doctorReport, check doctorCheck) {
	for i := range rep.Checks {
		if rep.Checks[i].Name == check.Name {
			rep.Checks[i] = check
			return
		}
	}
	rep.Checks = append(rep.Checks, check)
}
