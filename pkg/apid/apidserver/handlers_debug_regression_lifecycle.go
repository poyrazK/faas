package apidserver

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/regressiontriage"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/debugger"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// debugRegressionActionHandler applies debugger workflow state to one
// app-scoped observation. It deliberately has no deployment side effects:
// lifecycle state is triage metadata, not a traffic-control primitive.
func (s *server) debugRegressionActionHandler(w http.ResponseWriter, r *http.Request, acct state.Account) {
	s.debugRegressionAction(w, r, acct, false)
}

func (s *server) debugRegressionGuardedActionHandler(w http.ResponseWriter, r *http.Request, acct state.Account) {
	s.debugRegressionAction(w, r, acct, true)
}

func (s *server) debugRegressionAction(w http.ResponseWriter, r *http.Request, acct state.Account, guarded bool) {
	app, ok := s.loadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return
	}
	if !api.MustLimitsFor(acct.Plan).DebugTelemetryEnabled {
		api.WriteProblem(w, api.ErrPlanFeatureGated("debugger", acct.Plan))
		return
	}

	var req api.GuardedDebugRegressionActionRequest
	dec := json.NewDecoder(io.LimitReader(r.Body, 32<<10))
	if err := dec.Decode(&req); err != nil {
		api.WriteProblem(w, api.ErrValidation("request body must be valid JSON"))
		return
	}
	prepare := regressiontriage.Prepare
	if guarded {
		prepare = regressiontriage.PrepareChecked
	}
	params, problem := prepare(req, app.ID, time.Now().UTC())
	if problem != nil {
		api.WriteProblem(w, problem)
		return
	}
	if guarded && !params.ExpectedVersion.Valid {
		api.WriteProblem(w, api.ErrValidation("checked actions require expected_app_id, expected_version and reason"))
		return
	}
	if guarded {
		s.applyCheckedRegressionAction(w, r, acct, app.ID, params, req)
		return
	}
	row, err := s.store.ApplyRegressionAction(r.Context(), params)
	if errors.Is(err, pgx.ErrNoRows) {
		if params.ExpectedVersion.Valid {
			api.WriteProblem(w, api.NewProblem(409, api.CodeRegressionChanged, "Regression changed", "observation changed or disappeared; review again"))
			return
		}
		api.WriteProblem(w, api.NewProblem(http.StatusNotFound, api.CodeNotFound, "Not found", "no debugger regression matches the deployment and route"))
		return
	}
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("update debugger regression"))
		return
	}

	s.writeRegressionAction(w, r, acct, app.ID, row, req)
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
	item.SuspectedDependency = parseSuspectedDependency(row.SuspectedDependency)
	applyDebugRegressionSignal(&item, row.Kind, row.ErrorRatePct, row.ErrorRateBasePct, row.ErrorCount)
	return item
}

// applyDebugRegressionSignal copies the regression kind and error-rate
// signal onto the wire item. Rows written before error-rate detection read
// as latency observations.
func applyDebugRegressionSignal(item *api.DebugRegressionItem, kind string, rate, base pgtype.Numeric, count int32) {
	item.Kind = kind
	if item.Kind == "" {
		item.Kind = debugger.RegressionKindLatency
	}
	item.ErrorRatePct = numericFloat(rate)
	item.ErrorRateBasePct = numericFloat(base)
	item.ErrorCount = int(count)
}

func numericFloat(value pgtype.Numeric) float64 {
	if !value.Valid {
		return 0
	}
	f, err := value.Float64Value()
	if err != nil || !f.Valid {
		return 0
	}
	return f.Float64
}

// notifyDebugRegressionChanged is the single event encoder for detector and
// operator transitions. The payload stays metadata-only and includes app_id
// so /v1/events can enforce tenant ownership before delivery.
func (s *server) notifyDebugRegressionChanged(ctx context.Context, appID string, item api.DebugRegressionItem) {
	if s == nil || s.notif == nil {
		return
	}
	payload, err := json.Marshal(map[string]any{
		"app_id":               appID,
		"deployment_id":        item.DeploymentID,
		"route":                item.Route,
		"kind":                 item.Kind,
		"error_rate_pct":       item.ErrorRatePct,
		"error_rate_base_pct":  item.ErrorRateBasePct,
		"error_count":          item.ErrorCount,
		"p95_ms":               item.P95MS,
		"p95_base_ms":          item.P95BaseMS,
		"affected_count":       item.AffectedCount,
		"regression_factor":    item.Factor,
		"state":                item.State,
		"first_detected_at":    item.FirstDetectedAt,
		"last_detected_at":     item.LastDetectedAt,
		"acknowledged_at":      item.AcknowledgedAt,
		"dismissed_until":      item.DismissedUntil,
		"resolved_at":          item.ResolvedAt,
		"suspected_dependency": item.SuspectedDependency,
	})
	if err != nil {
		return
	}
	if err := s.notif.Notify(ctx, db.NotifyDebugRegressionChanged, string(payload)); err != nil && s.log != nil {
		s.log.Warn("debug regression notification failed", "app_id", appID, "err", err)
	}
}

func (s *server) writeRegressionAction(w http.ResponseWriter, r *http.Request, acct state.Account, appID string, row sqlc.DebugRegressionObservation, req api.GuardedDebugRegressionActionRequest) {
	item := debugRegressionObservationToItem(row)
	s.emitRegressionAction(r, acct, appID, item, req)
	review := debugRegressionReview(appID, row)
	writeJSON(w, http.StatusOK, api.DebugRegressionActionResponse{Regression: item, Review: &review})
}

func (s *server) emitRegressionAction(r *http.Request, acct state.Account, appID string, item api.DebugRegressionItem, req api.GuardedDebugRegressionActionRequest) {
	s.audit.Emit(r.Context(), "debug.regression."+strings.ToLower(strings.TrimSpace(req.Action)), &acct.ID, map[string]any{
		"app_id":           appID,
		"deployment_id":    item.DeploymentID,
		"route":            item.Route,
		"state":            item.State,
		"reason":           req.Reason,
		"expected_version": req.ExpectedVersion,
	})
	s.notifyDebugRegressionChanged(r.Context(), appID, item)
}
