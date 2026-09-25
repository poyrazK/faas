package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// cmdPlatformTenants manages one account customer across several apps.
func cmdPlatformTenants(args []string) int {
	if len(args) == 0 {
		PrintUsage(os.Stderr, "usage: gregale platform-tenants <list|add|apply|credentials-list|credentials-apply|info|activation|link-consumer|link-surface|usage|activity|suspend|resume> [flags]", "platform-tenants")
		return 1
	}
	verb := args[0]
	fs := newFlagSet("platform-tenants-"+verb, flag.ContinueOnError)
	id := fs.String("id", "", "platform tenant UUID")
	externalRef := fs.String("external-ref", "", "stable customer reference")
	name := fs.String("name", "", "customer display name")
	consumerID := fs.String("consumer-id", "", "existing app consumer UUID")
	surfaceID := fs.String("surface-id", "", "existing tenant surface UUID")
	file := fs.String("file", "", "onboarding or hash-only credential bundle JSON file (apply)")
	dryRun := fs.Bool("dry-run", false, "preview bundle without changes (apply)")
	wait := fs.Bool("wait", false, "wait for DNS, certificate, and routing readiness (activation)")
	timeout := fs.Duration("timeout", 10*time.Minute, "maximum wait for activation")
	since := fs.String("since", "", "usage window start (RFC3339) or activity lookback (e.g. 24h)")
	until := fs.String("until", "", "usage window end (RFC3339)")
	appID := fs.String("app-id", "", "filter activity to one linked app UUID")
	status := fs.Int("status", 0, "filter activity to one HTTP status (100..599)")
	cursor := fs.String("cursor", "", "opaque next-page cursor (activity)")
	limit := fs.Int("limit", 100, "list page size (1..100; activity allows up to 200)")
	offset := fs.Int("offset", 0, "list page offset")
	if err := fs.Parse(args[1:]); err != nil {
		return 1
	}
	valid := platformTenantFlagsValid(verb, *id, *externalRef, *name, *consumerID, *surfaceID, *limit, *offset)
	if verb == "apply" || verb == "credentials-apply" {
		valid = *file != "" && *externalRef == "" && *name == "" && *consumerID == "" && *surfaceID == "" &&
			((verb == "apply" && *id == "") || (verb == "credentials-apply" && *id != ""))
	} else if *file != "" || *dryRun {
		valid = false
	}
	if verb != "activation" && *wait {
		valid = false
	}
	if verb != "usage" && verb != "activity" && (*since != "" || *until != "") {
		valid = false
	}
	if verb != "activity" && (*appID != "" || *status != 0 || *cursor != "") {
		valid = false
	}
	if verb == "activity" && (*until != "" || *status < 0 || *status > 599 || (*status > 0 && *status < 100)) {
		valid = false
	}
	if *timeout <= 0 {
		valid = false
	}
	if fs.NArg() != 0 || !valid {
		PrintUsage(os.Stderr, "usage: gregale platform-tenants <list|add|apply|credentials-list|credentials-apply|info|activation|link-consumer|link-surface|usage|activity|suspend|resume> [--file bundle.json] [--dry-run] [--id UUID] [--wait] [--timeout 10m]", "platform-tenants")
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
			if _, err := fmt.Fprintf(osStdout, "%s\t%s\t%s\t%s\n", row.ID, row.ExternalRef, row.Name, row.Status); err != nil {
				return printErr("Output failed", err)
			}
		}
		if page.NextOffset != nil {
			if _, err := fmt.Fprintf(osStdout, "Next page: --offset %d\n", *page.NextOffset); err != nil {
				return printErr("Output failed", err)
			}
		}
	case "add":
		row, err := client.CreatePlatformTenant(ctx, api.CreatePlatformTenantRequest{ExternalRef: strings.TrimSpace(*externalRef), Name: strings.TrimSpace(*name)})
		if err != nil {
			return printErr("Create failed", err)
		}
		return platformTenantOutput(row, "Platform tenant registered")
	case "apply":
		body, err := os.ReadFile(*file)
		if err != nil {
			return printErr("Read onboarding bundle failed", err)
		}
		var req api.ApplyPlatformTenantRequest
		if err := json.Unmarshal(body, &req); err != nil {
			return printErr("Invalid onboarding bundle", err)
		}
		req.DryRun = req.DryRun || *dryRun
		row, err := client.ApplyPlatformTenant(ctx, req)
		if err != nil {
			return printErr("Apply failed", err)
		}
		if jsonOutput {
			return jsonOut(writeJSON(row))
		}
		if _, err := fmt.Fprintf(osStdout, "%s\t%s\t%s\t%s\n", row.Action, row.TenantID, row.ExternalRef, row.Status); err != nil {
			return printErr("Output failed", err)
		}
		for _, item := range row.Consumers {
			if _, err := fmt.Fprintf(osStdout, "consumer\t%s\t%s\t%s\n", item.Action, item.AppID, item.ID); err != nil {
				return printErr("Output failed", err)
			}
		}
		for _, item := range row.Surfaces {
			if _, err := fmt.Fprintf(osStdout, "surface\t%s\t%s\t%s\t%s\n", item.Action, item.ID, item.Status, item.CertState); err != nil {
				return printErr("Output failed", err)
			}
		}
	case "credentials-apply":
		return platformTenantCredentialApplyCommand(ctx, client, *id, *file, *dryRun)
	case "credentials-list":
		return platformTenantCredentialListCommand(ctx, client, *id, *limit, *offset)
	case "info":
		row, err := client.GetPlatformTenant(ctx, *id)
		if err != nil {
			return printErr("Fetch failed", err)
		}
		if jsonOutput {
			return jsonOut(writeJSON(row))
		}
		if _, err := fmt.Fprintf(osStdout, "%s\t%s\t%s\t%s\n", row.ID, row.ExternalRef, row.Name, row.Status); err != nil {
			return printErr("Output failed", err)
		}
		for _, consumer := range row.Consumers {
			if _, err := fmt.Fprintf(osStdout, "consumer\t%s\t%s\n", consumer.AppID, consumer.ID); err != nil {
				return printErr("Output failed", err)
			}
		}
		for _, surface := range row.Surfaces {
			if _, err := fmt.Fprintf(osStdout, "surface\t%s\t%s\n", surface.AppID, surface.ID); err != nil {
				return printErr("Output failed", err)
			}
		}
	case "activation":
		return platformTenantActivationCommand(ctx, client, *id, *wait, *timeout)
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
		if _, err := fmt.Fprintf(osStdout, "tenant %s: requests=%d errors=%d billable_units=%d\n", row.TenantID, row.RequestCount, row.ErrorCount, row.BillableUnits); err != nil {
			return printErr("Output failed", err)
		}
	case "activity":
		row, err := client.ListPlatformTenantActivity(ctx, *id, api.PlatformTenantActivityOptions{
			Since: *since, AppID: *appID, Status: *status, Cursor: *cursor, Limit: *limit,
		})
		if err != nil {
			return printErr("Activity fetch failed", err)
		}
		if jsonOutput {
			return jsonOut(writeJSON(row))
		}
		if _, err := fmt.Fprintf(osStdout, "tenant %s: %d rows / %d represented requests / %d errors (%s to %s; retention clamped=%t)\n",
			row.TenantID, row.PageTelemetryRows, row.PageRepresentedRequests, row.PageErrorRequests,
			row.WindowStart.Format(time.RFC3339), row.WindowEnd.Format(time.RFC3339), row.RetentionClamped); err != nil {
			return printErr("Output failed", err)
		}
		for _, item := range row.Requests {
			req := item.Request
			traceID := "-"
			if req.TraceID != nil {
				traceID = *req.TraceID
			}
			if _, err := fmt.Fprintf(osStdout, "%s\t%d\t%s %s\t%dms\tcount=%d\t%s\n",
				item.AppID, req.Status, req.Method, req.Route, req.LatencyMS, req.Count, traceID); err != nil {
				return printErr("Output failed", err)
			}
		}
		if !row.PageComplete {
			if _, err := fmt.Fprintf(osStdout, "Next page: --cursor %s\n", row.NextCursor); err != nil {
				return printErr("Output failed", err)
			}
		}
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
	case "credentials-list":
		return limit >= 1 && limit <= 100 && offset >= 0 && id != "" && externalRef == "" && name == "" && consumerID == "" && surfaceID == ""
	case "info", "activation", "usage", "suspend", "resume":
		return id != "" && externalRef == "" && name == "" && consumerID == "" && surfaceID == ""
	case "activity":
		return id != "" && externalRef == "" && name == "" && consumerID == "" && surfaceID == "" && limit >= 1 && limit <= 200
	case "link-consumer":
		return id != "" && consumerID != "" && externalRef == "" && name == "" && surfaceID == ""
	case "link-surface":
		return id != "" && surfaceID != "" && externalRef == "" && name == "" && consumerID == ""
	default:
		return false
	}
}

func platformTenantActivationCommand(ctx context.Context, client *api.Client, id string, wait bool, timeout time.Duration) int {
	if wait {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}
	for {
		row, err := client.GetPlatformTenantActivation(ctx, id)
		if err != nil {
			return printErr("Activation fetch failed", err)
		}
		if !wait || row.Ready {
			if jsonOutput {
				return jsonOut(writeJSON(row))
			}
			if _, err := fmt.Fprintf(osStdout, "tenant %s: ready=%t enabled=%t status=%s\n", row.TenantID, row.Ready, row.Enabled, row.Status); err != nil {
				return printErr("Output failed", err)
			}
			for _, surface := range row.Surfaces {
				if _, err := fmt.Fprintf(osStdout, "surface %s: ready=%t status=%s cert=%s error=%s\n", surface.ID, surface.Ready, surface.Status, surface.CertState, surface.CertLastError); err != nil {
					return printErr("Output failed", err)
				}
				for _, host := range surface.Hostnames {
					if _, err := fmt.Fprintf(osStdout, "hostname %s: verified=%t txt=%s token=%s error=%s\n", host.Hostname, host.Verified, host.TXTRecord, host.ChallengeToken, host.LastError); err != nil {
						return printErr("Output failed", err)
					}
				}
			}
			return 0
		}
		select {
		case <-ctx.Done():
			return printErr("Activation wait timed out", ctx.Err())
		case <-time.After(5 * time.Second):
		}
	}
}

func platformTenantOutput(row api.PlatformTenantResponse, message string) int {
	if jsonOutput {
		return jsonOut(writeJSON(row))
	}
	PrintOK(osStdout, "%s: %s (%s)", message, row.ID, row.Status)
	return 0
}
