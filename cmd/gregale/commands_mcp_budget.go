package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"strconv"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
)

const mcpBudgetUsage = "usage: gregale mcp connection budget <id> | --self [--set USD | --unlimited | --default] [--json]"

// cmdMCPConnectionBudget shows or sets an agent's monthly budget for
// always-on capacity (ADR-20261111114059).
func cmdMCPConnectionBudget(args []string) int {
	fs := newFlagSet("mcp-connections-budget", flag.ContinueOnError)
	self := fs.Bool("self", false, "the calling agent's own budget (with its agent key)")
	set := fs.String("set", "", "monthly budget in US dollars, for example 25 or 12.50")
	unlimited := fs.Bool("unlimited", false, "no budget of its own; the organization's ceiling still applies")
	def := fs.Bool("default", false, "return to the plan's default budget")
	flags, positional := splitArgsForFlags(args, "self", "unlimited", "default")
	if err := fs.Parse(flags); err != nil {
		return 1
	}
	id := ""
	switch {
	case *self && len(positional) != 0:
		return printErr("Agent budget", errors.New("--self takes no id; "+mcpBudgetUsage))
	case *self:
		id = "self"
	case len(positional) == 1:
		id = positional[0]
	default:
		return printErr("Agent budget", errors.New("name one connection id, or --self; "+mcpBudgetUsage))
	}
	req, change, err := budgetChange(*set, *unlimited, *def)
	if err != nil {
		return printErr("Agent budget", err)
	}
	if change && *self {
		return printErr("Agent budget", errors.New("an agent cannot change its own budget; a person runs this with the connection id"))
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	var out api.AgentBudgetResponse
	if change {
		out, err = client.SetAgentConnectionBudget(context.Background(), id, req)
	} else {
		out, err = client.AgentConnectionBudget(context.Background(), id)
	}
	if err != nil {
		return printErr("Could not read the agent's budget", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(out))
	}
	printAgentBudget(out)
	return 0
}

// budgetChange turns the flags into a request; change is false when none
// was given.
func budgetChange(set string, unlimited, def bool) (api.AgentBudgetRequest, bool, error) {
	n := 0
	for _, on := range []bool{set != "", unlimited, def} {
		if on {
			n++
		}
	}
	switch {
	case n == 0:
		return api.AgentBudgetRequest{}, false, nil
	case n > 1:
		return api.AgentBudgetRequest{}, false, errors.New("use one of --set, --unlimited or --default")
	case unlimited:
		return api.AgentBudgetRequest{Unlimited: true}, true, nil
	case def:
		return api.AgentBudgetRequest{Default: true}, true, nil
	}
	cents, err := parseUSDCents(set)
	if err != nil {
		return api.AgentBudgetRequest{}, false, err
	}
	return api.AgentBudgetRequest{MonthlyCents: &cents}, true, nil
}

// parseUSDCents reads "25", "25.5" or "25.50" (an optional leading $) as
// cents.
func parseUSDCents(value string) (int64, error) {
	value = strings.TrimPrefix(strings.TrimSpace(value), "$")
	whole, frac, hasFrac := strings.Cut(value, ".")
	bad := fmt.Errorf("--set takes a dollar amount such as 25 or 12.50, got %q", value)
	if whole == "" || (hasFrac && (frac == "" || len(frac) > 2)) {
		return 0, bad
	}
	dollars, err := strconv.ParseInt(whole, 10, 64)
	if err != nil || dollars < 0 || dollars > api.MaxAgentBudgetCents/100 {
		return 0, bad
	}
	cents := int64(0)
	if hasFrac {
		if len(frac) == 1 {
			frac += "0"
		}
		if cents, err = strconv.ParseInt(frac, 10, 64); err != nil || cents < 0 {
			return 0, bad
		}
	}
	return dollars*100 + cents, nil
}

func printAgentBudget(out api.AgentBudgetResponse) {
	budget := "unlimited"
	if out.MonthlyCents != nil {
		budget = dollars(*out.MonthlyCents) + " a month"
	}
	source := map[string]string{
		api.AgentBudgetSourceKey:         "set for this agent",
		api.AgentBudgetSourcePlanDefault: "the plan's default",
		api.AgentBudgetSourceOrgCeiling:  "the organization's ceiling",
		api.AgentBudgetSourceUnlimited:   "no budget set",
	}[out.Source]
	_, _ = fmt.Fprintf(osStdout, "Budget:  %s (%s)\n", budget, source)
	_, _ = fmt.Fprintf(osStdout, "Used:    %s in %s\n", dollars(out.SpentCents), out.Month)
	if out.OrgMaxCents != nil {
		_, _ = fmt.Fprintf(osStdout, "Org cap: %s a month\n", dollars(*out.OrgMaxCents))
	}
	_, _ = fmt.Fprintln(osStdout, "The budget counts always-on capacity an agent adds (warm instances, prewarms); past it, a person approves each change.")
}

func dollars(cents int64) string {
	return fmt.Sprintf("$%d.%02d", cents/100, cents%100)
}
