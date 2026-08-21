// commands_alert_presets.go — `gregale alerts preset <list|enable>`
// (issue #1233 / ADR-123). Two leaves:
//
//   * gregale alerts preset list                                  → cmdAlertPresetList
//   * gregale alerts preset enable <name> --app <slug> --webhook-url ... \
//       --webhook-secret ... [--cooldown-minutes N] [--enabled=false]
//                                                                  → cmdAlertPresetEnable
//
// Mirrors commands_alerts.go (the canonical dispatcher shape +
// flag conventions for the alerts surface) and
// commands_webhooks.go (the rotate-secret contract — also a
// "plaintext dropped, server-mints" surface). Auth: every route
// goes through authLimited → requireMFA → requireScope — same as
// the existing alerts leaves.
//
// Closed-set drift: enable leaf takes <name> as a positional arg,
// not --name, because preset names are the catalog key the CLI
// renders on `gregale alerts preset list`. Validating the
// --cooldown-minutes band locally (no network round-trip on a CLI
// typo) matches requireAlertCreateFlags's band-shape check.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"

	"github.com/onebox-faas/faas/pkg/api"
)

// cmdAlertsPreset dispatches the two preset leaves off the
// `gregale alerts preset ...` parser. Returns 1 with a usage line
// when the operator passes an unknown subcommand. Same shape as
// the parent cmdAlerts dispatcher at commands_alerts.go:40-64.
func cmdAlertsPreset(args []string) int {
	parent, _ := lookupCliCommand("alerts")
	if len(args) == 0 {
		PrintUsage(os.Stderr, "usage: gregale alerts preset <list|enable> --app <slug>", "alerts")
		return 1
	}
	switch args[0] {
	case subList:
		return cmdAlertPresetList(args[1:])
	case "enable":
		return cmdAlertPresetEnable(args[1:])
	}
	fmt.Fprintf(os.Stderr, "unknown alerts preset subcommand %q\n", args[0])
	sug, _ := suggestSubcommand(args[0], parent)
	maybeSuggestSub(sug)
	return 1
}

// cmdAlertPresetList mirrors cmdAlertList (commands_alerts.go:68)
// but takes no --app: the catalog is global, not per-app.
// Columns: name | display_name | category | metric | comparison
// | threshold | window_spec | enabled_in_catalog. JSON-mode
// (`--json`) emits the raw []AlertPresetResponse.
func cmdAlertPresetList(args []string) int {
	fs := flag.NewFlagSet("alerts preset list", flag.ContinueOnError)
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if fs.NArg() != 0 {
		PrintUsage(os.Stderr, "usage: gregale alerts preset list", "alerts")
		return 1
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	rows, err := client.ListAlertPresets(context.Background())
	if err != nil {
		return printErr("List failed", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(rows))
	}
	if len(rows) == 0 {
		_, _ = fmt.Fprintln(osStdout, "(no alert presets)")
		return 0
	}
	for _, p := range rows {
		// Glyph literals live in output.go so the lint tripwire
		// that rejects leading glyphs outside output.go lets the
		// table-rendering path through. The DisplayName column
		// is the only one a customer sees in pipes; the glyph
		// prefix is the visual signal that toggles when the
		// catalog row's enabled_in_catalog flips (precedent:
		// commands_crons_fire_now.go:175-183).
		enabledMark := GlyphFail
		if p.EnabledInCatalog {
			enabledMark = GlyphOK
		}
		fmt.Printf("%s %-32s %-15s %-22s %-3s %9s %-5s %s\n",
			enabledMark, p.Name, p.Category, p.Metric,
			p.Comparison, formatThreshold(p.Threshold),
			p.WindowSpec, p.DisplayName)
	}
	return 0
}

// cmdAlertPresetEnable mirrors cmdAlertAdd (commands_alerts.go:102)
// but takes the preset name positionally (the catalog key, NOT
// the rule name) and the operator supplies only webhook_url +
// webhook_secret + optional overrides. The (name, metric,
// comparison, threshold, window_spec, default_cooldown_minutes)
// sextuple comes from the catalog server-side.
func cmdAlertPresetEnable(args []string) int {
	fs := flag.NewFlagSet("alerts preset enable", flag.ContinueOnError)
	slug := fs.String("app", "", "app slug (required)")
	webhookURL := fs.String("webhook-url", "", "webhook URL (required, https://...)")
	webhookSecret := fs.String("webhook-secret", "", "webhook secret (required, ≤256 bytes)")
	cooldown := fs.Int(flagNameCooldownMinutes, 0, fmt.Sprintf("cooldown override in minutes (%d..%d); 0 means use preset default", api.AlertRuleCooldownMinMinutes, api.AlertRuleCooldownMaxMinutes))
	enabled := fs.Bool(flagNameEnabled, true, "whether the instantiated rule is enabled")
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if *slug == "" || fs.NArg() != 1 {
		PrintUsage(os.Stderr, "usage: gregale alerts preset enable --app <slug> [--webhook-url <url>] [--webhook-secret <s>] [--cooldown-minutes N] [--enabled=false] <preset-name>", "alerts")
		return 1
	}
	presetName := fs.Arg(0)
	if *webhookURL == "" || *webhookSecret == "" {
		return printErr("Missing flags", fmt.Errorf("--webhook-url and --webhook-secret are required"))
	}
	if *cooldown != 0 && (*cooldown < api.AlertRuleCooldownMinMinutes || *cooldown > api.AlertRuleCooldownMaxMinutes) {
		return printErr("Invalid cooldown", fmt.Errorf("--cooldown-minutes %d outside [%d,%d]", *cooldown, api.AlertRuleCooldownMinMinutes, api.AlertRuleCooldownMaxMinutes))
	}
	req := api.EnableAlertPresetRequest{
		WebhookURL:    *webhookURL,
		WebhookSecret: *webhookSecret,
		Enabled:       enabled,
	}
	if *cooldown != 0 {
		req.CooldownMinutes = cooldown
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	resp, err := client.EnableAlertPreset(context.Background(), *slug, presetName, req)
	if err != nil {
		return printErr("Enable failed", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(resp))
	}
	PrintOK(osStdout, "Alert rule %s created from preset %q for app %s.", resp.ID, presetName, *slug)
	return 0
}