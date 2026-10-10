package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"
	"text/tabwriter"

	"github.com/onebox-faas/faas/pkg/api"
)

const routesPriorityUsage = "usage: gregale routes priority APP [--critical \"[METHOD] PATH\"]... [--bulk \"[METHOD] PATH\"]... | --clear | --reset"

// cmdRoutesPriority shows or replaces an app's route priorities (ADR-957).
func cmdRoutesPriority(args []string) int {
	flags, positional := splitArgsForFlags(args, "clear", "reset")
	fs := newFlagSet("routes priority", flag.ContinueOnError)
	var critical, bulk multiFlag
	fs.Var(&critical, "critical", "route served first when the app is saturated, as \"[METHOD] PATH\" (repeatable)")
	fs.Var(&bulk, "bulk", "route served last and turned away first, as \"[METHOD] PATH\" (repeatable)")
	clearAll := fs.Bool("clear", false, "save an empty list: no priorities, not even the route-health default")
	reset := fs.Bool("reset", false, "delete saved priorities and use the route-health default")
	if err := fs.Parse(flags); err != nil {
		return 1
	}
	rules, err := routePriorityRules(critical, bulk)
	setting := len(rules) > 0 || *clearAll
	if err != nil || len(positional) != 1 || !validCLISlug(positional[0]) || (setting && *reset) || (len(rules) > 0 && *clearAll) {
		if err == nil {
			err = errors.New(routesPriorityUsage)
		}
		return printErr("Invalid route priority command", err)
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), api.RouteCheckTimeout)
	defer cancel()
	var out api.RoutePrioritiesResponse
	switch {
	case *reset:
		out, err = client.ResetRoutePriorities(ctx, positional[0])
	case setting:
		out, err = client.SetRoutePriorities(ctx, positional[0], rules)
	default:
		out, err = client.GetRoutePriorities(ctx, positional[0])
	}
	if err != nil {
		return printErr("Could not reach route priorities", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(out))
	}
	renderRoutePriorities(osStdout, out)
	return 0
}

// routePriorityRules parses "[METHOD] PATH" flag values in the order given.
func routePriorityRules(critical, bulk []string) ([]api.RoutePriorityRule, error) {
	var rules []api.RoutePriorityRule
	for _, group := range []struct {
		class  string
		values []string
	}{{api.RoutePriorityCritical, critical}, {api.RoutePriorityBulk, bulk}} {
		for _, value := range group.values {
			fields := strings.Fields(value)
			rule := api.RoutePriorityRule{Class: group.class}
			switch len(fields) {
			case 1:
				rule.Path = fields[0]
			case 2:
				rule.Method, rule.Path = strings.ToUpper(fields[0]), fields[1]
			default:
				return nil, fmt.Errorf("--%s %q must be \"[METHOD] PATH\"", group.class, value)
			}
			rules = append(rules, rule)
		}
	}
	if err := api.ValidateRoutePriorities(rules); err != nil {
		return nil, err
	}
	return rules, nil
}

func renderRoutePriorities(w io.Writer, p api.RoutePrioritiesResponse) {
	switch p.Source {
	case api.RoutePrioritySourceConfigured:
		_, _ = fmt.Fprintf(w, "Route priorities for %s (saved)\n", p.Slug)
	case api.RoutePrioritySourceRouteHealth:
		_, _ = fmt.Fprintf(w, "Route priorities for %s (default: route-health routes are critical)\n", p.Slug)
	default:
		_, _ = fmt.Fprintf(w, "Route priorities for %s: none; every route is normal priority\n", p.Slug)
	}
	if len(p.Routes) > 0 {
		tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
		_, _ = fmt.Fprintln(tw, "CLASS\tMETHOD\tPATH")
		for _, r := range p.Routes {
			method := r.Method
			if method == "" {
				method = "any"
			}
			_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\n", r.Class, method, r.Path)
		}
		_ = tw.Flush()
	}
	_, _ = fmt.Fprintln(w, "Crawlers and link-preview bots that match no rule are bulk. Priorities apply only while every instance is busy.")
}
