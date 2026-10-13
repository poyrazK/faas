package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"reflect"
	"sort"
	"strconv"
	"syscall"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func cmdAlertUpdateInteractive(slug string) int {
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
	app, err := client.GetApp(readCtx, slug)
	if err != nil {
		cancel()
		return printErr("Could not read app", err)
	}
	rules, err := client.ListAlertRules(readCtx, slug)
	cancel()
	if err != nil {
		return printErr("Could not list alert rules", err)
	}
	if len(rules) == 0 {
		PrintProgress(osStdout, "No alert rules available for %s.", slug)
		return 0
	}
	sort.Slice(rules, func(i, j int) bool {
		if rules[i].Name != rules[j].Name {
			return rules[i].Name < rules[j].Name
		}
		return rules[i].ID < rules[j].ID
	})
	labels := make([]string, 0, len(rules)+1)
	for _, rule := range rules {
		if rule.AppID != app.ID || !alertIDPattern.MatchString(rule.ID) {
			return printErr("Invalid alert list", errors.New("a rule does not match the selected app or has an invalid ID"))
		}
		labels = append(labels, fmt.Sprintf("%s · %s · %s · enabled=%t", oneLine(rule.Name), oneLine(rule.Metric), rule.ID, rule.Enabled))
	}
	labels = append(labels, "Cancel")
	prompt := &startPrompt{reader: bufio.NewReader(osStdin), writer: osStderr}
	choice, err := prompt.choose(ctx, "Choose an alert to edit for "+slug+".", labels, 0)
	if err != nil {
		return startInputExit(err)
	}
	if choice == len(rules) {
		return 0
	}
	readCtx, cancel = context.WithTimeout(ctx, 30*time.Second)
	rule, err := client.GetAlertRule(readCtx, slug, rules[choice].ID)
	cancel()
	if err != nil {
		return printErr("Could not read selected alert", err)
	}
	if rule.AppID != app.ID || !sameBindingDeployment(rule.ID, rules[choice].ID) {
		return printErr("Invalid selected rule", errors.New("the rule does not match the selected app and ID"))
	}
	PrintProgress(osStdout, "App: %s; rule: %s (%s)\nMetric: %s %s %s; action=%s", slug, oneLine(rule.Name), rule.ID, oneLine(rule.Metric), oneLine(rule.Comparison), formatThreshold(rule.Threshold), oneLine(rule.Action))
	var threshold float64
	for {
		value, inputErr := prompt.text(ctx, "Threshold", strconv.FormatFloat(rule.Threshold, 'g', -1, 64))
		if inputErr != nil {
			return startInputExit(inputErr)
		}
		threshold, err = strconv.ParseFloat(value, 64)
		if err == nil && api.IsFiniteFloat(threshold) {
			break
		}
		_, _ = fmt.Fprintln(osStderr, "Enter a finite number.")
	}
	var window string
	for {
		window, err = prompt.text(ctx, "Window (5m, 15m, 1h, 6h, 24h, 7d, 15d)", rule.WindowSpec)
		if err != nil {
			return startInputExit(err)
		}
		if api.AllowedAlertRuleWindowSpec(window) {
			break
		}
		_, _ = fmt.Fprintln(osStderr, "Choose one of the listed windows.")
	}
	cooldown, err := scalePromptInt(ctx, prompt, "Cooldown in minutes", rule.CooldownMinutes, api.AlertRuleCooldownMinMinutes, api.AlertRuleCooldownMaxMinutes)
	if err != nil {
		return startInputExit(err)
	}
	defaultEnabled := 1
	if rule.Enabled {
		defaultEnabled = 0
	}
	enabledChoice, err := prompt.choose(ctx, "Rule state", []string{"Enabled", "Disabled"}, defaultEnabled)
	if err != nil {
		return startInputExit(err)
	}
	enabled := enabledChoice == 0
	req := api.UpdateAlertRuleRequest{}
	changes := 0
	if threshold != rule.Threshold {
		req.Threshold = &threshold
		changes++
		PrintProgress(osStdout, "Threshold: %s → %s", formatThreshold(rule.Threshold), formatThreshold(threshold))
	}
	if window != rule.WindowSpec {
		req.WindowSpec = &window
		changes++
		PrintProgress(osStdout, "Window: %s → %s", oneLine(rule.WindowSpec), window)
	}
	if cooldown != rule.CooldownMinutes {
		req.CooldownMinutes = &cooldown
		changes++
		PrintProgress(osStdout, "Cooldown: %d → %d minutes", rule.CooldownMinutes, cooldown)
	}
	if enabled != rule.Enabled {
		req.Enabled = &enabled
		changes++
		PrintProgress(osStdout, "Enabled: %t → %t", rule.Enabled, enabled)
	}
	if changes == 0 {
		PrintProgress(osStdout, "No changes to save.")
		return 0
	}
	confirmed, err := prompt.confirm(ctx, "Save these alert changes?")
	if err != nil {
		return startInputExit(err)
	}
	if !confirmed {
		PrintProgress(osStdout, "Alert update canceled.")
		return 0
	}
	readCtx, cancel = context.WithTimeout(ctx, 30*time.Second)
	latest, err := client.GetAlertRule(readCtx, slug, rule.ID)
	cancel()
	if err != nil {
		return printErr("Could not recheck alert", err)
	}
	if !reflect.DeepEqual(alertEditableSnapshot(latest), alertEditableSnapshot(rule)) {
		return printErr("Alert changed", errors.New("the rule changed while you were editing; run the command again to review its current settings"))
	}
	writeCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	updated, err := client.UpdateAlertRule(writeCtx, slug, rule.ID, req)
	if err != nil {
		return printErr("Update failed", err)
	}
	PrintOK(osStdout, "Alert rule %s updated.", updated.ID)
	return 0
}

// Evaluation activity changes runtime fields without changing the configuration.
func alertEditableSnapshot(rule api.AlertRuleResponse) api.AlertRuleResponse {
	rule.State, rule.LastFiredAt, rule.LastEvaluatedAt = "", "", ""
	rule.CreatedAt, rule.UpdatedAt = "", ""
	return rule
}
