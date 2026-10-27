package api

import (
	"net/http"
	"regexp"
	"strings"
	"time"
)

// Evaluation states, verdicts and sample verdicts (ADR-1431).
const (
	OutboundAIEvaluationPending = "pending"
	OutboundAIEvaluationRunning = "running"
	OutboundAIEvaluationDone    = "done"
	OutboundAIEvaluationFailed  = "failed"

	OutboundAIEvaluationPass = "pass"
	OutboundAIEvaluationFail = "fail"

	OutboundAIEvaluationItemWin            = "win"
	OutboundAIEvaluationItemTie            = "tie"
	OutboundAIEvaluationItemLoss           = "loss"
	OutboundAIEvaluationItemCandidateError = "candidate_error"
	OutboundAIEvaluationItemJudgeError     = "judge_error"
)

var outboundAIEvaluationModel = regexp.MustCompile(`^[ -~]{1,128}$`)

// CreateOutboundAIEvaluationRequest evaluates a route target against the
// route's first target. Target is the 1-based position after the first.
// Canary, when set, starts on that target if the evaluation passes; its
// target must be the evaluated one.
type CreateOutboundAIEvaluationRequest struct {
	Target             int                              `json:"target"`
	JudgeIntegrationID string                           `json:"judge_integration_id"`
	JudgeModel         string                           `json:"judge_model"`
	SampleSize         *int                             `json:"sample_size,omitempty"`
	MaxLossRate        *int                             `json:"max_loss_rate,omitempty"`
	Canary             *PutOutboundAIRouteCanaryRequest `json:"canary,omitempty"`
}

// Normalized fills omitted settings with their defaults.
func (r CreateOutboundAIEvaluationRequest) Normalized() CreateOutboundAIEvaluationRequest {
	r.JudgeIntegrationID = strings.TrimSpace(r.JudgeIntegrationID)
	r.JudgeModel = strings.TrimSpace(r.JudgeModel)
	r.SampleSize = intOrDefault(r.SampleSize, OutboundAIEvaluationSampleDefault)
	r.MaxLossRate = intOrDefault(r.MaxLossRate, OutboundAIEvaluationMaxLossRateDefault)
	if r.Canary != nil {
		canary := r.Canary.Normalized()
		r.Canary = &canary
	}
	return r
}

// Valid checks a normalized request's ranges. The target and the judge
// integration must also exist, which only the stored route and account can
// tell.
func (r CreateOutboundAIEvaluationRequest) Valid() bool {
	r = r.Normalized()
	return r.Target >= 2 && r.Target <= OutboundAIRouteTargetsMax && r.JudgeIntegrationID != "" &&
		outboundAIEvaluationModel.MatchString(r.JudgeModel) &&
		*r.SampleSize >= 1 && *r.SampleSize <= OutboundAIEvaluationSampleMax &&
		*r.MaxLossRate >= 0 && *r.MaxLossRate <= 100 &&
		(r.Canary == nil || (r.Canary.Valid() && r.Canary.Target == r.Target))
}

// OutboundAIEvaluationTargetStats is one side's total cost over the sample
// and mean time to first byte over the calls that reported one.
type OutboundAIEvaluationTargetStats struct {
	CostMillicents int64  `json:"cost_millicents"`
	MeanTTFBMS     *int64 `json:"mean_ttfb_ms"`
}

// OutboundAIEvaluation is an evaluation of a route target (ADR-1431). The
// baseline is the route's first target as captured; the candidate is the
// evaluated target. Verdict is set when State is done, ErrorCode when it is
// failed. Items are returned only when one evaluation is read.
type OutboundAIEvaluation struct {
	ID                 string                           `json:"id"`
	RouteID            string                           `json:"route_id"`
	Target             int                              `json:"target"`
	JudgeIntegrationID string                           `json:"judge_integration_id"`
	JudgeModel         string                           `json:"judge_model"`
	SampleSize         int                              `json:"sample_size"`
	MaxLossRate        int                              `json:"max_loss_rate"`
	Canary             *PutOutboundAIRouteCanaryRequest `json:"canary"`
	State              string                           `json:"state"`
	Verdict            *string                          `json:"verdict"`
	ErrorCode          *string                          `json:"error_code"`
	Samples            int                              `json:"samples"`
	Wins               int                              `json:"wins"`
	Ties               int                              `json:"ties"`
	Losses             int                              `json:"losses"`
	CandidateErrors    int                              `json:"candidate_errors"`
	JudgeErrors        int                              `json:"judge_errors"`
	Baseline           OutboundAIEvaluationTargetStats  `json:"baseline"`
	Candidate          OutboundAIEvaluationTargetStats  `json:"candidate"`
	CanaryStarted      bool                             `json:"canary_started"`
	CreatedAt          time.Time                        `json:"created_at"`
	StartedAt          *time.Time                       `json:"started_at"`
	FinishedAt         *time.Time                       `json:"finished_at"`
	Items              []OutboundAIEvaluationItem       `json:"items,omitempty"`
}

// OutboundAIEvaluationItem is one sample: a captured call of the first
// target and the candidate's replay of it. Output texts are redacted and
// cut to OutboundAIReplayOutputTextMaxBytes.
type OutboundAIEvaluationItem struct {
	LogID                   string `json:"log_id"`
	Verdict                 string `json:"verdict"`
	CandidateStatus         *int   `json:"candidate_status"`
	CandidateCostMillicents int64  `json:"candidate_cost_millicents"`
	CandidateTTFBMS         *int64 `json:"candidate_ttfb_ms"`
	BaselineCostMillicents  int64  `json:"baseline_cost_millicents"`
	BaselineTTFBMS          *int64 `json:"baseline_ttfb_ms"`
	BaselineOutputText      string `json:"baseline_output_text"`
	CandidateOutputText     string `json:"candidate_output_text"`
}

// OutboundAIEvaluationList is a route's evaluations, newest first.
type OutboundAIEvaluationList struct {
	Items []OutboundAIEvaluation `json:"items"`
}

// ErrOutboundAIEvaluationLimit is the problem for a route that reached its
// evaluations for the UTC day.
func ErrOutboundAIEvaluationLimit(observed int64) *Problem {
	return NewProblem(http.StatusTooManyRequests, "outbound_ai_evaluation_limit", "AI evaluation limit reached",
		"A route can run at most 20 evaluations per UTC day.").
		WithLimit(OutboundAIEvaluationsPerDay, observed).
		WithDocs(docsBase + "/cli#outbound")
}
