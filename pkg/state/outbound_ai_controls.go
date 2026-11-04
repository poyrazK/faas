package state

import (
	"context"
	"errors"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// ErrOutboundCredentialKeyLimit reports an integration at its key pool
// ceiling (ADR-991).
var ErrOutboundCredentialKeyLimit = errors.New("state: outbound credential key limit reached")

// OutboundAIGuardrails are one app binding's per-minute limits, model
// allow-list and output ceiling (ADR-991). Nil fields are unset.
type OutboundAIGuardrails struct {
	RequestsPerMinute *int64
	TokensPerMinute   *int64
	AllowedModels     []string
	MaxOutputTokens   *int64
}

func (g OutboundAIGuardrails) valid() bool {
	return api.PutOutboundBindingAIGuardrailsRequest{
		RequestsPerMinute: g.RequestsPerMinute, TokensPerMinute: g.TokensPerMinute,
		AllowedModels: g.AllowedModels, MaxOutputTokens: g.MaxOutputTokens,
	}.Valid()
}

// OutboundCredentialKey is one extra sealed provider key. Only apid writes
// it and only outboundd opens AuthorizationSealed.
type OutboundCredentialKey struct {
	ID                  string
	AccountID           string
	IntegrationID       string
	Label               string
	AuthorizationSealed []byte
	CreatedAt           time.Time
}

// OutboundAIBudgetAlert is one budget threshold outboundd recorded at
// settlement. AppID is set only for an app budget, EndUser only for an end
// user's budget (ADR-995).
type OutboundAIBudgetAlert struct {
	ID            string
	IntegrationID string
	Budget        string
	AppID         string
	EndUser       string
	// Tag is the tag of a tag_daily_cost alert (ADR-1438).
	Tag              string
	PeriodStart      time.Time
	ThresholdPercent int32
	Limit            int64
	Used             int64
	CrossedAt        time.Time
}

// OutboundAIRequestLogEntry is one AI call's metadata outboundd logged.
type OutboundAIRequestLogEntry struct {
	ID                string
	CreatedAt         time.Time
	Caller            string
	RouteID           string
	Model             string
	Status            int
	Outcome           string
	InputTokens       int64
	CachedInputTokens int64
	CacheWriteTokens  int64
	OutputTokens      int64
	CostMillicents    int64
	DurationMS        int64
	TTFBMS            *int64
	Attempts          int
	// EndUser is empty when the call named no end user (ADR-994).
	EndUser string
	// Tags are the call's usage tags (ADR-1436).
	Tags []string
	// ContentCaptured reports stored bodies for the call (ADR-996).
	ContentCaptured bool
	// TraceID is the call's AI trace, and ToolCalls the tools its response
	// requested (ADR 20261020202035).
	TraceID   string
	ToolCalls []string
	// QualityFlags mark what went wrong with the answer; Feedback is the
	// call's rating ("good" or "bad", or empty) with its label; and
	// EvaluationScore is its online evaluation's score from 1 to 5
	// (ADR 20261021171712).
	QualityFlags    []string
	Feedback        string
	FeedbackLabel   string
	EvaluationScore *int32
}

// OutboundAIRequestContent is one logged call's captured bodies (ADR-996):
// the start of each as outboundd sent and received it, never headers.
type OutboundAIRequestContent struct {
	LogID string
	// RequestPath is the call's escaped provider path; empty for captures
	// made before ADR-997.
	RequestPath         string
	CreatedAt           time.Time
	RequestBody         []byte
	RequestTruncated    bool
	ResponseBody        []byte
	ResponseTruncated   bool
	ResponseContentType string
}

// OutboundAIContentStore reads the content outboundd captured. It is
// separate from OutboundAIControlStore so existing implementations do not
// change.
type OutboundAIContentStore interface {
	GetOutboundAIRequestContent(ctx context.Context, accountID, integrationID, logID string) (OutboundAIRequestContent, error)
}

// OutboundAIRequestCallerStore names who made one logged call of the
// account's integration, so apid can withhold a call of an app its reader
// may no longer see.
type OutboundAIRequestCallerStore interface {
	OutboundAIRequestCaller(ctx context.Context, accountID, integrationID, logID string) (string, error)
}

// OutboundAIControlStore owns the ADR-991 customer settings (guardrails and
// key pools) and reads what outboundd records (alerts and the request log).
type OutboundAIControlStore interface {
	SetOutboundBindingAIGuardrails(ctx context.Context, accountID, appID, integrationID string, guard OutboundAIGuardrails) error
	ListOutboundCredentialKeys(ctx context.Context, accountID, integrationID string) ([]OutboundCredentialKey, error)
	CreateOutboundCredentialKey(ctx context.Context, key OutboundCredentialKey) (OutboundCredentialKey, error)
	DeleteOutboundCredentialKey(ctx context.Context, accountID, integrationID, keyID string) error
	ListOutboundAIBudgetAlerts(ctx context.Context, accountID, integrationID string, limit int) ([]OutboundAIBudgetAlert, error)
	ListOutboundAIRequestLog(ctx context.Context, accountID, integrationID string, before *time.Time, limit int) ([]OutboundAIRequestLogEntry, error)
}

func boundedPage(limit, maximum int) int {
	if limit <= 0 || limit > maximum {
		return maximum
	}
	return limit
}
