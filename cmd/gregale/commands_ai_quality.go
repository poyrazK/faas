// commands_ai_quality.go — `gregale ai feedback` and `gregale ai
// quality-alerts`, the AI policy's online evaluation and drift alert
// flags, and the quality columns of `gregale ai usage`
// (ADR 20261021171712).

package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"strconv"
	"strings"
	"text/tabwriter"

	"github.com/onebox-faas/faas/pkg/api"
)

const aiFeedbackUsage = "usage: gregale ai feedback <integration-id> <call-id> --good|--bad [--label LABEL]"

// cmdAIFeedback rates one logged call by its X-Gregale-AI-Call ID.
func cmdAIFeedback(args []string) int {
	fs := newFlagSet("ai feedback", flag.ContinueOnError)
	good := fs.Bool("good", false, "rate the call good")
	bad := fs.Bool("bad", false, "rate the call bad")
	label := fs.String("label", "", "a short label such as wrong_answer")
	if err := parseInterspersed(fs, args); err != nil || fs.NArg() != 2 || *good == *bad {
		return printErr("Invalid ai feedback arguments", errors.New(aiFeedbackUsage))
	}
	rating := api.OutboundAIFeedbackGood
	if *bad {
		rating = api.OutboundAIFeedbackBad
	}
	if !api.ValidOutboundAIFeedback(rating, *label) {
		return printErr("Invalid --label", errors.New("a label is up to 32 lowercase letters, digits, _ . or - starting with a letter"))
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	if err := client.SetOutboundAIRequestFeedback(context.Background(), fs.Arg(0), fs.Arg(1),
		api.PutOutboundAIFeedbackRequest{Rating: rating, Label: *label}); err != nil {
		return printErr("Could not rate the AI call", err)
	}
	_, _ = fmt.Fprintf(osStdout, "Rated %s %s\n", fs.Arg(1), rating)
	return 0
}

const aiQualityAlertsUsage = "usage: gregale ai quality-alerts <integration-id> [--limit N]"

// cmdAIQualityAlerts lists an integration's drift alerts, newest first.
func cmdAIQualityAlerts(args []string) int {
	fs := newFlagSet("ai quality-alerts", flag.ContinueOnError)
	limit := fs.Int("limit", 50, "alerts to show, 1 to 100")
	if err := parseInterspersed(fs, args); err != nil || fs.NArg() != 1 || *limit < 1 || *limit > api.OutboundAIQualityAlertsPageMax {
		return printErr("Invalid ai quality-alerts arguments", errors.New(aiQualityAlertsUsage))
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	alerts, err := client.ListOutboundAIQualityAlerts(context.Background(), fs.Arg(0), *limit)
	if err != nil {
		return printErr("Could not list AI quality alerts", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(alerts))
	}
	tw := tabwriter.NewWriter(osStdout, 0, 0, 2, ' ', 0)
	_, _ = fmt.Fprintln(tw, "DAY\tMODEL\tMETRIC\tTODAY\tBEFORE\tCALLS\tBASELINE CALLS\tDETECTED")
	for _, alert := range alerts.Items {
		_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%d\t%d\t%s\n", alert.UsageDate, alert.Model, alert.Metric,
			formatQualityValue(alert.Metric, alert.CurrentValue), formatQualityValue(alert.Metric, alert.BaselineValue),
			alert.CurrentCount, alert.BaselineCount, alert.DetectedAt.Format("15:04Z"))
	}
	_ = tw.Flush()
	return 0
}

// formatQualityValue renders a rate in basis points as a percentage, and
// a mean score in hundredths as a score.
func formatQualityValue(metric string, value int64) string {
	if metric == api.OutboundAIQualityMetricEvaluationScore {
		return fmt.Sprintf("%d.%02d", value/100, value%100)
	}
	return fmt.Sprintf("%d.%02d%%", value/100, value%100)
}

// formatQuality lists a rollup's quality flags and ratings, or "-".
func formatQuality(item api.OutboundAIUsageItem) string {
	var parts []string
	for _, count := range []struct {
		name  string
		value int64
	}{
		{"truncated", item.TruncatedRequests}, {"refused", item.RefusedRequests}, {"empty", item.EmptyRequests},
		{"invalid_json", item.InvalidJSONRequests}, {"tool_error", item.ToolErrorRequests},
		{"good", item.GoodFeedback}, {"bad", item.BadFeedback},
	} {
		if count.value > 0 {
			parts = append(parts, count.name+"="+strconv.FormatInt(count.value, 10))
		}
	}
	if len(parts) == 0 {
		return "-"
	}
	return strings.Join(parts, ",")
}

// formatScore renders a rollup's mean evaluation score, or "-".
func formatScore(item api.OutboundAIUsageItem) string {
	if item.EvaluatedRequests == 0 {
		return "-"
	}
	return formatQualityValue(api.OutboundAIQualityMetricEvaluationScore, item.EvaluationScoreSum*100/item.EvaluatedRequests)
}

// applyQualityPolicy applies the online evaluation and quality alert
// flags to a policy request: empty keeps a setting, --online-eval-percent
// none turns evaluations off.
func applyQualityPolicy(req *api.PutOutboundAIPolicyRequest, percent, judge, model, rubric, alerts string) error {
	evaluation := req.OnlineEvaluation
	if evaluation != nil {
		copied := *evaluation
		evaluation = &copied
	}
	edited := judge != "" || model != "" || rubric != ""
	switch {
	case percent == "none":
		if edited {
			return errors.New("--online-eval-percent none turns evaluations off; drop the other --online-eval flags")
		}
		evaluation = nil
	case percent != "" || edited:
		if evaluation == nil {
			evaluation = &api.OutboundAIOnlineEvaluation{}
		}
		if percent != "" {
			value, err := strconv.Atoi(percent)
			if err != nil || value < 1 || value > 100 {
				return fmt.Errorf("--online-eval-percent %q must be 1 to 100, or none", percent)
			}
			evaluation.SamplePercent = int32(value)
		}
		if judge != "" {
			evaluation.JudgeIntegrationID = judge
		}
		if model != "" {
			evaluation.JudgeModel = model
		}
		if rubric == "none" {
			evaluation.Rubric = ""
		} else if rubric != "" {
			evaluation.Rubric = rubric
		}
	}
	req.OnlineEvaluation = evaluation
	switch alerts {
	case "":
	case "on":
		req.QualityAlerts = true
	case "off":
		req.QualityAlerts = false
	default:
		return errors.New("--quality-alerts takes on or off")
	}
	return nil
}
