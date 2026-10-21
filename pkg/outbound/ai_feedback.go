package outbound

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
)

// Feedback (ADR 20261021171712): an app rates one of its logged calls by
// the ID X-Gregale-AI-Call returned, through the gateway it called.

// AIFeedbackPath is the gateway path, under an integration, that takes an
// app's feedback.
const AIFeedbackPath = "/_gregale/feedback"

const aiFeedbackBodyMaxBytes = 4 << 10

// ErrAICallNotFound reports feedback on a call that is not in the
// integration's request log or was made by another caller.
var ErrAICallNotFound = errors.New("outbound: AI call not found")

// AIFeedback is one rating of a logged call. Caller is the app that made
// the call; Label is empty or a short key.
type AIFeedback struct {
	IntegrationID string
	Caller        string
	CallID        string
	Rating        string
	Label         string
}

// AIFeedbackRecorder stores ratings: a later rating of a call replaces the
// earlier one, and the call's daily rollup row counts the change.
type AIFeedbackRecorder interface {
	RecordAIFeedback(ctx context.Context, feedback AIFeedback) error
}

// validateAIFeedback checks a rating before it is stored.
func validateAIFeedback(feedback AIFeedback) error {
	if !validUUID(feedback.IntegrationID) || !validUUID(feedback.CallID) || !appCaller(feedback.Caller) || !validUUID(feedback.Caller) ||
		!api.ValidOutboundAIFeedback(feedback.Rating, feedback.Label) {
		return ErrInvalidIntegration
	}
	return nil
}

// serveAIFeedback takes an app's rating of one of its calls.
func (h *Handler) serveAIFeedback(w http.ResponseWriter, r *http.Request, integration Integration, appID string) {
	if r.Method != http.MethodPost {
		writeProblem(w, http.StatusMethodNotAllowed, "outbound_method_not_allowed", "Feedback takes POST", "")
		return
	}
	if appID == "" {
		writeProblem(w, http.StatusForbidden, "outbound_ai_feedback_not_authorized", "Only an app can rate its AI calls", "")
		return
	}
	var body struct {
		CallID string `json:"call_id"`
		Rating string `json:"rating"`
		Label  string `json:"label"`
	}
	raw, err := io.ReadAll(io.LimitReader(r.Body, aiFeedbackBodyMaxBytes+1))
	feedback := AIFeedback{IntegrationID: integration.ID, Caller: appID}
	if err == nil && len(raw) <= aiFeedbackBodyMaxBytes && json.Unmarshal(raw, &body) == nil {
		feedback.CallID, feedback.Rating, feedback.Label = body.CallID, body.Rating, body.Label
	}
	if validateAIFeedback(feedback) != nil {
		writeProblem(w, http.StatusBadRequest, "outbound_ai_feedback_invalid",
			`Feedback is {"call_id": "<X-Gregale-AI-Call>", "rating": "good"|"bad", "label": optional}; a label is up to 32 `+
				"lowercase letters, digits, _ . or - starting with a letter", "")
		return
	}
	recorder, ok := h.Backend.(AIFeedbackRecorder)
	if !ok {
		writeProblem(w, http.StatusServiceUnavailable, "outbound_ai_feedback_unavailable", "AI feedback is unavailable", "1")
		return
	}
	switch err := recorder.RecordAIFeedback(r.Context(), feedback); {
	case errors.Is(err, ErrAICallNotFound):
		writeProblem(w, http.StatusNotFound, "outbound_ai_call_not_found",
			"No logged call of this app on this integration has that ID; feedback needs the integration's request log", "")
	case err != nil:
		writeProblem(w, http.StatusServiceUnavailable, "outbound_ai_feedback_unavailable", "AI feedback is unavailable", "1")
	default:
		h.Metrics.ObserveAIFeedback(integration.MetricLabel(), feedback.Rating)
		w.WriteHeader(http.StatusNoContent)
	}
}
