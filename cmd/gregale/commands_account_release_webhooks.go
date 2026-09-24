package main

// gregale webhooks account manages one receiver for release events across all
// current and future apps owned by the active account (ADR-224).

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/base64"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
)

func cmdAccountReleaseWebhooks(args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: gregale webhooks account <list|add|info|update|rm|deliveries|retry|rotate-secret> [flags] [id]")
		return 1
	}
	command := args[0]
	fs := newFlagSet("webhooks-account-"+command, flag.ContinueOnError)
	target := fs.String("target-url", "", "HTTPS receiver URL")
	secret := fs.String("secret", "", "HMAC signing secret (omit on add to generate)")
	fromStdin := fs.Bool("from-stdin", false, "read signing secret from stdin")
	var events multiFlag
	fs.Var(&events, "event", "release event (repeatable)")
	policy := fs.String("retry-policy", "", "default|aggressive|none")
	format := fs.String("delivery-format", "", "json|cloudevents")
	enable := fs.Bool("enable", false, "enable receiver")
	disable := fs.Bool("disable", false, "disable receiver")
	pageSize := fs.Int("page-size", 50, "delivery page size (1..100)")
	pageToken := fs.String("page-token", "", "opaque delivery cursor")
	if err := fs.Parse(args[1:]); err != nil {
		return 1
	}
	if *enable && *disable {
		return printErr("Invalid flags", fmt.Errorf("--enable and --disable are mutually exclusive"))
	}
	if *fromStdin && *secret != "" {
		return printErr("Invalid flags", fmt.Errorf("--secret and --from-stdin are mutually exclusive"))
	}
	if *policy != "" && !strInSlice(*policy, webhookClosedVocab) {
		return printErr("Invalid --retry-policy", fmt.Errorf("must be one of %s", strings.Join(webhookClosedVocab, ", ")))
	}
	if *format != "" && !strInSlice(*format, webhookDeliveryFormatVocab) {
		return printErr("Invalid --delivery-format", fmt.Errorf("must be one of %s", strings.Join(webhookDeliveryFormatVocab, ", ")))
	}
	if len(events) > 0 {
		seen := make(map[string]bool, len(events))
		for _, event := range events {
			if !strInSlice(event, api.AllowedAccountReleaseWebhookEvents) || seen[event] {
				return printErr("Invalid --event", fmt.Errorf("expected distinct release events: %s", strings.Join(api.AllowedAccountReleaseWebhookEvents, ", ")))
			}
			seen[event] = true
		}
	}
	if *fromStdin {
		scanner := bufio.NewScanner(io.LimitReader(osStdin, int64(api.AppWebhookSecretMaxBytes)+2))
		if scanner.Scan() {
			*secret = strings.TrimSpace(scanner.Text())
		}
		if err := scanner.Err(); err != nil {
			return printErr("Could not read secret", err)
		}
	}
	if len(*secret) > api.AppWebhookSecretMaxBytes {
		return printErr("Invalid secret", fmt.Errorf("secret exceeds %d bytes", api.AppWebhookSecretMaxBytes))
	}
	// Exact positional guards stay here (before authedClient and mutations),
	// so a trailing token can never turn a write into a surprising request.
	switch command {
	case "list", "add":
		if fs.NArg() != 0 {
			return printErr("Invalid arguments", fmt.Errorf("unexpected positional argument"))
		}
	case "retry":
		if fs.NArg() != 2 {
			return printErr("Invalid arguments", fmt.Errorf("retry requires webhook and delivery IDs"))
		}
	default:
		if fs.NArg() != 1 {
			return printErr("Invalid arguments", fmt.Errorf("command requires exactly one webhook ID"))
		}
	}
	ids := fs.Args()
	if !validAccountWebhookCommandArgs(command, ids, *target, events, *pageSize) {
		fmt.Fprintln(os.Stderr, "usage: gregale webhooks account <list|add|info|update|rm|deliveries|retry|rotate-secret> [--target-url URL] [--event EVENT] [--secret SECRET|--from-stdin] [id] [delivery-id]")
		return 1
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	ctx := context.Background()
	switch command {
	case "list":
		rows, err := client.ListAccountReleaseWebhooks(ctx)
		if err != nil {
			return printErr("Request failed", err)
		}
		if jsonOutput {
			return jsonOut(writeJSON(rows))
		}
		for _, row := range rows {
			fmt.Printf("%-32s %-50s %-12s %s\n", row.ID, truncate(row.TargetURL, 50), row.DeliveryFormat, enabledStr(row.Enabled))
		}
	case "add":
		generated := false
		if *secret == "" {
			raw := make([]byte, 32)
			if _, err := rand.Read(raw); err != nil {
				return printErr("Could not generate webhook secret", err)
			}
			*secret = base64.RawURLEncoding.EncodeToString(raw)
			generated = true
		}
		row, err := client.CreateAccountReleaseWebhook(ctx, api.CreateAccountReleaseWebhookRequest{
			TargetURL: *target, WebhookSecret: *secret, EventFilter: events,
			RetryPolicy: *policy, DeliveryFormat: *format,
		})
		if err != nil {
			return printErr("Create failed", err)
		}
		if jsonOutput {
			if generated {
				return jsonOut(writeJSON(struct {
					api.AccountReleaseWebhookResponse
					WebhookSecret string `json:"webhook_secret"`
				}{row, *secret}))
			}
			return jsonOut(writeJSON(row))
		}
		PrintOK(osStdout, "Account release receiver created: %s -> %s", row.ID, row.TargetURL)
		if generated {
			PrintProgress(osStdout, "Signing secret (shown ONCE): %s", *secret)
		}
	case "info":
		row, err := client.GetAccountReleaseWebhook(ctx, ids[0])
		if err != nil {
			return printErr("Fetch failed", err)
		}
		if jsonOutput {
			return jsonOut(writeJSON(row))
		}
		fmt.Printf("id: %s\ntarget_url: %s\nevents: %s\nformat: %s\nenabled: %t\nsecret: %s\n", row.ID, row.TargetURL, strings.Join(row.EventFilter, ","), row.DeliveryFormat, row.Enabled, row.WebhookSecretSealedMasked)
	case "update":
		req := api.UpdateAccountReleaseWebhookRequest{}
		if *target != "" {
			req.TargetURL = target
		}
		if len(events) > 0 {
			filter := []string(events)
			req.EventFilter = &filter
		}
		if *policy != "" {
			req.RetryPolicy = policy
		}
		if *format != "" {
			req.DeliveryFormat = format
		}
		if *secret != "" {
			req.WebhookSecret = secret
		}
		if *enable {
			value := true
			req.Enabled = &value
		}
		if *disable {
			value := false
			req.Enabled = &value
		}
		row, err := client.UpdateAccountReleaseWebhook(ctx, ids[0], req)
		if err != nil {
			return printErr("Update failed", err)
		}
		if jsonOutput {
			return jsonOut(writeJSON(row))
		}
		PrintOK(osStdout, "Updated account release receiver %s", row.ID)
	case "rm":
		if err := client.DeleteAccountReleaseWebhook(ctx, ids[0]); err != nil {
			return printErr("Delete failed", err)
		}
		if jsonOutput {
			return jsonOut(writeJSON(map[string]any{"id": ids[0], "removed": true}))
		}
		PrintOK(osStdout, "Removed account release receiver %s", ids[0])
	case "deliveries":
		page, err := client.ListAccountReleaseWebhookDeliveries(ctx, ids[0], api.ListAppWebhookDeliveriesOptions{PageSize: *pageSize, PageToken: *pageToken})
		if err != nil {
			return printErr("Request failed", err)
		}
		if jsonOutput {
			return jsonOut(writeJSON(page))
		}
		for _, delivery := range page.Deliveries {
			fmt.Printf("%-32s app=%-32s %-20s %-10s attempt=%d\n", delivery.ID, delivery.AppID, delivery.Event, delivery.Status, delivery.Attempt)
		}
		if page.NextToken != "" {
			fmt.Fprintf(os.Stderr, "next page: --page-token %s\n", page.NextToken)
		}
	case "retry":
		row, err := client.RetryAccountReleaseWebhookDelivery(ctx, ids[0], ids[1])
		if err != nil {
			return printErr("Retry failed", err)
		}
		if jsonOutput {
			return jsonOut(writeJSON(row))
		}
		PrintOK(osStdout, "Queued delivery %s for retry", row.Delivery.ID)
	case "rotate-secret":
		if *secret == "" {
			return printErr("Missing secret", fmt.Errorf("--secret or --from-stdin is required"))
		}
		row, err := client.RotateAccountReleaseWebhookSecret(ctx, ids[0], api.RotateAppWebhookSecretRequest{WebhookSecret: *secret})
		if err != nil {
			return printErr("Rotate failed", err)
		}
		if jsonOutput {
			return jsonOut(writeJSON(row))
		}
		PrintOK(osStdout, "Rotated account release receiver %s secret at %s", ids[0], row.RotatedAt)
	}
	return 0
}

func validAccountWebhookCommandArgs(command string, ids []string, target string, events []string, pageSize int) bool {
	switch command {
	case "list":
		return len(ids) == 0
	case "add":
		return len(ids) == 0 && target != "" && len(events) > 0
	case "info", "update", "rm", "deliveries", "rotate-secret":
		return len(ids) == 1 && webhookIDPattern.MatchString(ids[0]) && pageSize >= 1 && pageSize <= 100
	case "retry":
		return len(ids) == 2 && webhookIDPattern.MatchString(ids[0]) && webhookIDPattern.MatchString(ids[1])
	default:
		return false
	}
}
