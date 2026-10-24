package outboundai

import (
	"errors"
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/httpkit"
	"github.com/onebox-faas/faas/pkg/state"
)

const feedbackInvalidDetail = `feedback is {"rating": "good"|"bad", "label": optional}; a label is up to 32 lowercase letters, ` +
	"digits, _ . or - starting with a letter"

func (s *Service) qualityStore(w http.ResponseWriter) (state.OutboundAIQualityStore, bool) {
	store, ok := s.Host.Store().(state.OutboundAIQualityStore)
	if !ok {
		problem(w, http.StatusServiceUnavailable, "outbound_ai_unavailable", "Outbound AI quality signals are unavailable")
	}
	return store, ok
}

// ListQualityAlerts lists an integration's drift alerts, newest first
// (ADR 20261021171712).
func (s *Service) ListQualityAlerts(w http.ResponseWriter, r *http.Request, acct state.Account) {
	integrationID, ok := integrationPathID(w, r)
	if !ok {
		return
	}
	limit, valid := queryLimit(r, api.OutboundAIQualityAlertsPageMax)
	if !valid {
		problem(w, http.StatusBadRequest, api.CodeValidation, "limit must be 1 to 100")
		return
	}
	store, ok := s.qualityStore(w)
	if !ok {
		return
	}
	alerts, err := store.ListOutboundAIQualityAlerts(r.Context(), acct.ID, integrationID, limit)
	switch {
	case errors.Is(err, state.ErrNotFound):
		httpkit.NotFound(w, "outbound integration not found")
	case err != nil:
		problem(w, http.StatusServiceUnavailable, "outbound_ai_unavailable", "Outbound AI quality signals are unavailable")
	default:
		out := api.OutboundAIQualityAlertList{Items: make([]api.OutboundAIQualityAlert, 0, len(alerts))}
		for _, alert := range alerts {
			out.Items = append(out.Items, api.OutboundAIQualityAlert{
				ID: alert.ID, IntegrationID: alert.IntegrationID, UsageDate: alert.UsageDate, Model: alert.Model, Metric: alert.Metric,
				CurrentValue: alert.CurrentValue, BaselineValue: alert.BaselineValue, CurrentCount: alert.CurrentCount,
				BaselineCount: alert.BaselineCount, DetectedAt: alert.DetectedAt,
			})
		}
		w.Header().Set("Cache-Control", "no-store")
		httpkit.WriteJSON(w, http.StatusOK, out)
	}
}

// PutRequestFeedback rates one logged call on a person's behalf
// (ADR 20261021171712).
func (s *Service) PutRequestFeedback(w http.ResponseWriter, r *http.Request, acct state.Account) {
	integrationID, ok := integrationPathID(w, r)
	if !ok {
		return
	}
	var req api.PutOutboundAIFeedbackRequest
	if err := httpkit.DecodeJSONSized(r, &req, 1<<10); err != nil || !api.ValidOutboundAIFeedback(req.Rating, req.Label) {
		problem(w, http.StatusBadRequest, api.CodeValidation, feedbackInvalidDetail)
		return
	}
	store, ok := s.qualityStore(w)
	if !ok {
		return
	}
	switch err := store.SetOutboundAIRequestFeedback(r.Context(), acct.ID, integrationID, r.PathValue("request"), req.Rating, req.Label); {
	case errors.Is(err, state.ErrNotFound):
		httpkit.NotFound(w, "logged AI call not found")
	case errors.Is(err, state.ErrInvalidArgument):
		problem(w, http.StatusBadRequest, api.CodeValidation, feedbackInvalidDetail)
	case err != nil:
		problem(w, http.StatusServiceUnavailable, "outbound_ai_unavailable", "Outbound AI quality signals are unavailable")
	default:
		w.WriteHeader(http.StatusNoContent)
	}
}

// qualityFields copies a log entry's quality flags, rating and evaluation
// score (ADR 20261021171712).
func qualityFields(item *api.OutboundAIRequestLogEntry, entry state.OutboundAIRequestLogEntry) {
	if len(entry.QualityFlags) > 0 {
		item.QualityFlags = append([]string{}, entry.QualityFlags...)
	}
	if entry.Feedback != "" {
		feedback := entry.Feedback
		item.Feedback = &feedback
	}
	if entry.FeedbackLabel != "" {
		label := entry.FeedbackLabel
		item.FeedbackLabel = &label
	}
	if entry.EvaluationScore != nil {
		score := *entry.EvaluationScore
		item.EvaluationScore = &score
	}
}
