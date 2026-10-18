package state

import (
	"context"
	"errors"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// ErrOutboundAIEvaluationLimit reports a route at its daily evaluations.
var ErrOutboundAIEvaluationLimit = errors.New("state: outbound AI evaluation limit reached")

// OutboundAIEvaluation is an evaluation of a route target against the
// route's first target (ADR-1431). apid creates it; outboundd claims it and
// writes the totals and items. Position is the candidate's 1-based route
// position; Canary, when set, starts on it if the evaluation passes.
type OutboundAIEvaluation struct {
	ID                      string
	AccountID               string
	RouteID                 string
	Position                int
	JudgeIntegrationID      string
	JudgeModel              string
	SampleSize              int
	MaxLossRate             int
	Canary                  *api.PutOutboundAIRouteCanaryRequest
	State                   string
	Verdict                 string
	ErrorCode               string
	Samples                 int
	Wins                    int
	Ties                    int
	Losses                  int
	CandidateErrors         int
	JudgeErrors             int
	BaselineCostMillicents  int64
	CandidateCostMillicents int64
	BaselineTTFBMSTotal     int64
	BaselineTTFBCalls       int
	CandidateTTFBMSTotal    int64
	CandidateTTFBCalls      int
	CanaryStarted           bool
	CreatedAt               time.Time
	StartedAt               *time.Time
	FinishedAt              *time.Time
	Items                   []OutboundAIEvaluationItem
}

// OutboundAIEvaluationItem is one sample's result.
type OutboundAIEvaluationItem struct {
	LogID                   string
	Verdict                 string
	CandidateStatus         *int
	CandidateCostMillicents int64
	CandidateTTFBMS         *int64
	BaselineCostMillicents  int64
	BaselineTTFBMS          *int64
	BaselineOutputText      string
	CandidateOutputText     string
}

// OutboundAIEvaluationStore records evaluations and reads them back.
type OutboundAIEvaluationStore interface {
	// CreateOutboundAIEvaluation checks that the route has the position and
	// that the judge is one of the account's enabled AI integrations, and
	// holds a route to OutboundAIEvaluationsPerDay per UTC day.
	CreateOutboundAIEvaluation(ctx context.Context, evaluation OutboundAIEvaluation) (OutboundAIEvaluation, error)
	// ListOutboundAIEvaluations lists a route's latest evaluations, newest
	// first, without items.
	ListOutboundAIEvaluations(ctx context.Context, accountID, routeID string) ([]OutboundAIEvaluation, error)
	GetOutboundAIEvaluation(ctx context.Context, accountID, evaluationID string) (OutboundAIEvaluation, error)
}

// outboundAIEvaluationsListMax bounds one route's listing.
const outboundAIEvaluationsListMax = 100
