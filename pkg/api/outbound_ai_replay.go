package api

import (
	"net/http"
	"time"

	"github.com/google/uuid"
)

// Replay states (ADR-997).
const (
	OutboundAIReplayPending = "pending"
	OutboundAIReplayRunning = "running"
	OutboundAIReplayDone    = "done"
	OutboundAIReplayFailed  = "failed"
)

// OutboundAIReplayTarget is one integration a captured call is replayed on,
// with an optional model. A target whose provider differs from the
// captured call's needs a model.
type OutboundAIReplayTarget struct {
	IntegrationID string `json:"integration_id"`
	Model         string `json:"model,omitempty"`
}

// CreateOutboundAIReplayRequest replays a captured call on up to
// OutboundAIReplayTargetsMax targets (ADR-997).
type CreateOutboundAIReplayRequest struct {
	Targets []OutboundAIReplayTarget `json:"targets"`
}

// Valid checks the target count, IDs and model names.
func (r CreateOutboundAIReplayRequest) Valid() bool {
	if len(r.Targets) == 0 || len(r.Targets) > OutboundAIReplayTargetsMax {
		return false
	}
	for _, target := range r.Targets {
		if _, err := uuid.Parse(target.IntegrationID); err != nil {
			return false
		}
		if target.Model != "" && !validAIModelText(target.Model) {
			return false
		}
	}
	return true
}

// OutboundAIReplaySource summarizes the captured call a replay resends.
// OutputText is the text read from its captured response, when any.
type OutboundAIReplaySource struct {
	Model             string  `json:"model"`
	Status            int     `json:"status"`
	InputTokens       int64   `json:"input_tokens"`
	OutputTokens      int64   `json:"output_tokens"`
	CostMillicents    int64   `json:"cost_millicents"`
	DurationMS        int64   `json:"duration_ms"`
	TimeToFirstByteMS *int64  `json:"ttfb_ms"`
	OutputText        *string `json:"output_text"`
}

// OutboundAIReplayResult is one target's replayed call. Status is null when
// the gateway sent no response, Outcome null when the call was refused
// before it reached the provider, and ErrorCode the problem code of a
// refused or failed call.
type OutboundAIReplayResult struct {
	IntegrationID     string                `json:"integration_id"`
	Model             *string               `json:"model"`
	Status            *int                  `json:"status"`
	Outcome           *string               `json:"outcome"`
	ErrorCode         *string               `json:"error_code"`
	InputTokens       int64                 `json:"input_tokens"`
	OutputTokens      int64                 `json:"output_tokens"`
	CostMillicents    int64                 `json:"cost_millicents"`
	DurationMS        int64                 `json:"duration_ms"`
	TimeToFirstByteMS *int64                `json:"ttfb_ms"`
	OutputText        string                `json:"output_text"`
	Response          OutboundAIContentBody `json:"response"`
}

// OutboundAIReplay is a replay of one captured call and, once done, its
// results in target order (ADR-997). Replayed calls settle under the
// "replay" caller and count against the targets' budgets.
type OutboundAIReplay struct {
	ID            string                   `json:"id"`
	IntegrationID string                   `json:"integration_id"`
	RequestID     string                   `json:"request_id"`
	State         string                   `json:"state"`
	ErrorCode     *string                  `json:"error_code"`
	CreatedAt     time.Time                `json:"created_at"`
	FinishedAt    *time.Time               `json:"finished_at"`
	Targets       []OutboundAIReplayTarget `json:"targets"`
	Source        OutboundAIReplaySource   `json:"source"`
	Results       []OutboundAIReplayResult `json:"results"`
}

// ErrOutboundAIReplayLimit is the problem for an integration that reached
// its replays for the UTC day.
func ErrOutboundAIReplayLimit(observed int64) *Problem {
	return NewProblem(http.StatusTooManyRequests, "outbound_ai_replay_limit", "AI replay limit reached",
		"An integration can replay at most 100 captured calls per UTC day.").
		WithLimit(OutboundAIReplaysPerDay, observed).
		WithDocs(docsBase + "/outbound-integrations#replay")
}
