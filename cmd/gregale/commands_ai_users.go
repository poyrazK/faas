// commands_ai_users.go — `gregale ai catalog` and `gregale ai users`: the
// built-in price catalog and one UTC day of AI usage by end user (ADR-992).

package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"text/tabwriter"

	"github.com/onebox-faas/faas/pkg/api"
)

const aiCatalogUsage = "usage: gregale ai catalog <openai|anthropic|gemini|azure_openai|bedrock|vertex> [--model NAME]"

// cmdAICatalog lists a provider's built-in prices, or the entry that prices
// one model. Prices are EUR per million tokens.
func cmdAICatalog(args []string) int {
	fs := newFlagSet("ai catalog", flag.ContinueOnError)
	model := fs.String("model", "", "show only the catalog entry that prices this model")
	if err := parseInterspersed(fs, args); err != nil || fs.NArg() != 1 || fs.Arg(0) == "" || !api.ValidOutboundAIProvider(fs.Arg(0)) {
		return printErr("Invalid ai catalog arguments", errors.New(aiCatalogUsage))
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	catalog, err := client.GetOutboundAIPriceCatalog(context.Background(), fs.Arg(0), *model)
	if err != nil {
		return printErr("Could not read the AI price catalog", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(catalog))
	}
	_, _ = fmt.Fprintf(osStdout, "Built-in %s prices (EUR per million tokens at %s USD per EUR; generated %s)\n",
		catalog.AIProvider, catalog.USDPerEUR, catalog.Generated)
	if len(catalog.Entries) == 0 {
		_, _ = fmt.Fprintln(osStdout, "(no catalog entry; set a price list with gregale ai prices set)")
		return 0
	}
	tw := tabwriter.NewWriter(osStdout, 0, 0, 2, ' ', 0)
	_, _ = fmt.Fprintln(tw, "MODEL\tINPUT\tCACHED INPUT\tCACHE WRITE\tOUTPUT")
	for _, entry := range catalog.Entries {
		_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n", entry.ModelPrefix, financialMoney(entry.InputMillicentsPerMTok),
			formatOptionalPrice(entry.CachedInputMillicentsPerMTok), formatOptionalPrice(entry.CacheWriteMillicentsPerMTok),
			financialMoney(entry.OutputMillicentsPerMTok))
	}
	_ = tw.Flush()
	return 0
}

const aiUsersUsage = "usage: gregale ai users <integration-id> [--date YYYY-MM-DD] [--user ID] [--limit N]"

// cmdAIUsers lists one UTC day's end users by cost, then tokens.
func cmdAIUsers(args []string) int {
	fs := newFlagSet("ai users", flag.ContinueOnError)
	date := fs.String("date", "", "UTC day within the last 31 days (default today)")
	user := fs.String("user", "", "show only this end user")
	limit := fs.Int("limit", 50, "end users to show, 1 to 1000")
	if err := parseInterspersed(fs, args); err != nil || fs.NArg() != 1 || *limit < 1 || *limit > api.OutboundAIUserUsagePageMax {
		return printErr("Invalid ai users arguments", errors.New(aiUsersUsage))
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	usage, err := client.GetOutboundAIUserUsage(context.Background(), fs.Arg(0), *date, *user, *limit)
	if err != nil {
		return printErr("Could not read AI usage by end user", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(usage))
	}
	_, _ = fmt.Fprintf(osStdout, "Day: %s   End users: %d   Per-user limits: %s tokens, %s\n", usage.UsageDate, usage.TrackedUsers,
		formatOptionalCount(usage.UserDailyTokenLimit), formatOptionalMoney(usage.UserDailyCostLimitMillicents))
	tw := tabwriter.NewWriter(osStdout, 0, 0, 2, ' ', 0)
	_, _ = fmt.Fprintln(tw, "END USER\tREQUESTS\tERRORS\tINPUT\tOUTPUT\tCOST")
	for _, item := range usage.Items {
		_, _ = fmt.Fprintf(tw, "%s\t%d\t%d\t%d\t%d\t%s\n", item.EndUser, item.Requests, item.ErrorRequests,
			item.InputTokens, item.OutputTokens, financialMoney(item.CostMillicents))
	}
	_ = tw.Flush()
	return 0
}
