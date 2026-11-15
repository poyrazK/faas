package outboundai

import (
	"net/http"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/httpkit"
	"github.com/onebox-faas/faas/pkg/state"
)

// Prompt canaries, stats and evaluations (ADR 20261114180740).

const promptCanaryInvalidDetail = "a prompt canary needs an existing version that is not live, a percent from 1 to 50, " +
	"max_error_rate_increase from 0 to 100, max_latency_increase_percent from 1 to 1000 or null, " +
	"min_calls from 10 to 100,000 and window_minutes from 5 to 1440"

const promptEvaluationInvalidDetail = "the candidate version must exist and not be live, the prompt must have a live version, " +
	"and the judge must be one of this account's enabled managed AI integrations an openai-format route accepts"

func promptCanaryResponse(canary state.OutboundAIPromptCanary) api.OutboundAIPromptCanary {
	return api.OutboundAIPromptCanary{Version: canary.Version, Percent: canary.Percent,
		MaxErrorRateIncrease: canary.MaxErrorRateIncrease, MaxLatencyIncreasePercent: copyInt(canary.MaxLatencyIncreasePercent),
		MinCalls: canary.MinCalls, WindowMinutes: canary.WindowMinutes, State: canary.State,
		Reason: optionalString(canary.Reason), StartedAt: canary.StartedAt, DecidedAt: canary.DecidedAt}
}

// PutPromptCanary starts or replaces a prompt's canary.
func (s *Service) PutPromptCanary(w http.ResponseWriter, r *http.Request, acct state.Account) {
	name, ok := promptPathName(w, r)
	if !ok {
		return
	}
	var req api.PutOutboundAIPromptCanaryRequest
	if err := httpkit.DecodeJSONSized(r, &req, 4<<10); err != nil || !req.Valid() {
		problem(w, http.StatusBadRequest, api.CodeValidation, promptCanaryInvalidDetail)
		return
	}
	store, ok := s.promptStore(w)
	if !ok {
		return
	}
	req = req.Normalized()
	canary, err := store.SetOutboundAIPromptCanary(r.Context(), acct.ID, name, state.OutboundAIPromptCanary{
		Version: req.Version, Percent: req.Percent, MaxErrorRateIncrease: *req.MaxErrorRateIncrease,
		MaxLatencyIncreasePercent: req.MaxLatencyIncreasePercent, MinCalls: *req.MinCalls, WindowMinutes: *req.WindowMinutes,
	})
	if writePromptError(w, err, "AI prompt not found", promptCanaryInvalidDetail) {
		return
	}
	writePromptJSON(w, http.StatusOK, promptCanaryResponse(canary))
}

// PromotePromptCanary makes the canary's version live and ends the canary.
func (s *Service) PromotePromptCanary(w http.ResponseWriter, r *http.Request, acct state.Account) {
	name, ok := promptPathName(w, r)
	if !ok {
		return
	}
	store, ok := s.promptStore(w)
	if !ok {
		return
	}
	prompt, err := store.PromoteOutboundAIPromptCanary(r.Context(), acct.ID, name)
	if writePromptError(w, err, "AI prompt canary not found", "The canary's version no longer exists") {
		return
	}
	writePromptJSON(w, http.StatusOK, promptResponse(prompt))
}

func (s *Service) DeletePromptCanary(w http.ResponseWriter, r *http.Request, acct state.Account) {
	name, ok := promptPathName(w, r)
	if !ok {
		return
	}
	store, ok := s.promptStore(w)
	if !ok {
		return
	}
	err := store.DeleteOutboundAIPromptCanary(r.Context(), acct.ID, name)
	if writePromptError(w, err, "AI prompt canary not found", "") {
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusNoContent)
}

func promptVersionStatsResponse(stats state.OutboundAIPromptVersionStats) api.OutboundAIPromptVersionStats {
	out := api.OutboundAIPromptVersionStats{Version: stats.Version, Calls: stats.Calls, Errors: stats.Errors,
		CostMillicents: stats.CostMillicents, GoodFeedback: stats.GoodFeedback, BadFeedback: stats.BadFeedback}
	if stats.Calls > 0 {
		out.ErrorRate = float64(stats.Errors) / float64(stats.Calls)
	}
	if stats.TTFBCalls > 0 {
		mean := stats.TTFBMSTotal / stats.TTFBCalls
		out.MeanTTFBMS = &mean
	}
	if successes := stats.Calls - stats.Errors; successes > 0 {
		mean := stats.CostMillicents / successes
		out.MeanCostMillicents = &mean
	}
	return out
}

// GetPromptStats returns each version's calls and feedback over the stats
// window.
func (s *Service) GetPromptStats(w http.ResponseWriter, r *http.Request, acct state.Account) {
	name, ok := promptPathName(w, r)
	if !ok {
		return
	}
	store, ok := s.promptStore(w)
	if !ok {
		return
	}
	window, err := store.OutboundAIPromptStats(r.Context(), acct.ID, name)
	if writePromptError(w, err, "AI prompt not found", "") {
		return
	}
	out := api.OutboundAIPromptStats{WindowStart: window.WindowStart, LiveVersion: copyInt(window.Prompt.LiveVersion),
		Versions: make([]api.OutboundAIPromptVersionStats, 0, len(window.Versions))}
	if canary := window.Prompt.Canary; canary != nil && canary.State == api.OutboundAIRouteCanaryActive {
		out.CanaryVersion = copyInt(&canary.Version)
	}
	for _, stats := range window.Versions {
		out.Versions = append(out.Versions, promptVersionStatsResponse(stats))
	}
	writePromptJSON(w, http.StatusOK, out)
}

// CreatePromptEvaluation queues an evaluation of a version against the live
// version on the caller's variable sets.
func (s *Service) CreatePromptEvaluation(w http.ResponseWriter, r *http.Request, acct state.Account) {
	name, ok := promptPathName(w, r)
	if !ok {
		return
	}
	var req api.CreateOutboundAIPromptEvaluationRequest
	if err := httpkit.DecodeJSONSized(r, &req, 1<<20); err != nil {
		problem(w, http.StatusBadRequest, api.CodeValidation, "The body must be an evaluation request of at most 1 MiB")
		return
	}
	if detail := req.Problem(); detail != "" {
		problem(w, http.StatusBadRequest, api.CodeValidation, detail)
		return
	}
	store, ok := s.promptStore(w)
	if !ok {
		return
	}
	prompt, err := store.GetOutboundAIPrompt(r.Context(), acct.ID, name)
	if writePromptError(w, err, "AI prompt not found", "") {
		return
	}
	req = req.Normalized()
	created, err := store.CreateOutboundAIPromptEvaluation(r.Context(), state.OutboundAIPromptEvaluation{
		ID: uuid.NewString(), AccountID: acct.ID, PromptID: prompt.ID, CandidateVersion: req.Version, Cases: req.Cases,
		JudgeIntegrationID: req.JudgeIntegrationID, JudgeModel: req.JudgeModel, MaxLossRate: *req.MaxLossRate, Canary: req.Canary,
	})
	if writePromptError(w, err, "AI prompt not found", promptEvaluationInvalidDetail) {
		return
	}
	writePromptJSON(w, http.StatusAccepted, promptEvaluationResponse(created, false))
}

func (s *Service) ListPromptEvaluations(w http.ResponseWriter, r *http.Request, acct state.Account) {
	name, ok := promptPathName(w, r)
	if !ok {
		return
	}
	store, ok := s.promptStore(w)
	if !ok {
		return
	}
	evaluations, err := store.ListOutboundAIPromptEvaluations(r.Context(), acct.ID, name)
	if writePromptError(w, err, "AI prompt not found", "") {
		return
	}
	out := api.OutboundAIPromptEvaluationList{Items: make([]api.OutboundAIPromptEvaluation, 0, len(evaluations))}
	for _, evaluation := range evaluations {
		out.Items = append(out.Items, promptEvaluationResponse(evaluation, false))
	}
	writePromptJSON(w, http.StatusOK, out)
}

// GetPromptEvaluation returns one evaluation with its cases' results; the
// answer texts need content scope (ADR 20261027132936 amendment 1).
func (s *Service) GetPromptEvaluation(w http.ResponseWriter, r *http.Request, acct state.Account) {
	evaluationID := r.PathValue("evaluation")
	if _, err := uuid.Parse(evaluationID); err != nil {
		problem(w, http.StatusBadRequest, api.CodeValidation, "Evaluation ID must be a UUID")
		return
	}
	store, ok := s.promptStore(w)
	if !ok {
		return
	}
	evaluation, err := store.GetOutboundAIPromptEvaluation(r.Context(), acct.ID, evaluationID)
	if writePromptError(w, err, "AI prompt evaluation not found", "") {
		return
	}
	writePromptJSON(w, http.StatusOK, promptEvaluationResponse(evaluation, s.contentReadable(r)))
}

func promptEvaluationResponse(e state.OutboundAIPromptEvaluation, content bool) api.OutboundAIPromptEvaluation {
	out := api.OutboundAIPromptEvaluation{
		ID: e.ID, Prompt: e.PromptName, BaselineVersion: e.BaselineVersion, CandidateVersion: e.CandidateVersion,
		JudgeIntegrationID: e.JudgeIntegrationID, JudgeModel: e.JudgeModel, MaxLossRate: e.MaxLossRate, Canary: e.Canary,
		State: e.State, Verdict: optionalString(e.Verdict), ErrorCode: optionalString(e.ErrorCode), Cases: len(e.Cases),
		Wins: e.Wins, Ties: e.Ties, Losses: e.Losses, CandidateErrors: e.CandidateErrors, JudgeErrors: e.JudgeErrors,
		Baseline:      api.OutboundAIEvaluationTargetStats{CostMillicents: e.BaselineCostMillicents, MeanTTFBMS: meanMS(e.BaselineTTFBMSTotal, e.BaselineTTFBCalls)},
		Candidate:     api.OutboundAIEvaluationTargetStats{CostMillicents: e.CandidateCostMillicents, MeanTTFBMS: meanMS(e.CandidateTTFBMSTotal, e.CandidateTTFBCalls)},
		CanaryStarted: e.CanaryStarted, CreatedAt: e.CreatedAt, StartedAt: e.StartedAt, FinishedAt: e.FinishedAt,
		ContentWithheld: !content && len(e.Items) > 0,
	}
	for _, item := range e.Items {
		if !content {
			item.BaselineOutputText, item.CandidateOutputText = "", ""
		}
		out.Items = append(out.Items, api.OutboundAIPromptEvaluationItem{
			Case: item.Case, Verdict: item.Verdict, BaselineStatus: copyInt(item.BaselineStatus),
			CandidateStatus: copyInt(item.CandidateStatus), BaselineCostMillicents: item.BaselineCostMillicents,
			CandidateCostMillicents: item.CandidateCostMillicents, BaselineTTFBMS: copyInt64(item.BaselineTTFBMS),
			CandidateTTFBMS: copyInt64(item.CandidateTTFBMS), BaselineOutputText: item.BaselineOutputText,
			CandidateOutputText: item.CandidateOutputText,
		})
	}
	return out
}
