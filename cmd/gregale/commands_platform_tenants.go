package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
)

// cmdPlatformTenants manages one account customer across several apps.
func cmdPlatformTenants(args []string) int {
	if len(args) == 0 {
		PrintUsage(os.Stderr, "usage: gregale platform-tenants <list|add|info|link-consumer|link-surface|usage|suspend|resume> [flags]", "platform-tenants")
		return 1
	}
	verb := args[0]
	fs := newFlagSet("platform-tenants-"+verb, flag.ContinueOnError)
	id := fs.String("id", "", "platform tenant UUID")
	externalRef := fs.String("external-ref", "", "stable customer reference")
	name := fs.String("name", "", "customer display name")
	consumerID := fs.String("consumer-id", "", "existing app consumer UUID")
	surfaceID := fs.String("surface-id", "", "existing tenant surface UUID")
	since := fs.String("since", "", "usage window start (RFC3339)")
	until := fs.String("until", "", "usage window end (RFC3339)")
	limit := fs.Int("limit", 100, "list page size (1..100)")
	offset := fs.Int("offset", 0, "list page offset")
	if err := fs.Parse(args[1:]); err != nil {
		return 1
	}
	if fs.NArg() != 0 || !platformTenantFlagsValid(verb, *id, *externalRef, *name, *consumerID, *surfaceID, *limit, *offset) {
		PrintUsage(os.Stderr, "usage: gregale platform-tenants <list|add|info|link-consumer|link-surface|usage|suspend|resume> [--id UUID] [--external-ref REF] [--name NAME] [--consumer-id UUID] [--surface-id UUID]", "platform-tenants")
		return 1
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	ctx := context.Background()
	switch verb {
	case "list":
		page, err := client.ListPlatformTenants(ctx, *limit, *offset)
		if err != nil {
			return printErr("List failed", err)
		}
		if jsonOutput {
			return jsonOut(writeJSON(page))
		}
		for _, row := range page.Tenants {
			fmt.Fprintf(osStdout, "%s\t%s\t%s\t%s\n", row.ID, row.ExternalRef, row.Name, row.Status)
		}
		if page.NextOffset != nil {
			fmt.Fprintf(osStdout, "Next page: --offset %d\n", *page.NextOffset)
		}
	case "add":
		row, err := client.CreatePlatformTenant(ctx, api.CreatePlatformTenantRequest{ExternalRef: strings.TrimSpace(*externalRef), Name: strings.TrimSpace(*name)})
		if err != nil {
			return printErr("Create failed", err)
		}
		return platformTenantOutput(row, "Platform tenant registered")
	case "info":
		row, err := client.GetPlatformTenant(ctx, *id)
		if err != nil {
			return printErr("Fetch failed", err)
		}
		if jsonOutput {
			return jsonOut(writeJSON(row))
		}
		fmt.Fprintf(osStdout, "%s\t%s\t%s\t%s\n", row.ID, row.ExternalRef, row.Name, row.Status)
		for _, consumer := range row.Consumers {
			fmt.Fprintf(osStdout, "consumer\t%s\t%s\n", consumer.AppID, consumer.ID)
		}
		for _, surface := range row.Surfaces {
			fmt.Fprintf(osStdout, "surface\t%s\t%s\n", surface.AppID, surface.ID)
		}
	case "link-consumer":
		row, err := client.LinkPlatformTenantConsumer(ctx, *id, api.LinkPlatformTenantConsumerRequest{ConsumerID: *consumerID})
		if err != nil {
			return printErr("Link failed", err)
		}
		if jsonOutput {
			return jsonOut(writeJSON(row))
		}
		PrintOK(osStdout, "Linked consumer %s to platform tenant %s", row.ID, *id)
	case "link-surface":
		row, err := client.LinkPlatformTenantSurface(ctx, *id, api.LinkPlatformTenantSurfaceRequest{SurfaceID: *surfaceID})
		if err != nil {
			return printErr("Link failed", err)
		}
		if jsonOutput {
			return jsonOut(writeJSON(row))
		}
		PrintOK(osStdout, "Linked surface %s to platform tenant %s", row.ID, *id)
	case "usage":
		row, err := client.GetPlatformTenantUsage(ctx, *id, api.APIConsumerUsageOptions{Since: *since, Until: *until})
		if err != nil {
			return printErr("Usage failed", err)
		}
		if jsonOutput {
			return jsonOut(writeJSON(row))
		}
		fmt.Fprintf(osStdout, "tenant %s: requests=%d errors=%d billable_units=%d\n", row.TenantID, row.RequestCount, row.ErrorCount, row.BillableUnits)
	case "suspend", "resume":
		status := "suspended"
		if verb == "resume" {
			status = "active"
		}
		row, err := client.SetPlatformTenantStatus(ctx, *id, api.SetPlatformTenantStatusRequest{Status: status})
		if err != nil {
			return printErr("Status update failed", err)
		}
		return platformTenantOutput(row, "Platform tenant updated")
	}
	return 0
}

func platformTenantFlagsValid(verb, id, externalRef, name, consumerID, surfaceID string, limit, offset int) bool {
	switch verb {
	case "list":
		return limit >= 1 && limit <= 100 && offset >= 0 && id == "" && externalRef == "" && name == "" && consumerID == "" && surfaceID == ""
	case "add":
		return externalRef != "" && name != "" && id == "" && consumerID == "" && surfaceID == ""
	case "info", "usage", "suspend", "resume":
		return id != "" && externalRef == "" && name == "" && consumerID == "" && surfaceID == ""
	case "link-consumer":
		return id != "" && consumerID != "" && externalRef == "" && name == "" && surfaceID == ""
	case "link-surface":
		return id != "" && surfaceID != "" && externalRef == "" && name == "" && consumerID == ""
	default:
		return false
	}
}

func platformTenantOutput(row api.PlatformTenantResponse, message string) int {
	if jsonOutput {
		return jsonOut(writeJSON(row))
	}
	PrintOK(osStdout, "%s: %s (%s)", message, row.ID, row.Status)
	return 0
}
