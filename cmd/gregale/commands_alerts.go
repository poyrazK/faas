// commands_alerts.go — `gregale alerts <list|add|info|update|rm|rotate-secret>`
// (Tier C). Mirrors commands_webhooks.go exactly: same dispatcher shape,
// same flag conventions, same rotate-secret contract (server-minted
// plaintext dropped — alerts.go:227-233).
//
// Auth: every route goes through authLimited → requireMFA →
// requireScope(ScopesReadSurface|ScopesDeployWriteSurface). Free /
// Hobby customers without MFA enrolled will hit 403 on every leaf;
// surface the server's APIError verbatim (consistent with the
// Tier B admin consume-credits 403 hint pattern, commands_admin.go:140-150).
//
// Closed-set drift test (commands_webhooks.go:127-134 mirrored): all
// four enum-typed flags --metric / --comparison / --window-spec /
// --failure-source are validated against the pkg/api.AllowedAlertRule*
// slices BEFORE the network round-trip. A CLI typo costs zero latency.

package main

import (
	"context"
	"flag"
	"fmt"
	"math"
	"os"
	"regexp"
	"strconv"

	"github.com/onebox-faas/faas/pkg/api"
)

// flagName constants — lifted from string literals so goconst stops
// flagging the repetition when the same flag name appears in both
// the fs.Bool definition and the fs.Visit matcher (pointer-shape
// detection in cmdAlertUpdate).
const (
	flagNameEnabled         = "enabled"
	flagNameCooldownMinutes = "cooldown-minutes"
)

func cmdAlerts(args []string) int {
	parent, _ := lookupCliCommand("alerts")
	if len(args) == 0 {
		PrintUsage(os.Stderr, "usage: gregale alerts <list|add|info|update|rm|rotate-secret|preset> --app <slug>", "alerts")
		return 1
	}
	switch args[0] {
	case subList:
		return cmdAlertList(args[1:])
	case subAdd:
		return cmdAlertAdd(args[1:])
	case subInfo:
		return cmdAlertInfo(args[1:])
	case subUpdate:
		return cmdAlertUpdate(args[1:])
	case subRm:
		return cmdAlertRm(args[1:])
	case "rotate-secret":
		return cmdAlertRotateSecret(args[1:])
	case "preset":
		// Issue #1233 / ADR-123 — alert-preset catalog +
		// instantiate-from-preset. Two leaves under preset:
		// list, enable. Documented at commands_alert_presets.go.
		return cmdAlertsPreset(args[1:])
	}
	fmt.Fprintf(os.Stderr, "unknown alerts subcommand %q\n", args[0])
	sug, _ := suggestSubcommand(args[0], parent)
	maybeSuggestSub(sug)
	return 1
}

// cmdAlertList mirrors cmdWebhookList. The SDK returns a flat slice;
// human-mode renders name | metric | threshold | window | state.
func cmdAlertList(args []string) int {
	fs := flag.NewFlagSet("alerts list", flag.ContinueOnError)
	slug := fs.String("app", "", "app slug (required)")
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if *slug == "" {
		PrintUsage(os.Stderr, "usage: gregale alerts list --app <slug>", "alerts")
		return 1
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	rules, err := client.ListAlertRules(context.Background(), *slug)
	if err != nil {
		return printErr("List failed", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(rules))
	}
	if len(rules) == 0 {
		_, _ = fmt.Fprintln(osStdout, "(no alert rules)")
		return 0
	}
	for _, r := range rules {
		fmt.Printf("%-32s %-22s %-18s %-6s %-6s %s\n", r.ID, r.Metric, r.WindowSpec, formatThreshold(r.Threshold), r.Comparison, r.Name)
	}
	return 0
}

// cmdAlertAdd mirrors cmdWebhookAdd. Validates closed-set fields
// locally, then sends the request. failure-source is required iff
// metric == failed_invocations (constraint: alert_rules_failure_source_xor_chk).
func cmdAlertAdd(args []string) int {
	fs := flag.NewFlagSet("alerts add", flag.ContinueOnError)
	slug := fs.String("app", "", "app slug (required)")
	name := fs.String("name", "", "rule name (required, 3..120 chars)")
	metric := fs.String("metric", "", "metric (closed set; one of error_rate_pct|latency_p50_ms|latency_p95_ms|latency_p99_ms|cold_start_pct|request_count|failed_invocations)")
	comparison := fs.String("comparison", "", "comparison (gt|gte|lt|lte)")
	threshold := fs.Float64("threshold", math.NaN(), "threshold value (must be finite)")
	windowSpec := fs.String("window-spec", "", "window (5m|15m|1h|6h|24h|7d|15d)")
	failureSource := fs.String("failure-source", "", "failure source (any|cron|queue|delayed_task|async_invoke) — required iff --metric=failed_invocations")
	webhookURL := fs.String("webhook-url", "", "webhook URL (required, https://...)")
	webhookSecret := fs.String("webhook-secret", "", "webhook secret (required, ≤256 bytes)")
	cooldown := fs.Int(flagNameCooldownMinutes, api.AlertRuleDefaultCooldownMinutes, fmt.Sprintf("cooldown window in minutes (%d..%d)", api.AlertRuleCooldownMinMinutes, api.AlertRuleCooldownMaxMinutes))
	enabled := fs.Bool(flagNameEnabled, true, "whether the rule is enabled")
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if code, ok := requireAlertCreateFlags(slug, name, metric, comparison, windowSpec, webhookURL, webhookSecret, threshold, cooldown); !ok {
		return code
	}
	if !validateAlertClosedSets(metric, comparison, windowSpec, failureSource) {
		return 1
	}
	if !api.IsFiniteFloat(*threshold) {
		return printErr("Invalid threshold", fmt.Errorf("--threshold must be a finite number; got %v", *threshold))
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	resp, err := client.CreateAlertRule(context.Background(), *slug, api.CreateAlertRuleRequest{
		Name:            *name,
		Enabled:         enabled,
		Metric:          *metric,
		Comparison:      *comparison,
		Threshold:       *threshold,
		WindowSpec:      *windowSpec,
		FailureSource:   *failureSource,
		WebhookURL:      *webhookURL,
		WebhookSecret:   *webhookSecret,
		CooldownMinutes: cooldown,
	})
	if err != nil {
		return printErr("Create failed", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(resp))
	}
	PrintOK(osStdout, "Alert rule %s created for app %s.", resp.ID, *slug)
	return 0
}

// requireAlertCreateFlags validates the presence + range of the
// required closed-set / range-constrained flags shared by
// cmdAlertAdd and cmdAlertUpdate. Returns (1, false) when the
// usage line should fire, (0, true) on success. Extracts the
// repeated presence check so both leaves stay under the
// 50-line handler cap (CLAUDE.md "Handlers ≤ 50 lines — extract").
func requireAlertCreateFlags(slug, name, metric, comparison, windowSpec, webhookURL, webhookSecret *string, threshold *float64, cooldown *int) (int, bool) {
	if *slug == "" || *name == "" || *metric == "" || *comparison == "" ||
		*windowSpec == "" || *webhookURL == "" || *webhookSecret == "" || math.IsNaN(*threshold) {
		PrintUsage(os.Stderr, "usage: gregale alerts add --app <slug> --name <text> --metric <v> --comparison <op> --threshold <num> --window-spec <w> --webhook-url <url> --webhook-secret <s> [--failure-source <s>] [--cooldown-minutes N] [--enabled=false]", "alerts")
		return 1, false
	}
	if *cooldown < api.AlertRuleCooldownMinMinutes || *cooldown > api.AlertRuleCooldownMaxMinutes {
		return printErr("Invalid cooldown", fmt.Errorf("--cooldown-minutes %d outside [%d,%d]", *cooldown, api.AlertRuleCooldownMinMinutes, api.AlertRuleCooldownMaxMinutes)), false
	}
	return 0, true
}

// validateAlertClosedSets checks the four closed-set enums shared by
// alerts add + update. Fires the APIError-style printErr on the
// first failure (consistent with the other leaves). On failure
// returns false; the caller returns 1.
func validateAlertClosedSets(metric, comparison, windowSpec, failureSource *string) bool {
	if !api.AllowedAlertRuleMetric(*metric) {
		return printErr("Invalid metric", fmt.Errorf("--metric %q is not in the closed set", *metric)) == 0
	}
	if !api.AllowedAlertRuleComparison(*comparison) {
		return printErr("Invalid comparison", fmt.Errorf("--comparison %q must be one of gt|gte|lt|lte", *comparison)) == 0
	}
	if !api.AllowedAlertRuleWindowSpec(*windowSpec) {
		return printErr("Invalid window-spec", fmt.Errorf("--window-spec %q must be 5m|15m|1h|6h|24h|7d|15d", *windowSpec)) == 0
	}
	if *metric == "failed_invocations" {
		if *failureSource == "" {
			return printErr("Missing failure-source", fmt.Errorf("--failure-source is required when --metric=failed_invocations")) == 0
		}
		if !api.AllowedAlertRuleFailureSource(*failureSource) {
			return printErr("Invalid failure-source", fmt.Errorf("--failure-source %q must be any|cron|queue|delayed_task|async_invoke", *failureSource)) == 0
		}
	}
	return true
}

// cmdAlertInfo mirrors cmdAuditEventsGet — single id, multi-line
// labelled block, --json output.
func cmdAlertInfo(args []string) int {
	fs := flag.NewFlagSet("alerts info", flag.ContinueOnError)
	slug := fs.String("app", "", "app slug (required)")
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if *slug == "" || fs.NArg() != 1 {
		PrintUsage(os.Stderr, "usage: gregale alerts info --app <slug> <alert-id>", "alerts")
		return 1
	}
	id := fs.Arg(0)
	if !alertIDPattern.MatchString(id) {
		return printErr("Invalid alert id", fmt.Errorf("must be a 32-hex-char UUID; got %q", id))
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	resp, err := client.GetAlertRule(context.Background(), *slug, id)
	if err != nil {
		return printErr("Fetch failed", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(resp))
	}
	fmt.Printf("id:           %s\n", resp.ID)
	fmt.Printf("name:         %s\n", resp.Name)
	fmt.Printf("enabled:      %t\n", resp.Enabled)
	fmt.Printf("metric:       %s\n", resp.Metric)
	fmt.Printf("comparison:   %s\n", resp.Comparison)
	fmt.Printf("threshold:    %s\n", formatThreshold(resp.Threshold))
	fmt.Printf("window_spec:  %s\n", resp.WindowSpec)
	if resp.FailureSource != "" {
		fmt.Printf("failure_source: %s\n", resp.FailureSource)
	}
	fmt.Printf("webhook_url:  %s\n", resp.WebhookURL)
	fmt.Printf("state:        %s\n", resp.State)
	fmt.Printf("cooldown:     %d minutes\n", resp.CooldownMinutes)
	if resp.LastFiredAt != "" {
		fmt.Printf("last_fired:   %s\n", resp.LastFiredAt)
	}
	return 0
}

// cmdAlertUpdate mirrors cmdWebhookUpdate — pointer-everything so
// "omitted" (leave alone) is distinguishable from "zero" (clear).
// Closed-set fields validate locally. metric-family swap is NOT
// forbidden here — the CLI passes whatever the operator typed; the
// server's alert_rules_failure_source_xor_chk fires if it violates
// the constraint (alerts.go:118-123).
func cmdAlertUpdate(args []string) int {
	fs := flag.NewFlagSet("alerts update", flag.ContinueOnError)
	slug := fs.String("app", "", "app slug (required)")
	name := fs.String("name", "", "rule name (3..120 chars)")
	enabled := fs.Bool(flagNameEnabled, true, "enable/disable the rule")
	metric := fs.String("metric", "", "metric (closed set)")
	comparison := fs.String("comparison", "", "comparison (gt|gte|lt|lte)")
	threshold := fs.Float64("threshold", math.NaN(), "threshold (must be finite)")
	windowSpec := fs.String("window-spec", "", "window (5m|15m|1h|6h|24h|7d|15d)")
	webhookURL := fs.String("webhook-url", "", "webhook URL")
	webhookSecret := fs.String("webhook-secret", "", "webhook secret (≤256 bytes)")
	cooldown := fs.Int(flagNameCooldownMinutes, api.AlertRuleDefaultCooldownMinutes, fmt.Sprintf("cooldown (%d..%d)", api.AlertRuleCooldownMinMinutes, api.AlertRuleCooldownMaxMinutes))
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if *slug == "" || fs.NArg() != 1 {
		PrintUsage(os.Stderr, "usage: gregale alerts update --app <slug> [--name <text>] [--enabled=false] [--metric <v>] [--comparison <op>] [--threshold <num>] [--window-spec <w>] [--webhook-url <url>] [--webhook-secret <s>] [--cooldown-minutes N] <alert-id>", "alerts")
		return 1
	}
	id := fs.Arg(0)
	if !alertIDPattern.MatchString(id) {
		return printErr("Invalid alert id", fmt.Errorf("must be a 32-hex-char UUID; got %q", id))
	}
	if !validateAlertUpdateFlags(metric, comparison, windowSpec, threshold, cooldown) {
		return 1
	}
	// UpdateAlertRuleRequest is pointer-everything (pkg/api/alerts.go:124-134):
	// "omitted" (leave alone) is distinguishable from "zero" (clear).
	// fs.Visit tells us which flags the operator actually passed; without
	// it, --enabled defaulting to true would silently re-enable a disabled
	// rule on a rename-only update.
	enabledSet := false
	cooldownSet := false
	fs.Visit(func(f *flag.Flag) {
		switch f.Name {
		case flagNameEnabled:
			enabledSet = true
		case flagNameCooldownMinutes:
			cooldownSet = true
		}
	})
	req := api.UpdateAlertRuleRequest{
		Name:          ptrIfNonEmpty(*name),
		Metric:        ptrIfNonEmpty(*metric),
		Comparison:    ptrIfNonEmpty(*comparison),
		Threshold:     thrIfFinite(*threshold),
		WindowSpec:    ptrIfNonEmpty(*windowSpec),
		WebhookURL:    ptrIfNonEmpty(*webhookURL),
		WebhookSecret: ptrIfNonEmpty(*webhookSecret),
	}
	if enabledSet {
		req.Enabled = enabled
	}
	if cooldownSet {
		req.CooldownMinutes = cooldown
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	resp, err := client.UpdateAlertRule(context.Background(), *slug, id, req)
	if err != nil {
		return printErr("Update failed", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(resp))
	}
	PrintOK(osStdout, "Alert rule %s updated.", resp.ID)
	return 0
}

// validateAlertUpdateFlags is the update-shape twin of
// validateAlertClosedSets: every closed-set check is gated on
// non-empty (omitted != invalid), and the threshold finite-check
// is gated on non-NaN (the unset sentinel from flag.Float64).
// Extracted so cmdAlertUpdate stays under the 50-line handler cap.
func validateAlertUpdateFlags(metric, comparison, windowSpec *string, threshold *float64, cooldown *int) bool {
	if *metric != "" && !api.AllowedAlertRuleMetric(*metric) {
		return printErr("Invalid metric", fmt.Errorf("--metric %q is not in the closed set", *metric)) == 0
	}
	if *comparison != "" && !api.AllowedAlertRuleComparison(*comparison) {
		return printErr("Invalid comparison", fmt.Errorf("--comparison %q must be one of gt|gte|lt|lte", *comparison)) == 0
	}
	if *windowSpec != "" && !api.AllowedAlertRuleWindowSpec(*windowSpec) {
		return printErr("Invalid window-spec", fmt.Errorf("--window-spec %q must be 5m|15m|1h|6h|24h|7d|15d", *windowSpec)) == 0
	}
	if !math.IsNaN(*threshold) && !api.IsFiniteFloat(*threshold) {
		return printErr("Invalid threshold", fmt.Errorf("--threshold must be a finite number; got %v", *threshold)) == 0
	}
	if *cooldown < api.AlertRuleCooldownMinMinutes || *cooldown > api.AlertRuleCooldownMaxMinutes {
		return printErr("Invalid cooldown", fmt.Errorf("--cooldown-minutes %d outside [%d,%d]", *cooldown, api.AlertRuleCooldownMinMinutes, api.AlertRuleCooldownMaxMinutes)) == 0
	}
	return true
}

// cmdAlertRm mirrors cmdWebhookRm — 204 No Content on success.
func cmdAlertRm(args []string) int {
	fs := flag.NewFlagSet("alerts rm", flag.ContinueOnError)
	slug := fs.String("app", "", "app slug (required)")
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if *slug == "" || fs.NArg() != 1 {
		PrintUsage(os.Stderr, "usage: gregale alerts rm --app <slug> <alert-id>", "alerts")
		return 1
	}
	id := fs.Arg(0)
	if !alertIDPattern.MatchString(id) {
		return printErr("Invalid alert id", fmt.Errorf("must be a 32-hex-char UUID; got %q", id))
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	if err := client.DeleteAlertRule(context.Background(), *slug, id); err != nil {
		return printErr("Delete failed", err)
	}
	PrintOK(osStdout, "Alert rule %s deleted.", id)
	return 0
}

// cmdAlertRotateSecret mirrors cmdWebhookRotateSecret. The success
// message adapts for the alerts contract (alerts.go:227-233): the
// plaintext is server-minted and NEVER returned to the customer —
// there is no reveal flow, dashboard or otherwise. The CLI confirms
// the rotation succeeded and the operator provisions the new secret
// in the webhook receiver out-of-band (same wording as the Tier B
// webhook fix).
func cmdAlertRotateSecret(args []string) int {
	fs := flag.NewFlagSet("alerts rotate-secret", flag.ContinueOnError)
	slug := fs.String("app", "", "app slug (required)")
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if *slug == "" || fs.NArg() != 1 {
		PrintUsage(os.Stderr, "usage: gregale alerts rotate-secret --app <slug> <alert-id>", "alerts")
		return 1
	}
	id := fs.Arg(0)
	if !alertIDPattern.MatchString(id) {
		return printErr("Invalid alert id", fmt.Errorf("must be a 32-hex-char UUID; got %q", id))
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	out, err := client.RotateAlertRuleSecret(context.Background(), *slug, id)
	if err != nil {
		return printErr("Rotate failed", err)
	}
	PrintOK(osStdout, "Alert rule %s secret rotated at %s (sealed=%s). Plaintext is server-minted and not retrievable; provision the new secret in the webhook receiver out-of-band.",
		id, out.RotatedAt, out.WebhookSecretSealedMasked)
	return 0
}

// alertIDPattern matches the 32-hex shape apid uses for alert rule
// ids. Same convention as webhookIDPattern (commands_webhooks.go:342).
var alertIDPattern = regexp.MustCompile(`^[0-9a-fA-F]{32}$`)

// formatThreshold renders a float threshold without scientific
// notation for the table view. Avoids the "%g" default which would
// render 0.0001 as "1e-04" — unhelpful for an alert rule the
// operator reads by eye.
func formatThreshold(v float64) string {
	return strconv.FormatFloat(v, 'f', -1, 64)
}

// ptrIfNonEmpty returns nil for an empty string, else a pointer to
// the string. Mirrors the pattern in cmdWebhookUpdate for
// pointer-everything DTO fields — "omitted" must be distinguishable
// from "zero" on the wire.
func ptrIfNonEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// thrIfFinite returns nil for NaN (the unset sentinel from flag.Float64),
// else a pointer to the value. Symmetric to ptrIfNonEmpty for the
// threshold field which can't use empty-string as the sentinel.
func thrIfFinite(v float64) *float64 {
	if math.IsNaN(v) {
		return nil
	}
	return &v
}
