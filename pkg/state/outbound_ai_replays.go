package state

import (
	"context"
	"errors"
	"time"
)

// ErrOutboundAIReplayLimit reports an integration that replayed its
// captured calls for the UTC day (ADR-997).
var ErrOutboundAIReplayLimit = errors.New("state: outbound AI replay limit reached")

// ErrOutboundAIReplayUnavailable reports a logged call without a capture
// that can be resent: no capture, a truncated request, or no path.
var ErrOutboundAIReplayUnavailable = errors.New("state: the AI call has no replayable capture")

// OutboundAIReplayTarget is one replay target; Model is empty to keep the
// captured call's model.
type OutboundAIReplayTarget struct {
	IntegrationID string
	Model         string
}

// OutboundAIReplayResult is what outboundd recorded for one target. Status
// is nil without a response and Outcome empty when the call did not settle.
type OutboundAIReplayResult struct {
	IntegrationID       string
	Model               string
	Status              *int
	Outcome             string
	ErrorCode           string
	InputTokens         int64
	OutputTokens        int64
	CostMillicents      int64
	DurationMS          int64
	TTFBMS              *int64
	ResponseBody        []byte
	ResponseTruncated   bool
	ResponseContentType string
	OutputText          string
}

// OutboundAIReplay is one replay of a captured call (ADR-997). apid writes
// it; outboundd moves its state and writes the results.
type OutboundAIReplay struct {
	ID               string
	AccountID        string
	IntegrationID    string
	LogID            string
	Targets          []OutboundAIReplayTarget
	State            string
	ErrorCode        string
	SourceOutputText string
	CreatedAt        time.Time
	FinishedAt       *time.Time
	Source           OutboundAIRequestLogEntry
	Results          []OutboundAIReplayResult
}

// OutboundAIReplayStore records replays and reads them back. Create checks
// the capture, the targets and the integration's daily replays.
type OutboundAIReplayStore interface {
	CreateOutboundAIReplay(ctx context.Context, replay OutboundAIReplay) (OutboundAIReplay, error)
	GetOutboundAIReplay(ctx context.Context, accountID, replayID string) (OutboundAIReplay, error)
}
