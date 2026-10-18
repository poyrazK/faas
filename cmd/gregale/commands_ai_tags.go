// commands_ai_tags.go — `gregale ai tags` and `gregale ai tag-limits`: one
// UTC day of AI usage by X-Gregale-AI-Tags tag, and per-tag daily cost
// limits (ADR-1436).

package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"slices"
	"strings"
	"text/tabwriter"

	"github.com/onebox-faas/faas/pkg/api"
)

const aiTagsUsage = "usage: gregale ai tags <integration-id> [--date YYYY-MM-DD] [--key KEY] [--limit N] | " +
	"gregale ai tags export <integration-id> [--from YYYY-MM-DD] [--to YYYY-MM-DD] [--key KEY] [--format csv|ndjson]"

// cmdAITags lists one UTC day's tags by cost, then tokens.
func cmdAITags(args []string) int {
	if len(args) > 0 && args[0] == "export" {
		return cmdAITagsExport(args[1:])
	}
	fs := newFlagSet("ai tags", flag.ContinueOnError)
	date := fs.String("date", "", "UTC day within the last 31 days (default today)")
	key := fs.String("key", "", "show only this tag key's tags")
	limit := fs.Int("limit", 50, "tags to show, 1 to 1000")
	if err := parseInterspersed(fs, args); err != nil || fs.NArg() != 1 || *limit < 1 || *limit > api.OutboundAITagUsagePageMax {
		return printErr("Invalid ai tags arguments", errors.New(aiTagsUsage))
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	usage, err := client.GetOutboundAITagUsage(context.Background(), fs.Arg(0), *date, *key, *limit)
	if err != nil {
		return printErr("Could not read AI usage by tag", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(usage))
	}
	_, _ = fmt.Fprintf(osStdout, "Day: %s   Tags: %d\n", usage.UsageDate, usage.TrackedTags)
	tw := tabwriter.NewWriter(osStdout, 0, 0, 2, ' ', 0)
	_, _ = fmt.Fprintln(tw, "TAG\tREQUESTS\tERRORS\tINPUT\tOUTPUT\tCOST\tDAILY LIMIT")
	for _, item := range usage.Items {
		_, _ = fmt.Fprintf(tw, "%s\t%d\t%d\t%d\t%d\t%s\t%s\n", item.Tag, item.Requests, item.ErrorRequests,
			item.InputTokens, item.OutputTokens, financialMoney(item.CostMillicents), formatOptionalMoney(item.DailyCostLimitMillicents))
	}
	_ = tw.Flush()
	return 0
}

// cmdAITagsExport writes a day range of usage by tag to stdout, CSV by
// default, for chargeback reports (ADR-1438).
func cmdAITagsExport(args []string) int {
	fs := newFlagSet("ai tags export", flag.ContinueOnError)
	from := fs.String("from", "", "first UTC day within the last 31 days (default today)")
	to := fs.String("to", "", "last UTC day (default today)")
	key := fs.String("key", "", "export only this tag key's tags")
	format := fs.String("format", "csv", "csv or ndjson")
	if err := parseInterspersed(fs, args); err != nil || fs.NArg() != 1 || (*format != "csv" && *format != "ndjson") {
		return printErr("Invalid ai tags export arguments", errors.New(aiTagsUsage))
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	body, err := client.ExportOutboundAITagUsage(context.Background(), fs.Arg(0), *from, *to, *key, *format)
	if err != nil {
		return printErr("Could not export AI usage by tag", err)
	}
	_, _ = osStdout.Write(body)
	return 0
}

const aiTagLimitsUsage = "usage: gregale ai tag-limits <integration-id> [--set KEY=VALUE:EUR]... [--remove KEY=VALUE]... [--clear]"

// cmdAITagLimits shows or changes an integration's per-tag daily cost
// limits; flags left out keep the other limits.
func cmdAITagLimits(args []string) int {
	fs := newFlagSet("ai tag-limits", flag.ContinueOnError)
	var set, remove stringListFlag
	fs.Var(&set, "set", "cap a tag's UTC-day cost: key=value:EUR (repeatable)")
	fs.Var(&remove, "remove", "remove a tag's limit (repeatable)")
	clear := fs.Bool("clear", false, "remove every limit before applying --set")
	if err := parseInterspersed(fs, args); err != nil || fs.NArg() != 1 {
		return printErr("Invalid ai tag-limits arguments", errors.New(aiTagLimitsUsage))
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	ctx := context.Background()
	current, err := client.GetOutboundAITagLimits(ctx, fs.Arg(0))
	if err != nil {
		return printErr("Could not read the tag limits", err)
	}
	limits, err := applyTagLimitFlags(current.Limits, set, remove, *clear)
	if err != nil {
		return printErr("Invalid tag limit", err)
	}
	if len(set) > 0 || len(remove) > 0 || *clear {
		if err := client.SetOutboundAITagLimits(ctx, fs.Arg(0), limits); err != nil {
			return printErr("Could not update the tag limits", err)
		}
	}
	if jsonOutput {
		return jsonOut(writeJSON(api.OutboundAITagLimits{Limits: limits}))
	}
	if len(limits) == 0 {
		_, _ = fmt.Fprintf(osStdout, "No tag limits for %s\n", fs.Arg(0))
		return 0
	}
	tw := tabwriter.NewWriter(osStdout, 0, 0, 2, ' ', 0)
	_, _ = fmt.Fprintln(tw, "TAG\tDAILY LIMIT")
	for _, limit := range limits {
		_, _ = fmt.Fprintf(tw, "%s\t%s\n", limit.Tag, financialMoney(limit.DailyCostLimitMillicents))
	}
	_ = tw.Flush()
	return 0
}

// applyTagLimitFlags returns the limits after --clear, --remove and --set,
// checked as the API checks them.
func applyTagLimitFlags(current []api.OutboundAITagLimit, set, remove []string, clear bool) ([]api.OutboundAITagLimit, error) {
	byTag := map[string]int64{}
	if !clear {
		for _, limit := range current {
			byTag[limit.Tag] = limit.DailyCostLimitMillicents
		}
	}
	for _, tag := range remove {
		delete(byTag, tag)
	}
	for _, spec := range set {
		// A value may contain ":", an amount never does.
		cut := strings.LastIndex(spec, ":")
		if cut < 0 || !api.ValidOutboundAITag(spec[:cut]) {
			return nil, fmt.Errorf("%q is not key=value:EUR", spec)
		}
		tag, amount := spec[:cut], spec[cut+1:]
		millicents, err := parseEuroMillicents(amount)
		if err != nil || millicents < 1 {
			return nil, fmt.Errorf("%q needs a positive euro amount", spec)
		}
		byTag[tag] = millicents
	}
	limits := make([]api.OutboundAITagLimit, 0, len(byTag))
	for tag, cost := range byTag {
		limits = append(limits, api.OutboundAITagLimit{Tag: tag, DailyCostLimitMillicents: cost})
	}
	slices.SortFunc(limits, func(a, b api.OutboundAITagLimit) int { return strings.Compare(a.Tag, b.Tag) })
	if !api.ValidOutboundAITagLimits(limits) {
		return nil, fmt.Errorf("at most %d limits of at most 10,000,000 EUR each", api.OutboundAITagLimitsMax)
	}
	return limits, nil
}
