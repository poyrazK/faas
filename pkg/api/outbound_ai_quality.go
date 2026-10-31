package api

import (
	"regexp"
	"time"
)

// Quality signals (ADR 20261021171712): every AI call's ID, its quality
// flags, feedback on it, online evaluations and drift alerts.

// OutboundAICallHeader carries an AI call's ID: its request-log entry ID,
// which feedback names.
const OutboundAICallHeader = "X-Gregale-AI-Call"

// Quality flags a call can carry, in the order calls report them.
const (
	OutboundAIQualityTruncated   = "truncated"
	OutboundAIQualityRefused     = "refused"
	OutboundAIQualityEmpty       = "empty"
	OutboundAIQualityInvalidJSON = "invalid_json"
	OutboundAIQualityToolError   = "tool_error"
)

// Feedback ratings.
const (
	OutboundAIFeedbackGood = "good"
	OutboundAIFeedbackBad  = "bad"
)

// Drift alert metrics: the five flag rates, the share of bad ratings and
// the mean evaluation score.
const (
	OutboundAIQualityMetricBadFeedback     = "bad_feedback"
	OutboundAIQualityMetricEvaluationScore = "evaluation_score"
)

// OutboundAIQualityMetrics lists every drift alert metric.
var OutboundAIQualityMetrics = []string{
	OutboundAIQualityTruncated, OutboundAIQualityRefused, OutboundAIQualityEmpty, OutboundAIQualityInvalidJSON,
	OutboundAIQualityToolError, OutboundAIQualityMetricBadFeedback, OutboundAIQualityMetricEvaluationScore,
}

var feedbackLabelPattern = regexp.MustCompile(`^[a-z][a-z0-9_.-]{0,31}$`)

// ValidOutboundAIFeedback reports a rating, and a label that is empty or
// up to OutboundAIFeedbackLabelMaxBytes lowercase letters, digits, _, . or
// -, starting with a letter.
func ValidOutboundAIFeedback(rating, label string) bool {
	return (rating == OutboundAIFeedbackGood || rating == OutboundAIFeedbackBad) &&
		(label == "" || (len(label) <= OutboundAIFeedbackLabelMaxBytes && feedbackLabelPattern.MatchString(label)))
}

// ValidOutboundAIOnlineEvaluation reports online-evaluation settings that
// can be stored: all absent, or a sample of 1 to 100 percent, a judge
// integration and model, and an optional rubric, on an AI integration.
func ValidOutboundAIOnlineEvaluation(provider string, percent *int32, judgeIntegrationID, judgeModel, rubric string) bool {
	if percent == nil {
		return judgeIntegrationID == "" && judgeModel == "" && rubric == ""
	}
	return provider != "" && *percent >= 1 && *percent <= 100 && judgeIntegrationID != "" &&
		len(judgeModel) >= 1 && len(judgeModel) <= 200 && len(rubric) <= OutboundAIOnlineEvaluationRubricMaxBytes
}

// OutboundAIQualityAlert is one drift the check found (ADR 20261021171712):
// a model's metric today against its previous days. Rates are basis points
// of answered calls (of rated calls for bad_feedback) and evaluation_score
// is the mean score in hundredths; the counts are those denominators.
type OutboundAIQualityAlert struct {
	ID            string    `json:"id"`
	IntegrationID string    `json:"integration_id"`
	UsageDate     string    `json:"usage_date"`
	Model         string    `json:"model"`
	Metric        string    `json:"metric"`
	CurrentValue  int64     `json:"current_value"`
	BaselineValue int64     `json:"baseline_value"`
	CurrentCount  int64     `json:"current_count"`
	BaselineCount int64     `json:"baseline_count"`
	DetectedAt    time.Time `json:"detected_at"`
}

// OutboundAIQualityAlertList is a page of drift alerts, newest first.
type OutboundAIQualityAlertList struct {
	Items []OutboundAIQualityAlert `json:"items"`
}

// OutboundAIQualityDriftWebhookPayload is the data of an
// outbound.ai_quality.drift webhook. AlertID is stable across retries.
type OutboundAIQualityDriftWebhookPayload struct {
	AlertID         string    `json:"alert_id"`
	IntegrationID   string    `json:"integration_id"`
	IntegrationName string    `json:"integration_name"`
	UsageDate       string    `json:"usage_date"`
	Model           string    `json:"model"`
	Metric          string    `json:"metric"`
	CurrentValue    int64     `json:"current_value"`
	BaselineValue   int64     `json:"baseline_value"`
	CurrentCount    int64     `json:"current_count"`
	BaselineCount   int64     `json:"baseline_count"`
	DetectedAt      time.Time `json:"detected_at"`
}

// OutboundAIOnlineEvaluation samples answered app calls for a judge model:
// SamplePercent of them, scored from 1 to 5 by JudgeModel through
// JudgeIntegrationID, one of the account's AI integrations. Rubric, when
// set, tells the judge what a good answer does for the app.
type OutboundAIOnlineEvaluation struct {
	SamplePercent      int32  `json:"sample_percent"`
	JudgeIntegrationID string `json:"judge_integration_id"`
	JudgeModel         string `json:"judge_model"`
	Rubric             string `json:"rubric,omitempty"`
}

// valid reports absent settings, or settings that can be stored on an AI
// integration that captures content.
func (e *OutboundAIOnlineEvaluation) valid(provider string, captures bool) bool {
	if e == nil {
		return true
	}
	percent := e.SamplePercent
	return captures && ValidOutboundAIOnlineEvaluation(provider, &percent, e.JudgeIntegrationID, e.JudgeModel, e.Rubric)
}

// PutOutboundAIFeedbackRequest rates one logged call (ADR 20261021171712).
type PutOutboundAIFeedbackRequest struct {
	Rating string `json:"rating"`
	Label  string `json:"label,omitempty"`
}
