// commands_ai.go — `gregale ai`: AI gateway settings, prices, per-app
// limits, usage and fallback routes on managed outbound integrations
// (ADR-988, ADR-989, ADR-990). Money is entered and shown in euros and sent
// to the API as integer millicents; no floating point is involved.

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

const aiUsageText = "usage: gregale ai <policy|content-policy|app-limit|guardrails|prices|catalog|usage|users|tags|tag-limits|routes|keys|alerts|logs|replay|evaluate|tokens> ..."

func cmdAI(args []string) int {
	parent, _ := lookupCliCommand("ai")
	if len(args) == 0 || hasHelpFlag(args[:1]) {
		PrintUsage(osStdout, aiUsageText, "ai")
		return boolToExit(len(args) == 0)
	}
	switch args[0] {
	case "policy":
		return cmdAIPolicy(args[1:])
	case "content-policy":
		return cmdAIContentPolicy(args[1:])
	case "app-limit":
		return cmdAIAppLimit(args[1:])
	case "prices":
		return cmdAIPrices(args[1:])
	case "usage":
		return cmdAIUsage(args[1:])
	case "catalog":
		return cmdAICatalog(args[1:])
	case "users":
		return cmdAIUsers(args[1:])
	case "tags":
		return cmdAITags(args[1:])
	case "tag-limits":
		return cmdAITagLimits(args[1:])
	case "routes":
		return cmdAIRoutes(args[1:])
	case "guardrails":
		return cmdAIGuardrails(args[1:])
	case "keys":
		return cmdAIKeys(args[1:])
	case "alerts":
		return cmdAIAlerts(args[1:])
	case "logs":
		return cmdAILogs(args[1:])
	case "replay":
		return cmdAIReplay(args[1:])
	case "evaluate":
		return cmdAIEvaluate(args[1:])
	case "tokens":
		return cmdAITokens(args[1:])
	}
	printCommandValidation(osStderr, "unknown ai subcommand %q\n", args[0])
	sug, _ := suggestSubcommand(args[0], parent)
	maybeSuggestSub(sug)
	return 1
}

func boolToExit(failed bool) int {
	if failed {
		return 1
	}
	return 0
}

// parseEuroMillicents reads a non-negative euro amount with at most five
// decimals ("12.5" is 1,250,000 millicents) using integer arithmetic only.
func parseEuroMillicents(value string) (int64, error) {
	whole, fraction, _ := strings.Cut(strings.TrimSpace(value), ".")
	if whole == "" && fraction == "" || len(fraction) > 5 || strings.HasPrefix(whole, "-") || strings.HasPrefix(whole, "+") {
		return 0, fmt.Errorf("%q is not a euro amount with at most 5 decimals", value)
	}
	if whole == "" {
		whole = "0"
	}
	euros, err := strconv.ParseInt(whole, 10, 64)
	if err != nil || euros > api.MaxOutboundAICostMillicents/100_000 {
		return 0, fmt.Errorf("%q is not a supported euro amount", value)
	}
	fraction += strings.Repeat("0", 5-len(fraction))
	fractionMillicents, err := strconv.ParseInt(fraction, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("%q is not a euro amount", value)
	}
	return euros*100_000 + fractionMillicents, nil
}

// optionalLimit applies a limit flag to a current value: "" keeps it,
// "none" clears it, anything else is parsed by parse.
func optionalLimit(flagValue string, current *int64, parse func(string) (int64, error)) (*int64, error) {
	switch strings.TrimSpace(flagValue) {
	case "":
		return current, nil
	case "none":
		return nil, nil
	}
	value, err := parse(flagValue)
	if err != nil {
		return nil, err
	}
	return &value, nil
}

func parseTokenCount(value string) (int64, error) {
	tokens, err := strconv.ParseInt(strings.ReplaceAll(value, "_", ""), 10, 64)
	if err != nil || tokens < 1 || tokens > api.MaxOutboundAITokensPerDay {
		return 0, fmt.Errorf("%q must be a token count from 1 to %d", value, api.MaxOutboundAITokensPerDay)
	}
	return tokens, nil
}

// findOutboundIntegration loads one integration's current settings so a
// partial `ai policy` change keeps the values it does not mention.
func findOutboundIntegration(client *api.Client, integrationID string) (api.OutboundIntegrationOffer, error) {
	offers, err := client.ListOutboundIntegrationOffers(context.Background())
	if err != nil {
		return api.OutboundIntegrationOffer{}, err
	}
	for _, offer := range offers.Items {
		if offer.ID == integrationID {
			return offer, nil
		}
	}
	return api.OutboundIntegrationOffer{}, fmt.Errorf("integration %s was not found on this account", integrationID)
}

const aiPolicyUsage = "usage: gregale ai policy <integration-id> [--provider openai|anthropic|gemini|azure_openai|bedrock|vertex|off] " +
	"[--daily-tokens N|none] [--daily-cost EUR|none] [--monthly-cost EUR|none] [--request-log on|off] " +
	"[--request-log-content on|off] [--user-daily-tokens N|none] [--user-daily-cost EUR|none] [--log-export on|off] " +
	"[--allow-unmetered on|off] [--prompt-cache on|off]"

func cmdAIPolicy(args []string) int {
	fs := newFlagSet("ai policy", flag.ContinueOnError)
	provider := fs.String("provider", "", "AI provider: openai, anthropic, gemini, azure_openai, bedrock, vertex, or off")
	requestLog := fs.String("request-log", "", "log per-call metadata for 7 days: on or off")
	requestLogContent := fs.String("request-log-content", "", "also keep the first 32 KiB of each logged call's bodies for 3 days: on or off")
	dailyTokens := fs.String("daily-tokens", "", "UTC-day input+output token limit, or none")
	dailyCost := fs.String("daily-cost", "", "UTC-day cost limit in EUR, or none")
	monthlyCost := fs.String("monthly-cost", "", "calendar-month cost limit in EUR, or none")
	userDailyTokens := fs.String("user-daily-tokens", "", "each end user's UTC-day token limit, or none")
	userDailyCost := fs.String("user-daily-cost", "", "each end user's UTC-day cost limit in EUR, or none")
	logExport := fs.String("log-export", "", "send each app call's metadata to the app's log drains: on or off")
	allowUnmetered := fs.String("allow-unmetered", "", "let through calls the gateway cannot meter, outside the caps: on or off")
	promptCache := fs.String("prompt-cache", "", "add prompt-cache breakpoints to Claude requests that carry none: on or off")
	if err := parseInterspersed(fs, args); err != nil || fs.NArg() != 1 {
		return printErr("Invalid ai policy arguments", errors.New(aiPolicyUsage))
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	current, err := findOutboundIntegration(client, fs.Arg(0))
	if err != nil {
		return printErr("Could not read integration", err)
	}
	req := api.PutOutboundAIPolicyRequest{AIProvider: current.AIProvider, DailyTokenLimit: current.AIDailyTokenLimit,
		DailyCostLimitMillicents: current.AIDailyCostLimitMillicents, MonthlyCostLimitMillicents: current.AIMonthlyCostLimitMillicents,
		RequestLogEnabled: current.AIRequestLogEnabled, UserDailyTokenLimit: current.AIUserDailyTokenLimit,
		UserDailyCostLimitMillicents: current.AIUserDailyCostLimitMillicents, RequestLogContent: current.AIRequestLogContent,
		LogExport: current.AILogExport, AllowUnmetered: current.AIAllowUnmetered, AutoPromptCache: current.AIAutoPromptCache}
	switch *provider {
	case "":
	case "off":
		req = api.PutOutboundAIPolicyRequest{}
	default:
		req.AIProvider = *provider
	}
	switch *requestLog {
	case "":
	case "on":
		req.RequestLogEnabled = true
	case "off":
		req.RequestLogEnabled, req.RequestLogContent = false, false
	default:
		return printErr("Invalid --request-log", errors.New("use on or off"))
	}
	switch *requestLogContent {
	case "":
	case "on":
		req.RequestLogContent = true
	case "off":
		req.RequestLogContent = false
	default:
		return printErr("Invalid --request-log-content", errors.New("use on or off"))
	}
	switch *logExport {
	case "":
	case "on":
		req.LogExport = true
	case "off":
		req.LogExport = false
	default:
		return printErr("Invalid --log-export", errors.New("use on or off"))
	}
	switch *allowUnmetered {
	case "":
	case "on":
		req.AllowUnmetered = true
	case "off":
		req.AllowUnmetered = false
	default:
		return printErr("Invalid --allow-unmetered", errors.New("use on or off"))
	}
	switch *promptCache {
	case "":
	case "on":
		req.AutoPromptCache = true
	case "off":
		req.AutoPromptCache = false
	default:
		return printErr("Invalid --prompt-cache", errors.New("use on or off"))
	}
	if req.DailyTokenLimit, err = optionalLimit(*dailyTokens, req.DailyTokenLimit, parseTokenCount); err == nil {
		if req.DailyCostLimitMillicents, err = optionalLimit(*dailyCost, req.DailyCostLimitMillicents, parseEuroMillicents); err == nil {
			req.MonthlyCostLimitMillicents, err = optionalLimit(*monthlyCost, req.MonthlyCostLimitMillicents, parseEuroMillicents)
		}
	}
	if err == nil {
		if req.UserDailyTokenLimit, err = optionalLimit(*userDailyTokens, req.UserDailyTokenLimit, parseTokenCount); err == nil {
			req.UserDailyCostLimitMillicents, err = optionalLimit(*userDailyCost, req.UserDailyCostLimitMillicents, parseEuroMillicents)
		}
	}
	if err != nil || !req.Valid() {
		return printErr("Invalid AI policy", errors.Join(err, errors.New("limits, log export, allow-unmetered and prompt-cache need a provider; tokens 1..1e11, costs up to EUR 10,000,000; content capture needs the request log")))
	}
	if err := client.SetOutboundIntegrationAIPolicy(context.Background(), fs.Arg(0), req); err != nil {
		return printErr("Could not update AI policy", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(req))
	}
	_, _ = fmt.Fprintf(osStdout, "AI policy updated for %s (provider=%s)\n", fs.Arg(0), displayProvider(req.AIProvider))
	return 0
}

// formatSaved renders what caching saved, which prompt-cache writes can
// make negative (ADR-1440).
func formatSaved(millicents int64) string {
	if millicents < 0 {
		return "-" + financialMoney(-millicents)
	}
	return financialMoney(millicents)
}

// formatMediaUsage lists a rollup's images, audio seconds and characters
// (ADR-997), video seconds (ADR-1432), built-in tool uses (ADR-1433) and
// audio tokens (ADR 20261019072722), or "-".
func formatMediaUsage(item api.OutboundAIUsageItem) string {
	var parts []string
	for _, count := range []struct {
		value int64
		unit  string
	}{{item.Images, "images"}, {item.AudioSeconds, "audio s"}, {item.Characters, "chars"}, {item.VideoSeconds, "video s"},
		{item.WebSearches, "web searches"}, {item.FileSearches, "file searches"}, {item.CodeSessionGB, "GB code sessions"},
		{item.InputAudioTokens, "audio in tokens"}, {item.OutputAudioTokens, "audio out tokens"}} {
		if count.value > 0 {
			parts = append(parts, strconv.FormatInt(count.value, 10)+" "+count.unit)
		}
	}
	if len(parts) == 0 {
		return "-"
	}
	return strings.Join(parts, ", ")
}

func displayProvider(provider string) string {
	if provider == "" {
		return "off"
	}
	return provider
}

const aiAppLimitUsage = "usage: gregale ai app-limit <integration-id> --app <slug> --daily-tokens N|none"

func cmdAIAppLimit(args []string) int {
	fs := newFlagSet("ai app-limit", flag.ContinueOnError)
	slug := fs.String("app", "", "bound app slug (required)")
	dailyTokens := fs.String("daily-tokens", "", "the app's own UTC-day token limit, or none (required)")
	if err := parseInterspersed(fs, args); err != nil || fs.NArg() != 1 || *slug == "" || *dailyTokens == "" {
		return printErr("Invalid ai app-limit arguments", errors.New(aiAppLimitUsage))
	}
	limit, err := optionalLimit(*dailyTokens, nil, parseTokenCount)
	if err != nil {
		return printErr("Invalid --daily-tokens", err)
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	if err := client.SetOutboundBindingAIBudget(context.Background(), *slug, fs.Arg(0), limit); err != nil {
		return printErr("Could not update the app's AI token limit", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(api.PutOutboundBindingAIBudgetRequest{DailyTokenLimit: limit}))
	}
	_, _ = fmt.Fprintf(osStdout, "AI token limit for %s on %s: %s\n", *slug, fs.Arg(0), formatOptionalCount(limit))
	return 0
}

func formatOptionalCount(value *int64) string {
	if value == nil {
		return "none"
	}
	return strconv.FormatInt(*value, 10)
}

func formatOptionalMoney(value *int64) string {
	if value == nil {
		return "none"
	}
	return financialMoney(*value)
}

const aiUsageCommandText = "usage: gregale ai usage <integration-id> [--date YYYY-MM-DD]"

func cmdAIUsage(args []string) int {
	fs := newFlagSet("ai usage", flag.ContinueOnError)
	date := fs.String("date", "", "UTC day within the last 90 days (default today)")
	if err := parseInterspersed(fs, args); err != nil || fs.NArg() != 1 {
		return printErr("Invalid ai usage arguments", errors.New(aiUsageCommandText))
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	usage, err := client.GetOutboundIntegrationAIUsage(context.Background(), fs.Arg(0), *date)
	if err != nil {
		return printErr("Could not read AI usage", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(usage))
	}
	_, _ = fmt.Fprintf(osStdout, "Provider: %s   Day: %s   Tokens: %d / %s\n", displayProvider(usage.AIProvider), usage.UsageDate,
		usage.DailyTokenCount, formatOptionalCount(usage.DailyTokenLimit))
	_, _ = fmt.Fprintf(osStdout, "Cost today: %s / %s   %s month to date: %s / %s\n", financialMoney(usage.DailyCostMillicents),
		formatOptionalMoney(usage.DailyCostLimitMillicents), usage.Month, financialMoney(usage.MonthlyCostMillicents), formatOptionalMoney(usage.MonthlyCostLimitMillicents))
	tw := tabwriter.NewWriter(osStdout, 0, 0, 2, ' ', 0)
	_, _ = fmt.Fprintln(tw, "CALLER\tMODEL\tREQUESTS\tCACHE HITS\tERRORS\tINPUT\tCACHED\tOUTPUT\tMEDIA\tCOST\tSAVED")
	var saved int64
	for _, item := range usage.Items {
		itemSaved := item.CacheSavedMillicents + item.PromptCacheSavedMillicents
		saved += itemSaved
		_, _ = fmt.Fprintf(tw, "%s\t%s\t%d\t%d\t%d\t%d\t%d\t%d\t%s\t%s\t%s\n", item.Caller, item.Model, item.Requests, item.CachedRequests,
			item.ErrorRequests, item.InputTokens, item.CachedInputTokens, item.OutputTokens, formatMediaUsage(item),
			financialMoney(item.CostMillicents), formatSaved(itemSaved))
	}
	_ = tw.Flush()
	if saved != 0 {
		_, _ = fmt.Fprintf(osStdout, "Saved today by the response cache and prompt caching: %s\n", formatSaved(saved))
	}
	return 0
}
