package outboundai

import (
	"errors"
	"net/http"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/httpkit"
	"github.com/onebox-faas/faas/pkg/state"
)

const evaluationInvalidDetail = "an evaluation needs a target from 2 to the route's last target, a judge_integration_id " +
	"naming one of the account's enabled AI integrations, a judge_model, a sample_size from 1 to 50, a max_loss_rate " +
	"from 0 to 100, and a canary, if any, for the same target"

func (s *Service) evaluationStore(w http.ResponseWriter) (state.OutboundAIEvaluationStore, bool) {
	store, ok := s.Host.Store().(state.OutboundAIEvaluationStore)
	if !ok {
		problem(w, http.StatusServiceUnavailable, "outbound_ai_unavailable", "Outbound AI evaluations are unavailable")
	}
	return store, ok
}

// CreateEvaluation records an evaluation of a route target against the
// route's first target; outboundd runs it (ADR-1431).
func (s *Service) CreateEvaluation(w http.ResponseWriter, r *http.Request, acct state.Account) {
	routeID, ok := routePathID(w, r)
	if !ok {
		return
	}
	var req api.CreateOutboundAIEvaluationRequest
	if err := httpkit.DecodeJSONSized(r, &req, 4<<10); err != nil || !req.Valid() {
		problem(w, http.StatusBadRequest, api.CodeValidation, evaluationInvalidDetail)
		return
	}
	req = req.Normalized()
	if _, err := uuid.Parse(req.JudgeIntegrationID); err != nil {
		problem(w, http.StatusBadRequest, api.CodeValidation, evaluationInvalidDetail)
		return
	}
	store, ok := s.evaluationStore(w)
	if !ok {
		return
	}
	created, err := store.CreateOutboundAIEvaluation(r.Context(), state.OutboundAIEvaluation{
		ID: uuid.NewString(), AccountID: acct.ID, RouteID: routeID, Position: req.Target,
		JudgeIntegrationID: req.JudgeIntegrationID, JudgeModel: req.JudgeModel, SampleSize: *req.SampleSize,
		MaxLossRate: *req.MaxLossRate, Canary: req.Canary,
	})
	switch {
	case errors.Is(err, state.ErrNotFound):
		httpkit.NotFound(w, "AI route not found")
	case errors.Is(err, state.ErrInvalidArgument):
		problem(w, http.StatusBadRequest, api.CodeValidation, evaluationInvalidDetail)
	case errors.Is(err, state.ErrOutboundAIEvaluationLimit):
		api.WriteProblem(w, api.ErrOutboundAIEvaluationLimit(api.OutboundAIEvaluationsPerDay))
	case err != nil:
		problem(w, http.StatusServiceUnavailable, "outbound_ai_unavailable", "The AI evaluation could not be recorded")
	default:
		w.Header().Set("Cache-Control", "no-store")
		httpkit.WriteJSON(w, http.StatusAccepted, evaluationResponse(created))
	}
}

// ListEvaluations lists a route's latest evaluations without their items.
func (s *Service) ListEvaluations(w http.ResponseWriter, r *http.Request, acct state.Account) {
	routeID, ok := routePathID(w, r)
	if !ok {
		return
	}
	store, ok := s.evaluationStore(w)
	if !ok {
		return
	}
	evaluations, err := store.ListOutboundAIEvaluations(r.Context(), acct.ID, routeID)
	if err != nil {
		problem(w, http.StatusServiceUnavailable, "outbound_ai_unavailable", "AI evaluations are unavailable")
		return
	}
	out := api.OutboundAIEvaluationList{Items: make([]api.OutboundAIEvaluation, 0, len(evaluations))}
	for _, evaluation := range evaluations {
		out.Items = append(out.Items, evaluationResponse(evaluation))
	}
	w.Header().Set("Cache-Control", "no-store")
	httpkit.WriteJSON(w, http.StatusOK, out)
}

// GetEvaluation reads an evaluation with its items.
func (s *Service) GetEvaluation(w http.ResponseWriter, r *http.Request, acct state.Account) {
	evaluationID := r.PathValue("evaluation")
	if _, err := uuid.Parse(evaluationID); err != nil {
		problem(w, http.StatusBadRequest, api.CodeValidation, "Evaluation ID must be a UUID")
		return
	}
	store, ok := s.evaluationStore(w)
	if !ok {
		return
	}
	evaluation, err := store.GetOutboundAIEvaluation(r.Context(), acct.ID, evaluationID)
	switch {
	case errors.Is(err, state.ErrNotFound):
		httpkit.NotFound(w, "AI evaluation not found")
	case err != nil:
		problem(w, http.StatusServiceUnavailable, "outbound_ai_unavailable", "The AI evaluation is unavailable")
	default:
		w.Header().Set("Cache-Control", "no-store")
		httpkit.WriteJSON(w, http.StatusOK, evaluationResponse(evaluation))
	}
}

func evaluationResponse(e state.OutboundAIEvaluation) api.OutboundAIEvaluation {
	out := api.OutboundAIEvaluation{
		ID: e.ID, RouteID: e.RouteID, Target: e.Position, JudgeIntegrationID: e.JudgeIntegrationID, JudgeModel: e.JudgeModel,
		SampleSize: e.SampleSize, MaxLossRate: e.MaxLossRate, State: e.State, Verdict: optionalString(e.Verdict),
		ErrorCode: optionalString(e.ErrorCode), Samples: e.Samples, Wins: e.Wins, Ties: e.Ties, Losses: e.Losses,
		CandidateErrors: e.CandidateErrors, JudgeErrors: e.JudgeErrors,
		Baseline:      api.OutboundAIEvaluationTargetStats{CostMillicents: e.BaselineCostMillicents, MeanTTFBMS: meanMS(e.BaselineTTFBMSTotal, e.BaselineTTFBCalls)},
		Candidate:     api.OutboundAIEvaluationTargetStats{CostMillicents: e.CandidateCostMillicents, MeanTTFBMS: meanMS(e.CandidateTTFBMSTotal, e.CandidateTTFBCalls)},
		CanaryStarted: e.CanaryStarted, CreatedAt: e.CreatedAt, StartedAt: e.StartedAt, FinishedAt: e.FinishedAt,
	}
	if e.Canary != nil {
		canary := *e.Canary
		out.Canary = &canary
	}
	for _, item := range e.Items {
		out.Items = append(out.Items, api.OutboundAIEvaluationItem{
			LogID: item.LogID, Verdict: item.Verdict, CandidateStatus: copyInt(item.CandidateStatus),
			CandidateCostMillicents: item.CandidateCostMillicents, CandidateTTFBMS: copyInt64(item.CandidateTTFBMS),
			BaselineCostMillicents: item.BaselineCostMillicents, BaselineTTFBMS: copyInt64(item.BaselineTTFBMS),
			BaselineOutputText: item.BaselineOutputText, CandidateOutputText: item.CandidateOutputText,
		})
	}
	return out
}

func meanMS(total int64, calls int) *int64 {
	if calls <= 0 {
		return nil
	}
	mean := total / int64(calls)
	return &mean
}

func copyInt(value *int) *int {
	if value == nil {
		return nil
	}
	out := *value
	return &out
}
