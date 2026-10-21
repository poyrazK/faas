package api

import "regexp"

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
