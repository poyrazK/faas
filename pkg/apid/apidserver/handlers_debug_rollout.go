package apidserver

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/rolloutevidence"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// rolloutEvidence explains rollouts with request evidence (ADR-127
// amendment following ADR-1447), reusing the debugger's response shapes.
func (s *server) rolloutEvidence() rolloutevidence.Reader {
	r := rolloutevidence.Reader{Store: s.store, Regressions: s.activeRegressionItems, RequestItem: debugTelemetryRowToItem}
	if holds, ok := s.store.(state.RolloutDecisionStore); ok {
		r.Holds = holds
	}
	return r
}

func (s *server) activeRegressionItems(ctx context.Context, appID string, since time.Duration) ([]api.DebugRegressionItem, error) {
	rows, err := s.store.ListActiveRegressionsByApp(ctx, sqlc.ListActiveRegressionsByAppParams{
		AppID:   stringToPgUUID(appID),
		Column2: pgtype.Interval{Microseconds: int64(since / time.Microsecond), Valid: true},
	})
	if err != nil {
		return nil, err
	}
	items := make([]api.DebugRegressionItem, len(rows))
	for i, row := range rows {
		items[i] = debugRegressionRowToItem(row)
	}
	return items, nil
}

// debugRetention is the plan's debugger retention, the bound on every
// evidence window.
func debugRetention(limits api.Limits) time.Duration {
	return time.Duration(limits.DebugTelemetryRetentionDays) * 24 * time.Hour
}

// debugRolloutHandler — GET /v1/apps/{slug}/debug/rollout. Explains why the
// app's newest canary rollout (or ?deployment_id) was rolled back or is held.
func (s *server) debugRolloutHandler(w http.ResponseWriter, r *http.Request, acct state.Account) {
	app, ok := s.loadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return
	}
	limits := api.MustLimitsFor(acct.Plan)
	if !limits.DebugTelemetryEnabled {
		api.WriteProblem(w, api.ErrPlanFeatureGated("debugger", acct.Plan))
		return
	}
	deploymentID := strings.TrimSpace(r.URL.Query().Get("deployment_id"))
	if deploymentID != "" {
		id, err := uuid.Parse(deploymentID)
		if err != nil {
			api.WriteProblem(w, api.ErrValidation("deployment_id must be a UUID"))
			return
		}
		deploymentID = id.String()
	}
	out, err := s.rolloutEvidence().Explain(r.Context(), app, deploymentID, debugRetention(limits))
	if errors.Is(err, rolloutevidence.ErrDeploymentNotFound) {
		api.WriteProblem(w, api.NewProblem(http.StatusNotFound, api.CodeNotFound, "Deployment not found", "No such deployment for this app."))
		return
	}
	if err != nil {
		writeCustomerInternalProblem(w, r, s.log, "explain rollout",
			"Gregale could not read the evidence for this rollout.", "Retry in a moment; if it continues, contact support.", err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// debugStatusHandler — GET /v1/apps/{slug}/debug/status. One summary of the
// app's health, release, regressions, and recent failures over ?since
// (default 1h, clamped to the plan's debugger retention).
func (s *server) debugStatusHandler(w http.ResponseWriter, r *http.Request, acct state.Account) {
	app, ok := s.loadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return
	}
	limits := api.MustLimitsFor(acct.Plan)
	if !limits.DebugTelemetryEnabled {
		api.WriteProblem(w, api.ErrPlanFeatureGated("debugger", acct.Plan))
		return
	}
	sinceRaw := r.URL.Query().Get("since")
	since, err := parseDebugSinceStrict(sinceRaw, time.Hour)
	if err != nil {
		api.WriteProblem(w, api.ErrValidation(err.Error()))
		return
	}
	retention := debugRetention(limits)
	if retention > 0 && since > retention {
		since = retention
	}
	out, err := s.rolloutEvidence().Status(r.Context(), app, since, retention)
	if err != nil {
		writeCustomerInternalProblem(w, r, s.log, "debug status",
			"Gregale could not summarize this app's debugger evidence.", "Retry in a moment; if it continues, contact support.", err)
		return
	}
	out.Since = echoDebugSince(sinceRaw, since)
	writeJSON(w, http.StatusOK, out)
}
