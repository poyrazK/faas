// commands_ai_deployments.go — `gregale ai deployments`, an app's AI
// gateway calls by deployment (ADR 20261022153155).

package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"text/tabwriter"
)

const aiDeploymentsUsage = "usage: gregale ai deployments [--app <slug>]"

func cmdAIDeployments(args []string) int {
	fs := newFlagSet("ai deployments", flag.ContinueOnError)
	app := fs.String("app", "", "app slug (defaults to linked project app)")
	if err := parseInterspersed(fs, args); err != nil || fs.NArg() != 0 {
		return printErr("Invalid ai deployments arguments", errors.New(aiDeploymentsUsage))
	}
	slug, err := resolveAppFlagOrContext(*app)
	if err != nil && !errors.Is(err, errProjectContextNotFound) {
		return printErr("Could not read local project context", err)
	}
	if slug == "" {
		return printErr("Invalid ai deployments arguments", errors.New(aiDeploymentsUsage))
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	list, err := client.ListAIDeployments(context.Background(), slug)
	if err != nil {
		return printErr("Could not list AI calls by deployment", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(list))
	}
	if len(list.Items) == 0 {
		PrintOK(osStdout, "No AI gateway calls from %s's instances in the last 14 days.", slug)
		return 0
	}
	tw := tabwriter.NewWriter(osStdout, 0, 0, 2, ' ', 0)
	_, _ = fmt.Fprintln(tw, "DEPLOYMENT\tLAST CALL\tCALLS\tERRORS\tFLAGGED\tCOST\tPER CALL\tMEAN TTFB")
	for _, item := range list.Items {
		_, _ = fmt.Fprintf(tw, "%s\t%s\t%d\t%.2f%%\t%.2f%%\t%s\t%s\t%s\n", item.DeploymentID, item.LastCallAt, item.Calls,
			item.ErrorRate*100, item.FlaggedRate*100, financialMoney(item.CostMillicents), financialMoney(item.CostPerCallMillicents),
			formatOptionalCount(item.MeanTTFBMS))
	}
	_ = tw.Flush()
	return 0
}
