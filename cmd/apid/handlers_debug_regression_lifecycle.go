package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

const (
	debugRegressionDefaultDismissal = 24 * time.Hour
	debugRegressionMaxDismissal     = 30 * 24 * time.Hour
)

// debugRegressionActionHandler applies debugger workflow state to one
// app-scoped observation. It deliberately has no deployment side effects:
// lifecycle state is triage metadata, not a traffic-control primitive.
func (s *server) debugRegressionActionHandler(w http.ResponseWriter, r *http.Request, acct state.Account) {
	app, ok := s.loadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return
	}
	if !api.MustLimitsFor(acct.Plan).DebugTelemetryEnabled {
		api.WriteProblem(w, api.ErrPlanFeatureGated("debugger", acct.Plan))
		return
	}

	var req api.DebugRegressionActionRequest
	dec := json.NewDecoder(io.LimitReader(r.Body, 32<<10))
	if err := dec.Decode(&req); err != nil {
		api.WriteProblem(w, api.ErrValidation("request body must be valid JSON"))
		return
	}
	req.Action = strings.ToLower(strings.TrimSpace(req.Action))
	req.Route = strings.TrimSpace(req.Route)
	if !api.AllowedDebugRegressionAction(req.Action) {
		api.WriteProblem(w, api.ErrValidation("action must be acknowledge, dismiss, resolve, or reopen"))
		return
	}
	if req.Route == "" || len(req.Route) > 256 {
		api.WriteProblem(w, api.ErrValidation("route must be between 1 and 256 characters"))
		return
	}
	deploymentID, err := uuid.Parse(strings.TrimSpace(req.DeploymentID))
	if err != nil {
		api.WriteProblem(w, api.ErrValidation("deployment_id must be a valid UUID"))
		return
	}

	stateName := map[string]string{
		"acknowledge": "acknowledged",
		"dismiss":     "dismissed",
		"resolve":     "resolved",
		"reopen":      "active",
	}[req.Action]
	dismissedUntil := pgtype.Timestamptz{}
	if req.Action == "dismiss" {
		dismissedUntil = pgtype.Timestamptz{Time: time.Now().UTC().Add(debugRegressionDefaultDismissal), Valid: true}
		if strings.TrimSpace(req.DismissedUntil) != "" {
			until, parseErr := time.Parse(time.RFC3339Nano, strings.TrimSpace(req.DismissedUntil))
			if parseErr != nil {
				api.WriteProblem(w, api.ErrValidation("dismissed_until must be an RFC3339 timestamp"))
				return
			}
			dismissedUntil = pgtype.Timestamptz{Time: until.UTC(), Valid: true}
		}
		now := time.Now().UTC()
		if !dismissedUntil.Time.After(now) || dismissedUntil.Time.After(now.Add(debugRegressionMaxDismissal)) {
			api.WriteProblem(w, api.ErrValidation("dismissed_until must be in the future and no more than 30 days away"))
			return
		}
	} else if strings.TrimSpace(req.DismissedUntil) != "" {
		api.WriteProblem(w, api.ErrValidation("dismissed_until is only valid for dismiss"))
		return
	}

	row, err := s.store.ApplyRegressionAction(r.Context(), sqlc.ApplyRegressionActionParams{
		AppID:        stringToPgUUID(app.ID),
		DeploymentID: pgtype.UUID{Bytes: deploymentID, Valid: true},
		Route:        req.Route,
		State:        stateName,
		Column5:      dismissedUntil,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		api.WriteProblem(w, api.NewProblem(http.StatusNotFound, api.CodeNotFound, "Not found", "no debugger regression matches the deployment and route"))
		return
	}
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("update debugger regression"))
		return
	}

	item := debugRegressionObservationToItem(row)
	s.audit.Emit(r.Context(), "debug.regression."+req.Action, &acct.ID, map[string]any{
		"app_id":        app.ID,
		"deployment_id": item.DeploymentID,
		"route":         item.Route,
		"state":         item.State,
	})
	s.notifyDebugRegressionChanged(r.Context(), app.ID, item)
	writeJSON(w, http.StatusOK, api.DebugRegressionActionResponse{Regression: item})
}

// debugRegressionObservationToItem maps the generated full observation row
// returned by lifecycle writes to the same safe wire shape used by GET.
func debugRegressionObservationToItem(row sqlc.DebugRegressionObservation) api.DebugRegressionItem {
	item := api.DebugRegressionItem{
		DeploymentID:    uuidFromPg(row.DeploymentID),
		Route:           row.Route,
		P95MS:           int(row.P95Ms),
		P95BaseMS:       int(row.P95BaseMs),
		AffectedCount:   int(row.AffectedCount),
		FirstDetectedAt: timeFromPg(row.FirstDetectedAt),
		LastDetectedAt:  timeFromPg(row.LastDetectedAt),
		State:           row.State,
		AcknowledgedAt:  timeFromPg(row.AcknowledgedAt),
		DismissedUntil:  timeFromPg(row.DismissedUntil),
		ResolvedAt:      timeFromPg(row.ResolvedAt),
	}
	if row.RegressionFactor.Valid {
		if f, err := row.RegressionFactor.Float64Value(); err == nil {
			item.Factor = formatFloat2(f.Float64)
		}
	}
	return item
}

// notifyDebugRegressionChanged is the single event encoder for detector and
// operator transitions. The payload stays metadata-only and includes app_id
// so /v1/events can enforce tenant ownership before delivery.
func (s *server) notifyDebugRegressionChanged(ctx context.Context, appID string, item api.DebugRegressionItem) {
	if s == nil || s.notif == nil {
		return
	}
	payload, err := json.Marshal(map[string]any{
		"app_id":            appID,
		"deployment_id":     item.DeploymentID,
		"route":             item.Route,
		"p95_ms":            item.P95MS,
		"p95_base_ms":       item.P95BaseMS,
		"affected_count":    item.AffectedCount,
		"regression_factor": item.Factor,
		"state":             item.State,
		"first_detected_at": item.FirstDetectedAt,
		"last_detected_at":  item.LastDetectedAt,
		"acknowledged_at":   item.AcknowledgedAt,
		"dismissed_until":   item.DismissedUntil,
		"resolved_at":       item.ResolvedAt,
	})
	if err != nil {
		return
	}
	if err := s.notif.Notify(ctx, db.NotifyDebugRegressionChanged, string(payload)); err != nil && s.log != nil {
		s.log.Warn("debug regression notification failed", "app_id", appID, "err", err)
	}
}
