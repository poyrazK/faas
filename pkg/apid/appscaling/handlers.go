// Package appscaling owns revision-guarded scaling and durable recovery receipts.
// adr: 20261104104521
package appscaling

import (
	"errors"
	"net/http"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/httpkit"
	"github.com/onebox-faas/faas/pkg/state"
)

type Deps struct {
	Store    state.AppScalingStore
	Resolve  func(http.ResponseWriter, *http.Request, state.Account, bool) (state.App, string, bool)
	Before   func(*http.Request, state.Account, state.App, api.UpdateAppRequest) *api.Problem
	Validate func(state.Account, state.App, api.UpdateAppRequest) *api.Problem
	After    func(*http.Request, state.Account, state.App, api.AppScalingReceipt)
}

type validationError struct{ problem *api.Problem }

func (e *validationError) Error() string { return "scaling validation failed" }

func Handler(d Deps) httpkit.AccountHandler {
	return func(w http.ResponseWriter, r *http.Request, account state.Account) {
		write := r.Method == http.MethodPost
		app, scope, ok := d.Resolve(w, r, account, write)
		if !ok {
			return
		}
		if d.Store == nil {
			api.WriteProblem(w, api.ErrCapacity("recoverable scaling is unavailable"))
			return
		}
		if !write {
			var out any
			var err error
			if id := r.PathValue("request_id"); id != "" {
				parsed, parseErr := uuid.Parse(id)
				if parseErr != nil || parsed == uuid.Nil {
					api.WriteProblem(w, api.ErrValidation("request_id must be a UUID"))
					return
				}
				out, err = d.Store.AppScalingReceipt(r.Context(), account.ID, app.ID, scope, parsed.String())
			} else {
				out, err = d.Store.AppScaling(r.Context(), account.ID, app.ID, scope)
			}
			if err != nil {
				writeError(w, err)
				return
			}
			httpkit.WriteJSON(w, http.StatusOK, out)
			return
		}
		var req api.ApplyAppScalingRequest
		if err := httpkit.DecodeJSON(r, &req); err != nil {
			api.WriteProblem(w, api.ErrValidation("invalid scaling request"))
			return
		}
		if p := req.Validate(); p != nil {
			api.WriteProblem(w, p)
			return
		}
		if (scope == "default") != (req.ExpectedEnvironmentID == "") {
			api.WriteProblem(w, api.ErrValidation("named scaling requests require expected_environment_id; omit it for the default app"))
			return
		}
		if d.Before != nil {
			if p := d.Before(r, account, app, req.UpdateAppRequest()); p != nil {
				api.WriteProblem(w, p)
				return
			}
		}
		out, err := d.Store.ApplyAppScaling(r.Context(), state.AppScalingChange{AccountID: account.ID, AppID: app.ID, Scope: scope, Request: req, Validate: func(current state.App) error {
			if p := validateSettings(account, current, req.UpdateAppRequest()); p != nil {
				return &validationError{p}
			}
			if p := d.Validate(account, current, req.UpdateAppRequest()); p != nil {
				return &validationError{p}
			}
			return nil
		}})
		if err != nil {
			writeError(w, err)
			return
		}
		if d.After != nil {
			d.After(r, account, app, out)
		}
		httpkit.WriteJSON(w, http.StatusOK, out)
	}
}

func writeError(w http.ResponseWriter, err error) {
	var validation *validationError
	switch {
	case errors.As(err, &validation):
		api.WriteProblem(w, validation.problem)
	case errors.Is(err, state.ErrScalingRevisionConflict):
		api.WriteProblem(w, api.NewProblem(http.StatusConflict, "scaling_revision_conflict", "Scaling settings changed", "review a new plan against the current scaling revision"))
	case errors.Is(err, state.ErrScalingRequestConflict):
		api.WriteProblem(w, api.NewProblem(http.StatusConflict, "scaling_request_conflict", "Scaling request ID reused", "retry the original payload or use a new request ID for a new plan"))
	case errors.Is(err, state.ErrNotFound):
		api.WriteProblem(w, api.NewProblem(http.StatusNotFound, api.CodeNotFound, "Scaling target not found", "the app, environment workload revision, or request receipt does not exist"))
	case errors.Is(err, state.ErrConflict):
		api.WriteProblem(w, api.NewProblem(http.StatusConflict, api.CodeConflict, "Scaling target changed", "reload the target and use its approved configuration workflow"))
	default:
		if problem := state.ServiceCapacityProblem(err); problem != nil {
			api.WriteProblem(w, problem)
			return
		}
		api.WriteProblem(w, api.ErrCapacity("could not commit scaling; recover or retry the original request ID"))
	}
}

func validateSettings(account state.Account, app state.App, req api.UpdateAppRequest) *api.Problem {
	limits := api.MustLimitsFor(account.Plan)
	ram, concurrency := app.RAMMB, app.MaxConcurrency
	if req.RAMMB != nil {
		ram = *req.RAMMB
		if ram <= 0 {
			return api.ErrInvalidAppRAM(ram)
		}
	}
	if req.MaxConcurrency != nil {
		concurrency = *req.MaxConcurrency
	}
	if req.CPUMillicores != nil {
		if p := api.ValidateAppCPUMillicores(*req.CPUMillicores); p != nil {
			return p
		}
	}
	return api.ValidateAppConfig(limits, ram, concurrency)
}
