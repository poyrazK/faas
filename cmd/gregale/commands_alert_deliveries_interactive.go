package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"
)

func cmdAlertDeliveriesInteractive(slug string) int {
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
	choice, err := prompt.choose(ctx, "Choose an alert rule for "+slug+".", labels, 0)
	if err != nil {
		return startInputExit(err)
	}
	if choice == len(rules) {
		PrintProgress(osStdout, "Delivery inspection canceled.")
		return 0
	}
	readCtx, cancel = context.WithTimeout(ctx, 30*time.Second)
	rule, err := client.GetAlertRule(readCtx, slug, rules[choice].ID)
	cancel()
	if err != nil {
		return printErr("Could not read selected alert rule", err)
	}
	if rule.AppID != app.ID || !sameBindingDeployment(rule.ID, rules[choice].ID) {
		return printErr("Invalid selected rule", errors.New("the rule does not match the selected app and ID"))
	}
	PrintProgress(osStdout, "App: %s; rule: %s (%s)\nMetric: %s %s %s; window=%s\nWebhook: %s", slug, oneLine(rule.Name), rule.ID, oneLine(rule.Metric), rule.Comparison, formatThreshold(rule.Threshold), oneLine(rule.WindowSpec), oneLine(rule.WebhookURL))
	includeTest, err := prompt.confirm(ctx, "Include test deliveries?")
	if err != nil {
		return startInputExit(err)
	}
	limit, err := scalePromptInt(ctx, prompt, "Maximum recent deliveries", 20, 1, 100)
	if err != nil {
		return startInputExit(err)
	}
	command := []string{"gregale"}
	if profile := currentProfile(); profile != "default" {
		command = append(command, "--profile", quoteLogCommandArg(profile))
	}
	command = append(command, "alerts", "deliveries", quoteLogCommandArg(rule.ID), "--app", quoteLogCommandArg(slug), "--limit", strconv.Itoa(limit))
	if includeTest {
		command = append(command, "--include-test")
	}
	_, _ = fmt.Fprintln(osStdout, "Equivalent command (POSIX shells):\n"+strings.Join(command, " "))
	readCtx, cancel = context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	deliveries, err := client.ListAlertRuleDeliveries(readCtx, slug, rule.ID, includeTest, limit)
	if err != nil {
		return printErr("Could not read alert deliveries", err)
	}
	return renderAlertDeliveries(deliveries, includeTest)
}
