package main

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/signal"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/onebox-faas/faas/pkg/api"
	"golang.org/x/term"
)

func cmdAlertPresetEnableInteractive(explicit string) int {
	input, ok := osStdin.(*os.File)
	if jsonOutput || nonInteractive || !stdinIsTTY() || !stdoutIsTTY() || !ok || !term.IsTerminal(int(input.Fd())) {
		return printErr("Interactive terminal required", errors.New("use an explicit preset name and --webhook-secret-stdin for scripts"))
	}
	slug, err := resolveReadAppTarget(explicit)
	if err != nil {
		return readAppTargetError(err)
	}
	if !validCLISlug(slug) {
		return printErr("Invalid app", errors.New("pass a valid app slug"))
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	readCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	_, err = client.GetApp(readCtx, slug)
	if err != nil {
		cancel()
		return printErr("Could not read app", err)
	}
	catalog, err := client.ListAlertPresets(readCtx)
	cancel()
	if err != nil {
		return printErr("Could not read alert presets", err)
	}
	presets := make([]api.AlertPresetResponse, 0, len(catalog))
	for _, preset := range catalog {
		if preset.EnabledInCatalog {
			presets = append(presets, preset)
		}
	}
	if len(presets) == 0 {
		PrintProgress(osStdout, "No enabled alert presets available.")
		return 0
	}
	sort.Slice(presets, func(i, j int) bool { return presets[i].Name < presets[j].Name })
	preset, req, confirmed, err := collectAlertPreset(ctx, input, slug, presets)
	if err != nil {
		return startInputExit(err)
	}
	if !confirmed {
		PrintProgress(osStdout, "No alert rule created.")
		return 0
	}
	defer func() { req.WebhookSecret = "" }()
	if err := ctx.Err(); err != nil {
		return startInputExit(err)
	}
	readCtx, cancel = context.WithTimeout(ctx, 30*time.Second)
	latest, err := client.ListAlertPresets(readCtx)
	cancel()
	if err != nil {
		return printErr("Could not recheck selected preset", err)
	}
	matched := false
	for _, current := range latest {
		if current.Name == preset.Name && reflect.DeepEqual(current, preset) {
			matched = true
			break
		}
	}
	if !matched {
		return printErr("Preset changed during review", errors.New("run the guide again to review the current preset"))
	}
	if err := ctx.Err(); err != nil {
		return startInputExit(err)
	}
	writeCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	resp, err := client.EnableAlertPreset(writeCtx, slug, preset.Name, req)
	req.WebhookSecret = ""
	if err != nil {
		return printErr("Could not confirm alert creation", err)
	}
	PrintOK(osStdout, "Alert rule %s created from preset %q for app %s.", resp.ID, preset.Name, slug)
	command := []string{"gregale"}
	if profile := currentProfile(); profile != "default" {
		command = append(command, "--profile", quoteLogCommandArg(profile))
	}
	command = append(command, "alerts", "info", quoteLogCommandArg(resp.ID), "--app", quoteLogCommandArg(slug))
	_, _ = fmt.Fprintln(osStdout, "Inspect this rule (POSIX shells):\n"+strings.Join(command, " "))
	return 0
}

// Use one terminal reader for the entire form so no buffered visible prompt
// can read ahead into a signing secret. Restore echo before any API writes.
func collectAlertPreset(ctx context.Context, input *os.File, slug string, presets []api.AlertPresetResponse) (api.AlertPresetResponse, api.EnableAlertPresetRequest, bool, error) {
	var preset api.AlertPresetResponse
	req := api.EnableAlertPresetRequest{}
	state, err := term.MakeRaw(int(input.Fd()))
	if err != nil {
		return preset, req, false, err
	}
	defer func() { _ = term.Restore(int(input.Fd()), state) }()
	stream := secretTerminalIO{input: input, output: osStderr}
	terminal := &secretEntryTerminal{Terminal: term.NewTerminal(stream, ""), input: stream, maxBytes: api.AlertRuleWebhookSecretMaxBytes}
	_, _ = fmt.Fprintln(terminal, "Choose an alert preset (number; q cancels). Ctrl-C or Ctrl-D cancels before saving.")
	for i, p := range presets {
		_, _ = fmt.Fprintf(terminal, "  %d. %s — %s (minimum plan: %s)\n", i+1, p.Name, p.DisplayName, p.MinimumPlan)
	}
	for {
		value, err := secretTerminalRead(ctx, terminal, "Preset", false)
		if err != nil {
			return preset, req, false, err
		}
		if value == "q" {
			return preset, req, false, nil
		}
		n, err := strconv.Atoi(value)
		if err == nil && n >= 1 && n <= len(presets) {
			preset = presets[n-1]
			break
		}
		_, _ = fmt.Fprintln(terminal, "Choose a number from the displayed list.")
	}
	_, _ = fmt.Fprintf(terminal, "\n%s\n%s\nRule: %s %s %s; window: %s; cooldown: %d minutes; minimum plan: %s\n", preset.DisplayName, preset.Description, preset.Metric, preset.Comparison, formatThreshold(preset.Threshold), preset.WindowSpec, preset.DefaultCooldownMinutes, preset.MinimumPlan)
	for {
		value, err := secretTerminalRead(ctx, terminal, "Webhook destination (HTTPS URL)", false)
		if err != nil {
			return preset, req, false, err
		}
		u, parseErr := url.Parse(value)
		if parseErr == nil && len(value) <= 2048 && strings.IndexFunc(value, unicode.IsControl) < 0 && u.Scheme == "https" && u.Hostname() != "" && u.User == nil && u.Fragment == "" {
			req.WebhookURL = value
			break
		}
		_, _ = fmt.Fprintln(terminal, "Use an HTTPS URL without user credentials, a fragment, or control characters (at most 2048 bytes).")
	}
	for {
		value, err := secretTerminalRead(ctx, terminal, "Webhook signing secret (hidden)", true)
		if err != nil {
			return preset, req, false, err
		}
		if value != "" && utf8.ValidString(value) && strings.IndexFunc(value, unicode.IsControl) < 0 {
			req.WebhookSecret = value
			break
		}
		_, _ = fmt.Fprintln(terminal, "Enter a nonempty signing secret without control characters.")
	}
	req.Enabled = boolPtr(true)
	action := "webhook"
	req.Action = &action
	_, _ = fmt.Fprintf(terminal, "\nReview: app=%s; preset=%s; action=webhook; enabled=true\nMetric: %s %s %s; window=%s; cooldown=%d minutes\nWebhook: %s\nSigning secret: hidden\n", slug, preset.Name, preset.Metric, preset.Comparison, formatThreshold(preset.Threshold), preset.WindowSpec, preset.DefaultCooldownMinutes, req.WebhookURL)
	_, _ = fmt.Fprintln(terminal, "This creates a new enabled webhook alert rule using preset defaults. Plan and server admission checks apply.")
	answer, err := secretTerminalRead(ctx, terminal, "Create this alert rule? (y/N)", false)
	confirmed := strings.EqualFold(answer, "y") || strings.EqualFold(answer, "yes")
	if err != nil || !confirmed {
		req.WebhookSecret = ""
	}
	return preset, req, confirmed, err
}
