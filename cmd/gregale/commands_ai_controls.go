// commands_ai_controls.go — `gregale ai guardrails|keys|alerts|logs`
// (ADR-991): per-app guardrails, extra provider keys, budget alerts and the
// opt-in AI request log.

package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/outbound"
)

const aiGuardrailsUsage = "usage: gregale ai guardrails <integration-id> --app <slug> [--rpm N|none] [--tpm N|none] " +
	"[--models a,b*|none] [--max-output-tokens N|none]"

// cmdAIGuardrails merges the given flags into the app's current guardrails
// and replaces them, because the API takes the complete set.
func cmdAIGuardrails(args []string) int {
	fs := newFlagSet("ai guardrails", flag.ContinueOnError)
	slug := fs.String("app", "", "bound app slug (required)")
	rpm := fs.String("rpm", "", "admitted calls per UTC minute, or none")
	tpm := fs.String("tpm", "", "tokens per UTC minute (settled plus reserved), or none")
	models := fs.String("models", "", "comma-separated model names or prefixes ending in *, or none")
	maxOutput := fs.String("max-output-tokens", "", "per-request output-token ceiling, or none")
	if err := parseInterspersed(fs, args); err != nil || fs.NArg() != 1 || *slug == "" {
		return printErr("Invalid ai guardrails arguments", errors.New(aiGuardrailsUsage))
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	bindings, err := client.ListOutboundAppBindings(context.Background(), *slug)
	if err != nil {
		return printErr("Could not read the app's bindings", err)
	}
	var req api.PutOutboundBindingAIGuardrailsRequest
	found := false
	for _, binding := range bindings.Items {
		if binding.Integration.ID == fs.Arg(0) {
			found = true
			req = api.PutOutboundBindingAIGuardrailsRequest{RequestsPerMinute: binding.AIRequestsPerMinute, TokensPerMinute: binding.AITokensPerMinute,
				AllowedModels: binding.AIAllowedModels, MaxOutputTokens: binding.AIMaxOutputTokens}
		}
	}
	if !found {
		return printErr("Integration not bound", fmt.Errorf("%s is not bound to app %s", fs.Arg(0), *slug))
	}
	if req, err = applyAIGuardrailFlags(req, *rpm, *tpm, *models, *maxOutput); err != nil || !req.Valid() {
		return printErr("Invalid AI guardrails", errors.Join(err, errors.New("rpm 1..100000, tpm 1..1e9, max-output-tokens 1..1000000, up to 32 models")))
	}
	if err := client.SetOutboundBindingAIGuardrails(context.Background(), *slug, fs.Arg(0), req); err != nil {
		return printErr("Could not update AI guardrails", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(req))
	}
	_, _ = fmt.Fprintf(osStdout, "AI guardrails for %s on %s: rpm=%s tpm=%s models=%s max-output-tokens=%s\n", *slug, fs.Arg(0),
		formatOptionalCount(req.RequestsPerMinute), formatOptionalCount(req.TokensPerMinute), formatAllowedModels(req.AllowedModels),
		formatOptionalCount(req.MaxOutputTokens))
	return 0
}

// applyAIGuardrailFlags applies "" (keep), "none" (clear) or a new value.
func applyAIGuardrailFlags(req api.PutOutboundBindingAIGuardrailsRequest, rpm, tpm, models, maxOutput string) (api.PutOutboundBindingAIGuardrailsRequest, error) {
	var err error
	if req.RequestsPerMinute, err = optionalLimit(rpm, req.RequestsPerMinute, parseTokenCount); err != nil {
		return req, err
	}
	if req.TokensPerMinute, err = optionalLimit(tpm, req.TokensPerMinute, parseTokenCount); err != nil {
		return req, err
	}
	if req.MaxOutputTokens, err = optionalLimit(maxOutput, req.MaxOutputTokens, parseTokenCount); err != nil {
		return req, err
	}
	switch strings.TrimSpace(models) {
	case "":
	case "none":
		req.AllowedModels = nil
	default:
		req.AllowedModels = nil
		for _, model := range strings.Split(models, ",") {
			if model = strings.TrimSpace(model); model != "" {
				req.AllowedModels = append(req.AllowedModels, model)
			}
		}
	}
	return req, nil
}

func formatAllowedModels(models []string) string {
	if models == nil {
		return "any"
	}
	return strings.Join(models, ",")
}

const aiKeysUsage = "usage: gregale ai keys <list|add|rm> <integration-id> [--label <label> --from-stdin] [<key-id>]"

// cmdAIKeys manages an integration's extra provider keys. Key values are
// read from stdin only, never from flags, so they stay out of shell history.
func cmdAIKeys(args []string) int {
	if len(args) < 2 {
		return printErr("Invalid ai keys arguments", errors.New(aiKeysUsage))
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	switch args[0] {
	case "list":
		return cmdAIKeysList(client, args[1])
	case "add":
		return cmdAIKeysAdd(client, args[1:])
	case "rm":
		if len(args) != 3 {
			return printErr("Invalid ai keys rm arguments", errors.New("usage: gregale ai keys rm <integration-id> <key-id>"))
		}
		if err := client.DeleteOutboundCredentialKey(context.Background(), args[1], args[2]); err != nil {
			return printErr("Could not remove key", err)
		}
		_, _ = fmt.Fprintf(osStdout, "Key %s removed from %s\n", args[2], args[1])
		return 0
	}
	return printErr("Invalid ai keys subcommand", errors.New(aiKeysUsage))
}

func cmdAIKeysList(client *api.Client, integrationID string) int {
	keys, err := client.ListOutboundCredentialKeys(context.Background(), integrationID)
	if err != nil {
		return printErr("Could not list keys", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(keys))
	}
	if len(keys.Items) == 0 {
		_, _ = fmt.Fprintln(osStdout, "(no extra keys; calls use the primary credential)")
		return 0
	}
	tw := tabwriter.NewWriter(osStdout, 0, 0, 2, ' ', 0)
	_, _ = fmt.Fprintln(tw, "ID\tLABEL\tCREATED")
	for _, key := range keys.Items {
		_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\n", key.ID, key.Label, key.CreatedAt.UTC().Format(time.RFC3339))
	}
	_ = tw.Flush()
	return 0
}

func cmdAIKeysAdd(client *api.Client, args []string) int {
	fs := newFlagSet("ai keys add", flag.ContinueOnError)
	label := fs.String("label", "", "key label, a DNS label (required)")
	fromStdin := fs.Bool("from-stdin", false, "read the credential from stdin (required)")
	if err := parseInterspersed(fs, args); err != nil || fs.NArg() != 1 || !api.ValidOutboundCredentialKeyLabel(*label) || !*fromStdin {
		return printErr("Invalid ai keys add arguments", errors.New("usage: gregale ai keys add <integration-id> --label <label> --from-stdin"))
	}
	value, err := readCredentialLine(osStdin)
	if err != nil || !outbound.ValidManagedAuthorization(value) {
		return printErr("Invalid credential", errors.Join(err, errors.New("stdin must hold one printable line of at most 8192 bytes")))
	}
	key, err := client.CreateOutboundCredentialKey(context.Background(), fs.Arg(0), api.CreateOutboundCredentialKeyRequest{Label: *label, Authorization: value})
	if err != nil {
		return printErr("Could not add key", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(key))
	}
	_, _ = fmt.Fprintf(osStdout, "Key %s (%s) added to %s\n", key.ID, key.Label, fs.Arg(0))
	return 0
}

// readCredentialLine reads one line, without its line ending, from r.
func readCredentialLine(r io.Reader) (string, error) {
	scanner := bufio.NewScanner(io.LimitReader(r, outbound.ManagedAuthorizationMaxBytes+2))
	scanner.Buffer(make([]byte, 0, 4096), outbound.ManagedAuthorizationMaxBytes+2)
	if !scanner.Scan() {
		if err := scanner.Err(); err != nil {
			return "", err
		}
		return "", errors.New("stdin was empty")
	}
	return strings.TrimRight(scanner.Text(), "\r"), nil
}

const aiAlertsUsage = "usage: gregale ai alerts <integration-id> [--limit N]"

func cmdAIAlerts(args []string) int {
	fs := newFlagSet("ai alerts", flag.ContinueOnError)
	limit := fs.Int("limit", 0, "alerts to show, 1 to 100 (default 100)")
	if err := parseInterspersed(fs, args); err != nil || fs.NArg() != 1 || *limit < 0 || *limit > api.OutboundAIBudgetAlertsPageMax {
		return printErr("Invalid ai alerts arguments", errors.New(aiAlertsUsage))
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	alerts, err := client.ListOutboundAIBudgetAlerts(context.Background(), fs.Arg(0), *limit)
	if err != nil {
		return printErr("Could not list AI budget alerts", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(alerts))
	}
	if len(alerts.Items) == 0 {
		_, _ = fmt.Fprintln(osStdout, "(no budget alerts)")
		return 0
	}
	tw := tabwriter.NewWriter(osStdout, 0, 0, 2, ' ', 0)
	_, _ = fmt.Fprintln(tw, "CROSSED\tBUDGET\tAPP / END USER / TAG\tPERIOD\tTHRESHOLD\tUSED\tLIMIT")
	for _, alert := range alerts.Items {
		app := "-"
		if alert.AppID != nil {
			app = *alert.AppID
		} else if alert.EndUser != nil {
			app = *alert.EndUser
		} else if alert.Tag != nil {
			app = *alert.Tag
		}
		used, limitText := strconv.FormatInt(alert.Used, 10), strconv.FormatInt(alert.Limit, 10)
		if strings.HasSuffix(alert.Budget, "_cost") {
			used, limitText = financialMoney(alert.Used), financialMoney(alert.Limit)
		}
		_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%d%%\t%s\t%s\n", alert.CrossedAt.UTC().Format(time.RFC3339), alert.Budget, app,
			alert.PeriodStart, alert.ThresholdPercent, used, limitText)
	}
	_ = tw.Flush()
	return 0
}

const aiLogsUsage = "usage: gregale ai logs <integration-id> [--limit N] [--before RFC3339] | gregale ai logs show <integration-id> <request-id>"

func cmdAILogs(args []string) int {
	if len(args) > 0 && args[0] == "show" {
		return cmdAILogsShow(args[1:])
	}
	fs := newFlagSet("ai logs", flag.ContinueOnError)
	limit := fs.Int("limit", 50, "calls to show, 1 to 200")
	beforeText := fs.String("before", "", "show calls before this time (a previous page's next_before)")
	if err := parseInterspersed(fs, args); err != nil || fs.NArg() != 1 || *limit < 1 || *limit > api.OutboundAIRequestLogPageMax {
		return printErr("Invalid ai logs arguments", errors.New(aiLogsUsage))
	}
	var before *time.Time
	if *beforeText != "" {
		parsed, err := time.Parse(time.RFC3339Nano, *beforeText)
		if err != nil {
			return printErr("Invalid --before", err)
		}
		before = &parsed
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	page, err := client.ListOutboundAIRequestLog(context.Background(), fs.Arg(0), *limit, before)
	if err != nil {
		return printErr("Could not read the AI request log", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(page))
	}
	if len(page.Items) == 0 {
		_, _ = fmt.Fprintln(osStdout, "(no logged calls; enable with gregale ai policy <integration-id> --request-log on)")
		return 0
	}
	tw := tabwriter.NewWriter(osStdout, 0, 0, 2, ' ', 0)
	_, _ = fmt.Fprintln(tw, "TIME\tID\tCALLER\tEND USER\tMODEL\tSTATUS\tIN\tOUT\tCOST\tMS\tTTFB\tATTEMPTS\tCONTENT")
	for _, entry := range page.Items {
		endUser, content := "-", "-"
		if entry.EndUser != nil {
			endUser = *entry.EndUser
		}
		if entry.ContentCaptured {
			content = "yes"
		}
		_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%d\t%d\t%d\t%s\t%d\t%s\t%d\t%s\n", entry.CreatedAt.UTC().Format(time.RFC3339), entry.ID,
			entry.Caller, endUser, entry.Model, entry.Status, entry.InputTokens, entry.OutputTokens, financialMoney(entry.CostMillicents),
			entry.DurationMS, formatOptionalCount(entry.TimeToFirstByteMS), entry.Attempts, content)
	}
	_ = tw.Flush()
	if page.NextBefore != nil {
		_, _ = fmt.Fprintf(osStdout, "more: --before %s\n", page.NextBefore.UTC().Format(time.RFC3339Nano))
	}
	return 0
}

// cmdAILogsShow prints one logged call's captured bodies (ADR-996).
func cmdAILogsShow(args []string) int {
	fs := newFlagSet("ai logs show", flag.ContinueOnError)
	if err := parseInterspersed(fs, args); err != nil || fs.NArg() != 2 {
		return printErr("Invalid ai logs show arguments", errors.New(aiLogsUsage))
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	content, err := client.GetOutboundAIRequestContent(context.Background(), fs.Arg(0), fs.Arg(1))
	if err != nil {
		return printErr("Could not read the captured content (capture it with gregale ai policy <integration-id> --request-log-content on)", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(content))
	}
	for _, part := range []struct {
		name string
		body api.OutboundAIContentBody
	}{{"Request", content.Request}, {"Response", content.Response}} {
		notes := []string{part.body.Encoding}
		if part.body.ContentType != nil {
			notes = append([]string{*part.body.ContentType}, notes...)
		}
		if part.body.Truncated {
			notes = append(notes, "truncated")
		}
		_, _ = fmt.Fprintf(osStdout, "%s (%s)\n%s\n\n", part.name, strings.Join(notes, ", "), part.body.Body)
	}
	return 0
}
