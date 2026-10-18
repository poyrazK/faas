package apidserver

import (
	"errors"
	"net/http"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/regressiontriage"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func (s *server) debugRegressionReviewHandler(w http.ResponseWriter, r *http.Request, acct state.Account) {
	app, ok := s.loadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return
	}
	if !api.MustLimitsFor(acct.Plan).DebugTelemetryEnabled {
		api.WriteProblem(w, api.ErrPlanFeatureGated("debugger", acct.Plan))
		return
	}
	route := r.URL.Query().Get("route")
	id, problem := regressiontriage.Selection(r.URL.Query().Get("deployment_id"), route)
	if problem != nil {
		api.WriteProblem(w, problem)
		return
	}
	row, err := s.store.GetRegressionObservation(r.Context(), sqlc.GetRegressionObservationParams{AppID: stringToPgUUID(app.ID), DeploymentID: id, Route: route})
	if errors.Is(err, pgx.ErrNoRows) {
		api.WriteProblem(w, api.NewProblem(404, api.CodeNotFound, "Not found", "no debugger regression matches the deployment and route"))
		return
	}
	if err != nil {
		api.WriteProblem(w, api.ErrDebugRegressionUnavailable("retained regression review is unavailable"))
		return
	}
	writeJSON(w, http.StatusOK, debugRegressionReview(app.ID, row))
}

func debugRegressionReview(appID string, row sqlc.DebugRegressionObservation) api.DebugRegressionReview {
	return api.DebugRegressionReview{AppID: appID, DeploymentID: uuidFromPg(row.DeploymentID), Route: row.Route, Version: uuidFromPg(row.ReviewVersion), State: row.State,
		FirstDetectedAt: timeFromPg(row.FirstDetectedAt), LastDetectedAt: timeFromPg(row.LastDetectedAt), AcknowledgedAt: timeFromPg(row.AcknowledgedAt), DismissedUntil: timeFromPg(row.DismissedUntil), ResolvedAt: timeFromPg(row.ResolvedAt)}
}
