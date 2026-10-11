package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"strings"
	"text/tabwriter"

	"github.com/onebox-faas/faas/pkg/api"
)

const aiPricesUsage = "usage: gregale ai prices <integration-id> | gregale ai prices set <integration-id> --price model=PREFIX,input=EUR,output=EUR[,cached=EUR][,cache-write=EUR] ..."

// cmdAIPrices shows or replaces a price list. Prices are EUR per million
// tokens; "model=*" prices any model without a longer matching prefix.
func cmdAIPrices(args []string) int {
	if len(args) > 0 && args[0] == "set" {
		return cmdAIPricesSet(args[1:])
	}
	fs := newFlagSet("ai prices", flag.ContinueOnError)
	if err := parseInterspersed(fs, args); err != nil || fs.NArg() != 1 {
		return printErr("Invalid ai prices arguments", errors.New(aiPricesUsage))
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	list, err := client.GetOutboundIntegrationAIPrices(context.Background(), fs.Arg(0))
	if err != nil {
		return printErr("Could not read AI prices", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(list))
	}
	if list.EffectiveFrom == nil {
		_, _ = fmt.Fprintln(osStdout, "(no price list; cost is zero)")
		return 0
	}
	_, _ = fmt.Fprintf(osStdout, "Effective from %s (EUR per million tokens)\n", list.EffectiveFrom.Format("2006-01-02 15:04 UTC"))
	tw := tabwriter.NewWriter(osStdout, 0, 0, 2, ' ', 0)
	_, _ = fmt.Fprintln(tw, "MODEL PREFIX\tINPUT\tCACHED INPUT\tCACHE WRITE\tOUTPUT")
	for _, entry := range list.Entries {
		_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n", entry.ModelPrefix, financialMoney(entry.InputMillicentsPerMTok),
			formatOptionalPrice(entry.CachedInputMillicentsPerMTok), formatOptionalPrice(entry.CacheWriteMillicentsPerMTok), financialMoney(entry.OutputMillicentsPerMTok))
	}
	_ = tw.Flush()
	return 0
}

func formatOptionalPrice(value *int64) string {
	if value == nil {
		return "= input"
	}
	return financialMoney(*value)
}

func cmdAIPricesSet(args []string) int {
	fs := newFlagSet("ai prices set", flag.ContinueOnError)
	var prices stringListFlag
	fs.Var(&prices, "price", "model=PREFIX,input=EUR,output=EUR[,cached=EUR][,cache-write=EUR] per million tokens (repeatable; none clears)")
	if err := parseInterspersed(fs, args); err != nil || fs.NArg() != 1 || len(prices) == 0 {
		return printErr("Invalid ai prices set arguments", errors.New(aiPricesUsage))
	}
	entries := make([]api.OutboundAIPriceEntry, 0, len(prices))
	for _, spec := range prices {
		if spec == "none" {
			continue
		}
		entry, err := parseAIPriceSpec(spec)
		if err != nil {
			return printErr("Invalid --price", err)
		}
		entries = append(entries, entry)
	}
	if !api.ValidOutboundAIPriceEntries(entries) {
		return printErr("Invalid price list", errors.New("model prefixes must be unique and printable; at most 64 entries; prices up to EUR 10,000 per million tokens"))
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	if err := client.SetOutboundIntegrationAIPrices(context.Background(), fs.Arg(0), entries); err != nil {
		return printErr("Could not replace AI prices", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(api.PutOutboundAIPricesRequest{Entries: entries}))
	}
	_, _ = fmt.Fprintf(osStdout, "Price list replaced for %s (%d entries, effective now)\n", fs.Arg(0), len(entries))
	return 0
}

// parseAIPriceSpec reads model=...,input=...,output=...[,cached=...][,cache-write=...].
func parseAIPriceSpec(spec string) (api.OutboundAIPriceEntry, error) {
	var entry api.OutboundAIPriceEntry
	seen := map[string]bool{}
	for _, part := range strings.Split(spec, ",") {
		key, value, ok := strings.Cut(strings.TrimSpace(part), "=")
		if !ok || seen[key] {
			return entry, fmt.Errorf("%q: expected unique key=value pairs", spec)
		}
		seen[key] = true
		if key == "model" {
			entry.ModelPrefix = value
			continue
		}
		millicents, err := parseEuroMillicents(value)
		if err != nil {
			return entry, err
		}
		switch key {
		case "input":
			entry.InputMillicentsPerMTok = millicents
		case "output":
			entry.OutputMillicentsPerMTok = millicents
		case "cached":
			entry.CachedInputMillicentsPerMTok = &millicents
		case "cache-write":
			entry.CacheWriteMillicentsPerMTok = &millicents
		default:
			return entry, fmt.Errorf("%q: unknown key %q", spec, key)
		}
	}
	if !seen["model"] || !seen["input"] || !seen["output"] {
		return entry, fmt.Errorf("%q: model, input and output are required", spec)
	}
	return entry, nil
}

const aiRoutesUsage = "usage: gregale ai routes <list|create|set|rm> ... (targets as <integration-id>[:<model>])"

func cmdAIRoutes(args []string) int {
	if len(args) == 0 {
		return printErr("Invalid ai routes arguments", errors.New(aiRoutesUsage))
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	switch args[0] {
	case "list":
		return cmdAIRoutesList(client)
	case "create":
		return cmdAIRoutesCreate(client, args[1:])
	case "set":
		return cmdAIRoutesSet(client, args[1:])
	case "rm":
		if len(args) != 2 {
			return printErr("Invalid ai routes rm arguments", errors.New("usage: gregale ai routes rm <route-id>"))
		}
		if err := client.DeleteOutboundAIRoute(context.Background(), args[1]); err != nil {
			return printErr("Could not delete AI route", err)
		}
		_, _ = fmt.Fprintf(osStdout, "AI route %s deleted\n", args[1])
		return 0
	}
	return printErr("Invalid ai routes subcommand", errors.New(aiRoutesUsage))
}

func cmdAIRoutesList(client *api.Client) int {
	routes, err := client.ListOutboundAIRoutes(context.Background())
	if err != nil {
		return printErr("Could not list AI routes", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(routes))
	}
	if len(routes.Items) == 0 {
		_, _ = fmt.Fprintln(osStdout, "(no AI routes)")
		return 0
	}
	for _, route := range routes.Items {
		targets := make([]string, 0, len(route.Targets))
		for _, target := range route.Targets {
			targets = append(targets, formatAIRouteTarget(target))
		}
		_, _ = fmt.Fprintf(osStdout, "%s  %s  %s\n  call: $GREGALE_OUTBOUND_URL/r/%s/<provider path>\n", route.ID, route.Name, strings.Join(targets, " -> "), route.ID)
	}
	return 0
}

func formatAIRouteTarget(target api.OutboundAIRouteTarget) string {
	if target.Model == "" {
		return target.IntegrationID
	}
	return target.IntegrationID + ":" + target.Model
}

func parseAIRouteTargets(values []string) ([]api.OutboundAIRouteTarget, error) {
	targets := make([]api.OutboundAIRouteTarget, 0, len(values))
	for _, value := range values {
		id, model, _ := strings.Cut(value, ":")
		targets = append(targets, api.OutboundAIRouteTarget{IntegrationID: id, Model: model})
	}
	if !api.ValidOutboundAIRouteTargetShape(targets) {
		return nil, errors.New("give 1 to 5 --target <integration-id>[:<model>] values")
	}
	return targets, nil
}

func cmdAIRoutesCreate(client *api.Client, args []string) int {
	fs := newFlagSet("ai routes create", flag.ContinueOnError)
	name := fs.String("name", "", "route name, a DNS label (required)")
	var targetValues stringListFlag
	fs.Var(&targetValues, "target", "<integration-id>[:<model>] in fallback order (repeatable, required)")
	if err := parseInterspersed(fs, args); err != nil || fs.NArg() != 0 || !api.ValidOutboundAIRouteName(*name) {
		return printErr("Invalid ai routes create arguments", errors.New("usage: gregale ai routes create --name <name> --target <integration-id>[:<model>] ..."))
	}
	targets, err := parseAIRouteTargets(targetValues)
	if err != nil {
		return printErr("Invalid --target", err)
	}
	route, err := client.CreateOutboundAIRoute(context.Background(), api.CreateOutboundAIRouteRequest{Name: *name, Targets: targets})
	if err != nil {
		return printErr("Could not create AI route", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(route))
	}
	_, _ = fmt.Fprintf(osStdout, "AI route %s created: call $GREGALE_OUTBOUND_URL/r/%s/<provider path>\n", route.Name, route.ID)
	return 0
}

func cmdAIRoutesSet(client *api.Client, args []string) int {
	fs := newFlagSet("ai routes set", flag.ContinueOnError)
	var targetValues stringListFlag
	fs.Var(&targetValues, "target", "<integration-id>[:<model>] in fallback order (repeatable, required)")
	if err := parseInterspersed(fs, args); err != nil || fs.NArg() != 1 {
		return printErr("Invalid ai routes set arguments", errors.New("usage: gregale ai routes set <route-id> --target <integration-id>[:<model>] ..."))
	}
	targets, err := parseAIRouteTargets(targetValues)
	if err != nil {
		return printErr("Invalid --target", err)
	}
	if err := client.SetOutboundAIRouteTargets(context.Background(), fs.Arg(0), targets); err != nil {
		return printErr("Could not update AI route", err)
	}
	_, _ = fmt.Fprintf(osStdout, "AI route %s now tries %d target(s)\n", fs.Arg(0), len(targets))
	return 0
}
