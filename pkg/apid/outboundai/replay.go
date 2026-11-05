package outboundai

import (
	"errors"
	"net/http"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/httpkit"
	"github.com/onebox-faas/faas/pkg/state"
)

const replayInvalidDetail = "targets must list 1 to 5 of the account's AI integrations; a target whose provider differs from the captured call's needs a model"

func (s *Service) replayStore(w http.ResponseWriter) (state.OutboundAIReplayStore, bool) {
	store, ok := s.Host.Store().(state.OutboundAIReplayStore)
	if !ok {
		problem(w, http.StatusServiceUnavailable, "outbound_ai_unavailable", "Outbound AI replays are unavailable")
	}
	return store, ok
}

// CreateReplay records a replay of one captured call on up to five
// targets; outboundd runs it (ADR-997).
func (s *Service) CreateReplay(w http.ResponseWriter, r *http.Request, acct state.Account) {
	integrationID, ok := integrationPathID(w, r)
	if !ok {
		return
	}
	requestID := r.PathValue("request")
	var req api.CreateOutboundAIReplayRequest
	if _, err := uuid.Parse(requestID); err != nil || httpkit.DecodeJSONSized(r, &req, 8<<10) != nil || !req.Valid() {
		problem(w, http.StatusBadRequest, api.CodeValidation, replayInvalidDetail)
		return
	}
	store, ok := s.replayStore(w)
	if !ok || !s.requestVisible(w, r, acct, integrationID, requestID, "logged AI call not found") {
		return
	}
	replay := state.OutboundAIReplay{AccountID: acct.ID, IntegrationID: integrationID, LogID: requestID}
	for _, target := range req.Targets {
		replay.Targets = append(replay.Targets, state.OutboundAIReplayTarget{IntegrationID: target.IntegrationID, Model: target.Model})
	}
	created, err := store.CreateOutboundAIReplay(r.Context(), replay)
	switch {
	case errors.Is(err, state.ErrNotFound):
		httpkit.NotFound(w, "logged AI call not found")
	case errors.Is(err, state.ErrInvalidArgument):
		problem(w, http.StatusBadRequest, api.CodeValidation, replayInvalidDetail)
	case errors.Is(err, state.ErrOutboundAIReplayUnavailable):
		problem(w, http.StatusConflict, "outbound_ai_replay_unavailable",
			"The call has no capture that can be resent; turn on request_log_content and replay a call captured whole")
	case errors.Is(err, state.ErrOutboundAIReplayLimit):
		api.WriteProblem(w, api.ErrOutboundAIReplayLimit(api.OutboundAIReplaysPerDay))
	case err != nil:
		problem(w, http.StatusServiceUnavailable, "outbound_ai_unavailable", "The AI replay could not be recorded")
	default:
		w.Header().Set("Cache-Control", "no-store")
		httpkit.WriteJSON(w, http.StatusAccepted, replayResponse(created))
	}
}

// GetReplay reads a replay and, once done, its results.
func (s *Service) GetReplay(w http.ResponseWriter, r *http.Request, acct state.Account) {
	replayID := r.PathValue("replay")
	if _, err := uuid.Parse(replayID); err != nil {
		problem(w, http.StatusBadRequest, api.CodeValidation, "Replay ID must be a UUID")
		return
	}
	store, ok := s.replayStore(w)
	if !ok {
		return
	}
	replay, err := store.GetOutboundAIReplay(r.Context(), acct.ID, replayID)
	switch {
	case errors.Is(err, state.ErrNotFound):
		httpkit.NotFound(w, "AI replay not found")
	case err != nil:
		problem(w, http.StatusServiceUnavailable, "outbound_ai_unavailable", "The AI replay is unavailable")
	case !s.requestVisible(w, r, acct, replay.IntegrationID, replay.LogID, "AI replay not found"):
		// requestVisible wrote the problem.
	default:
		w.Header().Set("Cache-Control", "no-store")
		httpkit.WriteJSON(w, http.StatusOK, replayResponse(replay))
	}
}

func replayResponse(replay state.OutboundAIReplay) api.OutboundAIReplay {
	out := api.OutboundAIReplay{
		ID: replay.ID, IntegrationID: replay.IntegrationID, RequestID: replay.LogID, State: replay.State,
		ErrorCode: optionalString(replay.ErrorCode), CreatedAt: replay.CreatedAt, FinishedAt: replay.FinishedAt,
		Targets: make([]api.OutboundAIReplayTarget, 0, len(replay.Targets)),
		Source: api.OutboundAIReplaySource{
			Model: replay.Source.Model, Status: replay.Source.Status, InputTokens: replay.Source.InputTokens,
			OutputTokens: replay.Source.OutputTokens, CostMillicents: replay.Source.CostMillicents,
			DurationMS: replay.Source.DurationMS, TimeToFirstByteMS: copyInt64(replay.Source.TTFBMS),
			OutputText: optionalString(replay.SourceOutputText),
		},
		Results: make([]api.OutboundAIReplayResult, 0, len(replay.Results)),
	}
	for _, target := range replay.Targets {
		out.Targets = append(out.Targets, api.OutboundAIReplayTarget{IntegrationID: target.IntegrationID, Model: target.Model})
	}
	for _, result := range replay.Results {
		item := api.OutboundAIReplayResult{
			IntegrationID: result.IntegrationID, Model: optionalString(result.Model), Outcome: optionalString(result.Outcome),
			ErrorCode: optionalString(result.ErrorCode), InputTokens: result.InputTokens, OutputTokens: result.OutputTokens,
			CostMillicents: result.CostMillicents, DurationMS: result.DurationMS, TimeToFirstByteMS: copyInt64(result.TTFBMS),
			OutputText: result.OutputText,
			Response:   contentBody(result.ResponseBody, result.ResponseTruncated, result.ResponseContentType),
		}
		if result.Status != nil {
			status := *result.Status
			item.Status = &status
		}
		out.Results = append(out.Results, item)
	}
	return out
}

func optionalString(value string) *string {
	if value == "" {
		return nil
	}
	return &value
}
