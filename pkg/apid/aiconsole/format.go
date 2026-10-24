package aiconsole

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

const dayLayout = "2006-01-02"

// money renders millicents as the dashboard does elsewhere.
func money(millicents int64) string {
	const perUnit = 100 * api.MillicentsPerCent
	sign := ""
	if millicents < 0 {
		sign, millicents = "-", -millicents
	}
	return fmt.Sprintf("%s %s%d.%05d", api.BillingCurrency, sign, millicents/perUnit, millicents%perUnit)
}

// optionalMoney renders a limit, or "none".
func optionalMoney(millicents *int64) string {
	if millicents == nil {
		return "none"
	}
	return money(*millicents)
}

// count renders an integer with thousands separators.
func count(n int64) string {
	digits := strconv.FormatInt(n, 10)
	negative := strings.HasPrefix(digits, "-")
	digits = strings.TrimPrefix(digits, "-")
	var b strings.Builder
	for index, digit := range digits {
		if index > 0 && (len(digits)-index)%3 == 0 {
			b.WriteByte(',')
		}
		b.WriteRune(digit)
	}
	if negative {
		return "-" + b.String()
	}
	return b.String()
}

// percent renders part of whole, or "—" for an empty whole.
func percent(part, whole int64) string {
	if whole <= 0 {
		return "—"
	}
	return strconv.FormatFloat(float64(part)*100/float64(whole), 'f', 1, 64) + "%"
}

// share is part's share of whole in whole percent, for a bar.
func share(part, whole int64) int {
	if whole <= 0 || part <= 0 {
		return 0
	}
	return int(min(part*100/whole, 100))
}

// milliseconds renders a duration in milliseconds, or "—".
func milliseconds(ms *int64) string {
	if ms == nil {
		return "—"
	}
	return duration(*ms)
}

func duration(ms int64) string {
	if ms < 1_000 {
		return strconv.FormatInt(ms, 10) + " ms"
	}
	return strconv.FormatFloat(float64(ms)/1_000, 'f', 1, 64) + " s"
}

func timestamp(t time.Time) string {
	return t.UTC().Format("2006-01-02 15:04:05Z")
}

// sumUsage adds a day's rollup rows together.
func sumUsage(items []state.OutboundAIUsageItem) state.OutboundAIUsageItem {
	var total state.OutboundAIUsageItem
	for _, item := range items {
		addUsage(&total, item)
	}
	return total
}

func addUsage(total *state.OutboundAIUsageItem, item state.OutboundAIUsageItem) {
	total.Requests += item.Requests
	total.ErrorRequests += item.ErrorRequests
	total.InputTokens += item.InputTokens
	total.OutputTokens += item.OutputTokens
	total.CostMillicents += item.CostMillicents
	total.CacheSavedMillicents += item.CacheSavedMillicents
	total.PromptCacheSavedMillicents += item.PromptCacheSavedMillicents
	total.TruncatedRequests += item.TruncatedRequests
	total.RefusedRequests += item.RefusedRequests
	total.EmptyRequests += item.EmptyRequests
	total.InvalidJSONRequests += item.InvalidJSONRequests
	total.ToolErrorRequests += item.ToolErrorRequests
	total.GoodFeedback += item.GoodFeedback
	total.BadFeedback += item.BadFeedback
	total.EvaluatedRequests += item.EvaluatedRequests
	total.EvaluationScoreSum += item.EvaluationScoreSum
	total.EvaluationErrors += item.EvaluationErrors
}

func flagTotal(item state.OutboundAIUsageItem) int64 {
	return item.TruncatedRequests + item.RefusedRequests + item.EmptyRequests + item.InvalidJSONRequests + item.ToolErrorRequests
}

// qualityFlags lists a rollup's flag counts, or "—".
func qualityFlags(item state.OutboundAIUsageItem) string {
	var parts []string
	for _, flag := range []struct {
		name  string
		value int64
	}{
		{"truncated", item.TruncatedRequests}, {"refused", item.RefusedRequests}, {"empty", item.EmptyRequests},
		{"invalid JSON", item.InvalidJSONRequests}, {"tool error", item.ToolErrorRequests},
	} {
		if flag.value > 0 {
			parts = append(parts, flag.name+" "+count(flag.value))
		}
	}
	if len(parts) == 0 {
		return "—"
	}
	return strings.Join(parts, ", ")
}

// ratings renders good and bad ratings, or "—".
func ratings(item state.OutboundAIUsageItem) string {
	if item.GoodFeedback == 0 && item.BadFeedback == 0 {
		return "—"
	}
	return count(item.GoodFeedback) + " good / " + count(item.BadFeedback) + " bad"
}

// meanScore renders the mean online-evaluation score, or "—".
func meanScore(item state.OutboundAIUsageItem) string {
	if item.EvaluatedRequests == 0 {
		return "—"
	}
	return hundredths(item.EvaluationScoreSum * 100 / item.EvaluatedRequests)
}

func hundredths(value int64) string {
	return fmt.Sprintf("%d.%02d", value/100, value%100)
}

// callerName names a rollup caller: an app's slug, or the gateway's own
// callers.
func callerName(caller string, apps map[string]string) string {
	if slug, ok := apps[caller]; ok {
		return slug
	}
	switch caller {
	case "runs":
		return "Runs"
	case "replay":
		return "Replays"
	case "moderation":
		return "Moderation checks"
	case "evaluation":
		return "Evaluations"
	}
	return caller
}

// qualityValue renders a drift alert's value: a rate in basis points, or
// a score in hundredths.
func qualityValue(metric string, value int64) string {
	if metric == api.OutboundAIQualityMetricEvaluationScore {
		return hundredths(value)
	}
	return hundredths(value) + "%"
}

// metricName is a drift metric's label.
func metricName(metric string) string {
	switch metric {
	case api.OutboundAIQualityInvalidJSON:
		return "invalid JSON"
	case api.OutboundAIQualityToolError:
		return "tool errors"
	case api.OutboundAIQualityMetricBadFeedback:
		return "bad ratings"
	case api.OutboundAIQualityMetricEvaluationScore:
		return "mean score"
	}
	return metric
}
