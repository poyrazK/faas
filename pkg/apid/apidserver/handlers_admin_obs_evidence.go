package apidserver

import (
	"errors"
	"net/http"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/operatorobs"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) obsIncidentEvidence(w http.ResponseWriter, r *http.Request, caller state.Account) {
	if allowed, prob := s.adminAllows(caller); !allowed {
		api.WriteProblem(w, prob)
		return
	}
	kind, id := r.PathValue("kind"), r.PathValue("id")
	if err := operatorobs.ValidateReference(kind, id); err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation,
			"Invalid incident reference", "expected deployment, job_run, compute_node UUID or platform_alert SHA-256"))
		return
	}
	prob, limit := api.ParseLimit(r.URL.Query().Get("limit"), api.ObsIncidentEvidenceLimitDefault, api.ObsIncidentEvidenceLimitMax, "incident evidence")
	if prob != nil {
		api.WriteProblem(w, prob)
		return
	}
	response, err := operatorobs.Evidence(r.Context(), s.store, s.promqlClient, kind, id, limit, time.Now().UTC())
	if err != nil {
		if errors.Is(err, state.ErrNotFound) {
			api.WriteProblem(w, api.NewProblem(http.StatusNotFound, api.CodeNotFound,
				"Incident resource not found", "resource is missing or deleted, or alert evidence is not retained"))
		} else {
			api.WriteProblem(w, api.ErrCapacity("could not load incident evidence"))
		}
		return
	}
	if kind == "platform_alert" {
		suppression := operatorobs.AlertSuppression(r.Context(), s.promqlClient, s.alertmanagerClient, response.Incident, time.Now().UTC())
		response.Suppression = &suppression
		if suppression.State != "active" && suppression.State != "suppressed" && suppression.State != "not_applicable" {
			response.UnavailableSources = append(response.UnavailableSources, "alertmanager_suppression")
		}
	}
	writeJSON(w, http.StatusOK, response)
}
