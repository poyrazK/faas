package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"text/tabwriter"
)

const aiIntegrationsUsage = "usage: gregale ai integrations list | gregale ai integrations rm <integration-id> [--yes]"

// cmdAIIntegrations lists and removes AI integrations. Every other ai
// command takes an integration ID, and nothing listed them or removed one
// short of the raw DELETE route (prod hunt 2026-11-25 r51, 2026-11-28).
func cmdAIIntegrations(args []string) int {
	if len(args) == 0 {
		return printErr("Invalid ai integrations arguments", errors.New(aiIntegrationsUsage))
	}
	switch args[0] {
	case "list", "ls":
		if len(args) != 1 {
			return printErr("Invalid ai integrations list arguments", errors.New(aiIntegrationsUsage))
		}
		return cmdAIIntegrationsList()
	case "rm", "delete":
		return cmdAIIntegrationsRemove(args[1:])
	}
	return printErr("Invalid ai integrations subcommand", errors.New(aiIntegrationsUsage))
}

func cmdAIIntegrationsList() int {
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	offers, err := client.ListOutboundIntegrationOffers(context.Background())
	if err != nil {
		return printErr("Could not list integrations", err)
	}
	type row struct {
		ID         string `json:"id"`
		Name       string `json:"name"`
		Provider   string `json:"provider"`
		Origin     string `json:"origin"`
		Enabled    bool   `json:"enabled"`
		Credential bool   `json:"credential_configured"`
	}
	rows := []row{}
	for _, offer := range offers.Items {
		if offer.AIProvider == "" {
			continue
		}
		rows = append(rows, row{ID: offer.ID, Name: offer.Name, Provider: offer.AIProvider, Origin: offer.Origin,
			Enabled: offer.Enabled, Credential: offer.CredentialConfigured})
	}
	if jsonOutput {
		return jsonOut(writeJSON(map[string]any{"items": rows}))
	}
	if len(rows) == 0 {
		_, _ = fmt.Fprintln(osStdout, "(no AI integrations; set one up with gregale add ai --app <app> --provider <provider>)")
		return 0
	}
	tw := tabwriter.NewWriter(osStdout, 0, 0, 2, ' ', 0)
	_, _ = fmt.Fprintln(tw, "ID\tNAME\tPROVIDER\tORIGIN\tENABLED\tKEY")
	for _, r := range rows {
		_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%t\t%t\n", r.ID, r.Name, r.Provider, r.Origin, r.Enabled, r.Credential)
	}
	_ = tw.Flush()
	return 0
}

func cmdAIIntegrationsRemove(args []string) int {
	fs := newFlagSet("ai integrations rm", flag.ContinueOnError)
	yes := fs.Bool("yes", false, "remove without asking")
	if err := parseInterspersed(fs, args); err != nil || fs.NArg() != 1 {
		return printErr("Invalid ai integrations rm arguments", errors.New(aiIntegrationsUsage))
	}
	id := fs.Arg(0)
	if !*yes {
		question := fmt.Sprintf("Remove AI integration %s? Apps bound to it can no longer call it, and its stored keys are deleted.", id)
		if code := confirmYesNo(question, "--yes", "removing the integration"); code != 0 {
			return code
		}
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	if err := client.DeleteOutboundIntegration(context.Background(), id); err != nil {
		return printErr("Could not remove the integration", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(map[string]any{"id": id, "removed": true}))
	}
	_, _ = fmt.Fprintf(osStdout, "Removed AI integration %s\n", id)
	return 0
}
