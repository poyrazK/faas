package main

// `gregale app protection <app>` shows an app's production protection, and
// `gregale app protection on|off <app>` turns it on or off
// (ADR-20261116120000 §5, §6). Today's spelling is
// `gregale app <app> protection [on|off]`.

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
)

const subProtection = "protection"

const appProtectionUsage = "usage: gregale app protection <app> | on <app> | off <app>"

// cmdAppProtection runs `app <slug> protection [on|off]`.
func cmdAppProtection(slug string, args []string) int {
	if slug == "" || len(args) > 1 {
		PrintUsage(osStderr, appProtectionUsage, "apps")
		return 1
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	ctx := context.Background()
	var resp api.AppProtectionResponse
	switch {
	case len(args) == 0:
		resp, err = client.GetAppProtection(ctx, slug)
	case args[0] == api.AppProtectionStateOn || args[0] == api.AppProtectionStateOff:
		resp, err = client.SetAppProtection(ctx, slug, args[0])
	default:
		PrintUsage(osStderr, appProtectionUsage, "apps")
		return 1
	}
	if err != nil {
		return printErr("Could not read or change app protection", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(resp))
	}
	renderAppProtection(osStdout, slug, resp)
	return 0
}

// protectionLabels are the items' names in the summary line.
var protectionLabels = map[string]string{
	"availability_slo":        "availability SLO",
	"latency_slo":             "latency SLO",
	"health_check":            "health check",
	"email_channel":           "email alerts",
	"availability_burn_alert": "availability alert",
	"latency_burn_alert":      "latency alert",
	"health_alert":            "health alert",
	"availability_gate":       "rollback gate",
}

// protectionSummary is the one line `app get` and `app protection` print.
func protectionSummary(slug string, resp api.AppProtectionResponse) string {
	switch {
	case resp.State == api.AppProtectionStateNone || resp.State == api.AppProtectionStateOff:
		return fmt.Sprintf("not protected (gregale app protection on %s)", slug)
	case resp.Protected:
		return "protected ✓ (SLOs, health check, alerts, rollback)"
	}
	var missing []string
	pending := 0
	for _, item := range resp.Items {
		switch item.State {
		case api.AppProtectionItemPending:
			pending++
		case api.AppProtectionItemSkipped, api.AppProtectionItemRemoved:
			missing = append(missing, protectionLabels[item.Item]+" "+item.State)
		}
	}
	if len(missing) == 0 && pending > 0 {
		return "being set up (waits for a live production deploy)"
	}
	return fmt.Sprintf("partly protected — %s (gregale app protection %s)", strings.Join(missing, ", "), slug)
}

// printAppProtectionLine adds the production protection line to the app
// info block. It prints nothing for a project environment, or when the
// account cannot read protection (capability not released, older apid).
func printAppProtectionLine(ctx context.Context, client *Client, slug, environment string) {
	if environment != "" {
		return
	}
	resp, err := client.GetAppProtection(ctx, slug)
	if err != nil {
		return
	}
	fmt.Printf("%-30s %s\n", "production:", protectionSummary(slug, resp))
}

// renderAppProtection prints the summary and each item.
func renderAppProtection(w io.Writer, slug string, resp api.AppProtectionResponse) {
	_, _ = fmt.Fprintf(w, "Production: %s\n", protectionSummary(slug, resp))
	for _, item := range resp.Items {
		line := fmt.Sprintf("  %-20s %-9s %s", protectionLabels[item.Item], item.State, item.Name)
		if item.Reason != "" {
			line += " (" + strings.ReplaceAll(item.Reason, "_", " ") + ")"
		}
		_, _ = fmt.Fprintln(w, strings.TrimRight(line, " "))
	}
}
