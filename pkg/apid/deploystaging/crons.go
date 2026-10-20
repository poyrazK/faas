// Package deploystaging owns HTTP cron staging for saved single-app deploys.
package deploystaging

import (
	"context"
	"errors"
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/httpkit"
	"github.com/onebox-faas/faas/pkg/state"
)

type CronHandlers struct {
	Store   state.DeploymentCronStagingStore
	LoadApp func(http.ResponseWriter, *http.Request, state.Account, string) (state.App, bool)
	Changed func(context.Context, string, string, string)
}

func (h CronHandlers) Snapshot(w http.ResponseWriter, r *http.Request, acct state.Account) {
	app, ok := h.LoadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return
	}
	if h.Store == nil {
		api.WriteProblem(w, api.ErrCapacity("deployment cron staging is unavailable"))
		return
	}
	snapshot, err := h.Store.DeploymentCronStagingSnapshot(r.Context(), acct.ID, app.ID)
	if err != nil {
		h.writeError(w, err, acct.Plan)
		return
	}
	httpkit.WriteJSON(w, http.StatusOK, snapshot)
}
func (h CronHandlers) Apply(w http.ResponseWriter, r *http.Request, acct state.Account) {
	app, ok := h.LoadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return
	}
	var request api.DeploymentCronStagingRequest
	if err := httpkit.DecodeJSON(r, &request); err != nil {
		api.WriteProblem(w, api.ErrValidation(err.Error()))
		return
	}
	if err := request.Validate(); err != nil {
		api.WriteProblem(w, api.ErrValidation(err.Error()))
		return
	}
	if h.Store == nil {
		api.WriteProblem(w, api.ErrCapacity("deployment cron staging is unavailable"))
		return
	}
	limits, known := api.LimitsFor(acct.Plan)
	target := request.Desired
	if request.Operation == "restore" {
		target = request.Before
	}
	if (!known || limits.CronLimitPerApp == 0) && len(target) > 0 {
		api.WriteProblem(w, api.ErrPlanCronsNotAllowed(acct.Plan))
		return
	}
	result, err := h.Store.ApplyDeploymentCronStaging(r.Context(), app.ID, state.DeploymentCronStagingParams{AccountID: acct.ID, Request: request, Limits: limits})
	if err != nil {
		h.writeError(w, err, acct.Plan)
		return
	}
	if result.Changed && h.Changed != nil {
		h.Changed(r.Context(), acct.ID, app.ID, request.Operation)
	}
	httpkit.WriteJSON(w, http.StatusOK, result.Receipt)
}
func (h CronHandlers) writeError(w http.ResponseWriter, err error, plan api.Plan) {
	var quota *state.CronQuotaError
	switch {
	case errors.Is(err, state.ErrDeploymentCronConflict):
		problem := api.NewProblem(http.StatusConflict, api.CodeDeploymentCronConflict, "Deployment cron staging changed", "app, deployment, HTTP cron set or ownership changed; recovery remains pending")
		problem.Hint = "Inspect the current cron set and saved deploy; no schedules were overwritten."
		api.WriteProblem(w, problem)
	case errors.As(err, &quota):
		api.WriteProblem(w, api.ErrPlanCronQuota(plan, string(quota.Scope), quota.Limit, quota.Observed))
	case errors.Is(err, state.ErrNotFound):
		api.WriteProblem(w, api.NewProblem(http.StatusNotFound, api.CodeNotFound, "Not found", "no such app"))
	case errors.Is(err, state.ErrInvalidArgument):
		api.WriteProblem(w, api.ErrValidation("invalid deployment cron staging request"))
	default:
		api.WriteProblem(w, api.ErrCapacity("could not apply deployment cron staging"))
	}
}
