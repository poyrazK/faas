package state

import (
	"context"
	"errors"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// Managed AI prompts (ADR 20261114180740). apid is the only writer of
// prompts, versions, canaries and evaluations; outboundd reads them per call
// and writes minute rows, canary rollbacks and evaluation results.

// ErrOutboundAIPromptLimit reports an account at its plan's prompt limit.
var ErrOutboundAIPromptLimit = errors.New("state: outbound AI prompt limit reached")

// OutboundAIPrompt is a managed prompt.
type OutboundAIPrompt struct {
	ID                  string
	AccountID           string
	Name                string
	Description         string
	LiveVersion         *int
	PreviousLiveVersion *int
	LatestVersion       int
	Canary              *OutboundAIPromptCanary
	CreatedAt           time.Time
	UpdatedAt           time.Time
}

// OutboundAIPromptVersion is one immutable version. Messages, Defaults and
// Params are left empty when versions are listed.
type OutboundAIPromptVersion struct {
	PromptID  string
	Version   int
	Messages  []api.OutboundAIPromptMessage
	Defaults  map[string]string
	Params    api.OutboundAIPromptParams
	Target    api.OutboundAIPromptTarget
	Note      string
	CreatedAt time.Time
}

// OutboundAIPromptCanary is a prompt's canary.
type OutboundAIPromptCanary struct {
	Version                   int
	Percent                   int
	MaxErrorRateIncrease      int
	MaxLatencyIncreasePercent *int
	MinCalls                  int
	WindowMinutes             int
	State                     string
	Reason                    string
	StartedAt                 time.Time
	DecidedAt                 *time.Time
}

// OutboundAIPromptPush stores a version: on a new prompt, ID names the
// prompt to create and the version is live; otherwise Live makes it live.
// MaxPrompts is the account plan's prompt limit.
type OutboundAIPromptPush struct {
	ID          string
	AccountID   string
	Name        string
	Description *string
	Version     OutboundAIPromptVersion
	Live        bool
	MaxPrompts  int
}

// OutboundAIPromptVersionStats is one version's minute rows and feedback
// over the stats window.
type OutboundAIPromptVersionStats struct {
	Version                               int
	Calls, Errors, TTFBCalls, TTFBMSTotal int64
	CostMillicents                        int64
	GoodFeedback, BadFeedback             int64
}

// OutboundAIPromptStatsWindow is every version with calls since
// WindowStart.
type OutboundAIPromptStatsWindow struct {
	WindowStart time.Time
	Prompt      OutboundAIPrompt
	Versions    []OutboundAIPromptVersionStats
}

// OutboundAIPromptEvaluation is an evaluation of a candidate version
// against the version live when it was created.
type OutboundAIPromptEvaluation struct {
	ID                      string
	AccountID               string
	PromptID                string
	PromptName              string
	BaselineVersion         int
	CandidateVersion        int
	Cases                   []map[string]string
	JudgeIntegrationID      string
	JudgeModel              string
	MaxLossRate             int
	Canary                  *api.PutOutboundAIPromptCanaryRequest
	State                   string
	Verdict                 string
	ErrorCode               string
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
	Items                   []OutboundAIPromptEvaluationItem
}

// OutboundAIPromptEvaluationItem is one case's result.
type OutboundAIPromptEvaluationItem struct {
	Case                    int
	Verdict                 string
	BaselineStatus          *int
	CandidateStatus         *int
	BaselineCostMillicents  int64
	CandidateCostMillicents int64
	BaselineTTFBMS          *int64
	CandidateTTFBMS         *int64
	BaselineOutputText      string
	CandidateOutputText     string
}

// OutboundAIPromptStore keeps managed prompts. Prompts are addressed by
// account and name. A write naming a version the prompt does not have, or a
// target the account cannot use, returns ErrInvalidArgument.
type OutboundAIPromptStore interface {
	ListOutboundAIPrompts(ctx context.Context, accountID string) ([]OutboundAIPrompt, error)
	GetOutboundAIPrompt(ctx context.Context, accountID, name string) (OutboundAIPrompt, error)
	// PushOutboundAIPromptVersion stores a version, creating the prompt on
	// its first one, and reports whether it stored one: a push identical to
	// the latest version returns that version.
	PushOutboundAIPromptVersion(ctx context.Context, push OutboundAIPromptPush) (OutboundAIPrompt, OutboundAIPromptVersion, bool, error)
	ListOutboundAIPromptVersions(ctx context.Context, accountID, name string) ([]OutboundAIPromptVersion, error)
	GetOutboundAIPromptVersion(ctx context.Context, accountID, name string, version int) (OutboundAIPromptVersion, error)
	// SetOutboundAIPromptLive makes a version live, keeps the old live
	// version as the previous one and ends any canary.
	SetOutboundAIPromptLive(ctx context.Context, accountID, name string, version int) (OutboundAIPrompt, error)
	// RollBackOutboundAIPromptLive swaps the live and previous live
	// versions and ends any canary.
	RollBackOutboundAIPromptLive(ctx context.Context, accountID, name string) (OutboundAIPrompt, error)
	DeleteOutboundAIPrompt(ctx context.Context, accountID, name string) error
	// SetOutboundAIPromptCanary starts or replaces the canary and clears its
	// version's minute rows. The version must exist and not be live.
	SetOutboundAIPromptCanary(ctx context.Context, accountID, name string, canary OutboundAIPromptCanary) (OutboundAIPromptCanary, error)
	DeleteOutboundAIPromptCanary(ctx context.Context, accountID, name string) error
	// PromoteOutboundAIPromptCanary makes an active canary's version live
	// and ends the canary.
	PromoteOutboundAIPromptCanary(ctx context.Context, accountID, name string) (OutboundAIPrompt, error)
	OutboundAIPromptStats(ctx context.Context, accountID, name string) (OutboundAIPromptStatsWindow, error)
	// CreateOutboundAIPromptEvaluation records a pending evaluation of a
	// version against the live version, whose number it fills in. The
	// judge must be the account's enabled managed AI integration.
	CreateOutboundAIPromptEvaluation(ctx context.Context, evaluation OutboundAIPromptEvaluation) (OutboundAIPromptEvaluation, error)
	ListOutboundAIPromptEvaluations(ctx context.Context, accountID, name string) ([]OutboundAIPromptEvaluation, error)
	// GetOutboundAIPromptEvaluation returns one evaluation with its items.
	GetOutboundAIPromptEvaluation(ctx context.Context, accountID, evaluationID string) (OutboundAIPromptEvaluation, error)
}

// validOutboundAIPromptCanary checks a canary's settings; the database
// enforces the same ranges.
func validOutboundAIPromptCanary(canary OutboundAIPromptCanary) bool {
	return api.PutOutboundAIPromptCanaryRequest{Version: canary.Version, Percent: canary.Percent,
		MaxErrorRateIncrease: &canary.MaxErrorRateIncrease, MaxLatencyIncreasePercent: canary.MaxLatencyIncreasePercent,
		MinCalls: &canary.MinCalls, WindowMinutes: &canary.WindowMinutes}.Valid()
}

// outboundAIPromptPushRequest is the version as a push request, to compare
// a push with the latest version.
func outboundAIPromptPushRequest(version OutboundAIPromptVersion) api.PushOutboundAIPromptVersionRequest {
	return api.PushOutboundAIPromptVersionRequest{Messages: version.Messages, Defaults: version.Defaults, Target: version.Target,
		Params: version.Params, Note: version.Note}
}

// validOutboundAIPromptVersion checks a version's shape before storing it.
func validOutboundAIPromptVersion(push OutboundAIPromptPush) bool {
	request := outboundAIPromptPushRequest(push.Version)
	request.Description = push.Description
	return api.ValidOutboundAIPromptName(push.Name) && request.Problem() == ""
}

// outboundAIPromptPrunable picks the oldest version beyond the version limit
// that is neither live, previous live nor the canary's, or 0.
func outboundAIPromptPrunable(versions []int, prompt OutboundAIPrompt) int {
	if len(versions) <= api.OutboundAIPromptVersionsMax {
		return 0
	}
	for _, version := range versions {
		if prompt.LiveVersion != nil && *prompt.LiveVersion == version {
			continue
		}
		if prompt.PreviousLiveVersion != nil && *prompt.PreviousLiveVersion == version {
			continue
		}
		if prompt.Canary != nil && prompt.Canary.Version == version {
			continue
		}
		return version
	}
	return 0
}

func copyOutboundAIPrompt(in OutboundAIPrompt) OutboundAIPrompt {
	out := in
	out.LiveVersion = copyInt(in.LiveVersion)
	out.PreviousLiveVersion = copyInt(in.PreviousLiveVersion)
	if in.Canary != nil {
		canary := *in.Canary
		canary.MaxLatencyIncreasePercent = copyInt(in.Canary.MaxLatencyIncreasePercent)
		out.Canary = &canary
	}
	return out
}

func promptVersionPtr(value int) *int { return &value }
