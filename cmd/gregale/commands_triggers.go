// commands_triggers.go — CLI lifecycle for the unified trigger resource.
//
// The API already exposes one trigger primitive for broker mappings and
// cron-linked rows. Keeping the CLI on that same shape avoids making users
// learn a separate command for every source kind:
//
//	gregale triggers list|get|create|update|delete|pause|resume|records|retry|drop|dlq|metrics
//
// App slugs are resolved to app IDs at the CLI boundary for create/list so
// the wire request and the list filter use the API's actual ownership key.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
)

var triggerKindNames = []string{
	string(api.TriggerKindCron),
	string(api.TriggerKindKafka),
	string(api.TriggerKindNATS),
	string(api.TriggerKindRedisStreams),
	string(api.TriggerKindSQSCompat),
	string(api.TriggerKindQueue),
}

var triggerBrokerKindNames = []string{
	string(api.TriggerKindKafka),
	string(api.TriggerKindNATS),
	string(api.TriggerKindRedisStreams),
	string(api.TriggerKindSQSCompat),
	string(api.TriggerKindQueue),
}

var triggerRecordStateNames = []string{
	"pending", "claimed", "succeeded", "retry", "dead_letter",
}

const triggerKindsUsage = "cron|kafka|nats|redis_streams|sqs_compat|queue"
const triggerBrokerKindsUsage = "kafka|nats|redis_streams|sqs_compat|queue"

func triggerFlagSet(name, usage string) *flag.FlagSet {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(osStderr)
	fs.Usage = func() {
		PrintUsage(osStderr, usage, "triggers")
		fs.PrintDefaults()
	}
	return fs
}

// cmdTriggers dispatches the unified trigger lifecycle. Aliases are kept
// narrow and conventional: ls for list, info for get, and rm for delete.
func cmdTriggers(args []string) int {
	parent, _ := lookupCliCommand("triggers")
	if len(args) == 0 {
		PrintUsage(os.Stderr, "usage: gregale triggers <list|get|create|update|delete|pause|resume|records|retry|drop|dlq|metrics>", "triggers")
		return 1
	}
	if args[0] == "--help" || args[0] == "-h" {
		PrintUsage(os.Stdout, "usage: gregale triggers <list|get|create|update|delete|pause|resume|records|retry|drop|dlq|metrics>", "triggers")
		return 0
	}
	switch args[0] {
	case "list", "ls":
		return cmdTriggersList(args[1:])
	case "get", "info":
		return cmdTriggersGet(args[1:])
	case "create":
		return cmdTriggersCreate(args[1:])
	case "update":
		return cmdTriggersUpdate(args[1:])
	case "delete", "rm":
		return cmdTriggersDelete(args[1:])
	case "pause":
		return cmdTriggersToggle(args[1:], false)
	case "resume":
		return cmdTriggersToggle(args[1:], true)
	case "records":
		return cmdTriggersRecords(args[1:])
	case "retry":
		return cmdTriggersRecordAction(args[1:], true)
	case "drop":
		return cmdTriggersRecordAction(args[1:], false)
	case "dlq":
		return cmdTriggersDLQ(args[1:])
	case "metrics":
		return cmdTriggersMetrics(args[1:])
	default:
		fmt.Fprintf(os.Stderr, "gregale triggers: unknown subcommand %q\n", args[0])
		sug, _ := suggestSubcommand(args[0], parent)
		maybeSuggestSub(sug)
		return 1
	}
}

func triggerKindValid(kind string) bool {
	for _, name := range triggerKindNames {
		if kind == name {
			return true
		}
	}
	return false
}

func triggerBrokerKindValid(kind string) bool {
	return kind != string(api.TriggerKindCron) && triggerKindValid(kind)
}

func triggerStateValid(state string) bool {
	for _, name := range triggerRecordStateNames {
		if state == name {
			return true
		}
	}
	return false
}

func triggerUsageError(usage string, format string, args ...any) int {
	PrintFail(os.Stderr, format, args...)
	PrintUsage(os.Stderr, usage, "triggers")
	return 1
}

func cmdTriggersList(args []string) int {
	flags, pos := splitArgsForFlags(args)
	usage := "usage: gregale triggers list [--app <slug>] [--kind " + triggerKindsUsage + "]"
	fs := triggerFlagSet("triggers-list", usage)
	appSlug := fs.String("app", "", "filter to an app slug")
	kind := fs.String("kind", "", "filter by trigger kind")
	if err := fs.Parse(flags); err != nil {
		return 1
	}
	if len(pos) != 0 {
		return triggerUsageError(usage, "unexpected positional argument %q", pos[0])
	}
	if *kind != "" && !triggerKindValid(*kind) {
		return triggerUsageError(usage, "invalid --kind %q (expected one of %s)", *kind, triggerKindsUsage)
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	ctx := context.Background()
	appID := ""
	if *appSlug != "" {
		app, err := client.GetApp(ctx, *appSlug)
		if err != nil {
			return printErr("Could not load app", err)
		}
		appID = app.ID
	}
	triggers, err := client.GetTriggers(ctx, appID, api.TriggerKind(*kind))
	if err != nil {
		return printErr("Could not list triggers", err)
	}
	if jsonOutput {
		return jsonOut(writeNDJSON(triggers))
	}
	if len(triggers) == 0 {
		_, _ = fmt.Fprintln(osStdout, "(no triggers)")
		return 0
	}
	_, _ = fmt.Fprintln(osStdout, "ID                                   KIND           SLUG                       STATE      APP")
	for _, trigger := range triggers {
		renderTriggerListRow(osStdout, trigger)
	}
	return 0
}

func cmdTriggersGet(args []string) int {
	flags, pos := splitArgsForFlags(args)
	usage := "usage: gregale triggers get <id>"
	fs := triggerFlagSet("triggers-get", usage)
	if err := fs.Parse(flags); err != nil {
		return 1
	}
	if len(pos) != 1 {
		return triggerUsageError(usage, "expected one trigger ID")
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	trigger, err := client.GetTriggersId(context.Background(), pos[0])
	if err != nil {
		return printErr("Could not load trigger", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(trigger))
	}
	renderTriggerHuman(osStdout, trigger)
	return 0
}

func cmdTriggersCreate(args []string) int {
	flags, pos := splitArgsForFlags(args)
	usage := "usage: gregale triggers create --app <slug> --kind <" + triggerBrokerKindsUsage + "> [flags]"
	fs := triggerFlagSet("triggers-create", usage)
	appSlug := fs.String("app", "", "app slug (required)")
	kind := fs.String("kind", "", "trigger kind (required)")
	slug := fs.String("slug", "", "trigger slug (required for non-cron kinds)")
	config := fs.String("config", "", "JSON config (inline | @file | - for stdin)")
	enabled := fs.Bool("enabled", false, "enable the trigger")
	disabled := fs.Bool("disabled", false, "disable the trigger")
	batchSize := fs.Int("batch-size", 0, "maximum records per dispatch batch")
	batchWindow := fs.Int("batch-window-ms", 0, "maximum batch dwell time in milliseconds")
	maxAttempts := fs.Int("max-attempts", 0, "maximum delivery attempts")
	payloadMaxBytes := fs.Int("payload-max-bytes", 0, "maximum broker payload size in bytes")
	poisonStrategy := fs.String("broker-poison-strategy", "", "kafka poison strategy (commit|seek-to-offset)")
	if err := fs.Parse(flags); err != nil {
		return 1
	}
	if len(pos) != 0 {
		return triggerUsageError(usage, "unexpected positional argument %q", pos[0])
	}
	explicit := triggerExplicitFlags(fs)
	if *appSlug == "" || *kind == "" {
		return triggerUsageError(usage, "--app and --kind are required")
	}
	if !triggerKindValid(*kind) {
		return triggerUsageError(usage, "invalid --kind %q (expected one of %s)", *kind, triggerKindsUsage)
	}
	if *kind == string(api.TriggerKindCron) {
		return triggerUsageError(usage, "kind=cron is managed by `gregale crons add`; POST /v1/triggers rejects cron rows")
	}
	if !triggerBrokerKindValid(*kind) {
		return triggerUsageError(usage, "invalid --kind %q (expected one of %s)", *kind, triggerBrokerKindsUsage)
	}
	if *slug == "" || !explicit["config"] {
		return triggerUsageError(usage, "--slug and --config are required for non-cron triggers")
	}
	configRaw, err := triggerJSONFlag(*config)
	if err != nil {
		return printErr("Invalid --config", err)
	}
	if err := validateTriggerOptionalFlags(explicit, *enabled, *disabled, *batchSize, *batchWindow, *maxAttempts, *payloadMaxBytes, *poisonStrategy); err != nil {
		return printErr("Invalid trigger flags", err)
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	ctx := context.Background()
	app, err := client.GetApp(ctx, *appSlug)
	if err != nil {
		return printErr("Could not load app", err)
	}
	req := api.CreateTriggerRequest{
		AppID:  app.ID,
		Kind:   api.TriggerKind(*kind),
		Slug:   *slug,
		Config: configRaw,
	}
	if explicit["enabled"] {
		req.Enabled = boolPtr(*enabled)
	}
	if explicit["disabled"] {
		v := false
		req.Enabled = &v
	}
	if explicit["batch-size"] {
		req.BatchSizeMax = triggerIntPtr(*batchSize)
	}
	if explicit["batch-window-ms"] {
		req.BatchWindowMs = triggerIntPtr(*batchWindow)
	}
	if explicit["max-attempts"] {
		req.MaxAttempts = triggerIntPtr(*maxAttempts)
	}
	if explicit["payload-max-bytes"] {
		req.PayloadMaxBytes = triggerIntPtr(*payloadMaxBytes)
	}
	if explicit["broker-poison-strategy"] {
		req.BrokerPoisonStrategy = triggerStringPtr(*poisonStrategy)
	}
	trigger, err := client.PostTriggers(ctx, req)
	if err != nil {
		return printErr("Could not create trigger", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(trigger))
	}
	PrintOK(osStdout, "Created trigger %s", trigger.ID)
	return 0
}

func cmdTriggersUpdate(args []string) int {
	flags, pos := splitArgsForFlags(args)
	usage := "usage: gregale triggers update <id> [flags]"
	fs := triggerFlagSet("triggers-update", usage)
	enabled := fs.Bool("enabled", false, "enable the trigger")
	disabled := fs.Bool("disabled", false, "disable the trigger")
	config := fs.String("config", "", "replace JSON config (inline | @file | - for stdin)")
	schedule := fs.String("schedule", "", "replace cron expression")
	path := fs.String("path", "", "replace cron request path")
	batchSize := fs.Int("batch-size", 0, "maximum records per dispatch batch")
	batchWindow := fs.Int("batch-window-ms", 0, "maximum batch dwell time in milliseconds")
	maxAttempts := fs.Int("max-attempts", 0, "maximum delivery attempts")
	payloadMaxBytes := fs.Int("payload-max-bytes", 0, "maximum broker payload size in bytes")
	poisonStrategy := fs.String("broker-poison-strategy", "", "kafka poison strategy (commit|seek-to-offset)")
	if err := fs.Parse(flags); err != nil {
		return 1
	}
	if len(pos) != 1 {
		return triggerUsageError(usage, "expected one trigger ID")
	}
	explicit := triggerExplicitFlags(fs)
	if len(explicit) == 0 {
		return triggerUsageError(usage, "at least one update flag is required")
	}
	if explicit["enabled"] && explicit["disabled"] {
		return triggerUsageError(usage, "--enabled and --disabled are mutually exclusive")
	}
	if explicit["config"] {
		if _, err := triggerJSONFlag(*config); err != nil {
			return printErr("Invalid --config", err)
		}
	}
	if explicit["schedule"] && len(strings.Fields(*schedule)) != 5 {
		return printErr("Invalid --schedule", fmt.Errorf("expected 5 fields, got %q", *schedule))
	}
	if err := validateTriggerOptionalFlags(explicit, *enabled, *disabled, *batchSize, *batchWindow, *maxAttempts, *payloadMaxBytes, *poisonStrategy); err != nil {
		return printErr("Invalid trigger flags", err)
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	req := api.UpdateTriggerRequest{}
	if explicit["enabled"] {
		req.Enabled = boolPtr(*enabled)
	}
	if explicit["disabled"] {
		v := false
		req.Enabled = &v
	}
	if explicit["config"] {
		req.Config, err = triggerJSONFlag(*config)
		if err != nil {
			return printErr("Invalid --config", err)
		}
	}
	if explicit["schedule"] {
		req.Schedule = triggerStringPtr(*schedule)
	}
	if explicit["path"] {
		req.Path = triggerStringPtr(*path)
	}
	if explicit["batch-size"] {
		req.BatchSizeMax = triggerIntPtr(*batchSize)
	}
	if explicit["batch-window-ms"] {
		req.BatchWindowMs = triggerIntPtr(*batchWindow)
	}
	if explicit["max-attempts"] {
		req.MaxAttempts = triggerIntPtr(*maxAttempts)
	}
	if explicit["payload-max-bytes"] {
		req.PayloadMaxBytes = triggerIntPtr(*payloadMaxBytes)
	}
	if explicit["broker-poison-strategy"] {
		req.BrokerPoisonStrategy = triggerStringPtr(*poisonStrategy)
	}
	trigger, err := client.PatchTriggersId(context.Background(), pos[0], req)
	if err != nil {
		return printErr("Could not update trigger", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(trigger))
	}
	PrintOK(osStdout, "Updated trigger %s", trigger.ID)
	return 0
}

func cmdTriggersDelete(args []string) int {
	flags, pos := splitArgsForFlags(args)
	usage := "usage: gregale triggers delete <id> [--quiet]"
	fs := triggerFlagSet("triggers-delete", usage)
	quiet := fs.Bool("quiet", false, "skip the typed confirmation (for scripts)")
	if err := fs.Parse(flags); err != nil {
		return 1
	}
	if len(pos) != 1 {
		return triggerUsageError(usage, "expected one trigger ID")
	}
	if !*quiet {
		_, _ = fmt.Fprintf(osStderr, "About to delete trigger %s.\n", pos[0])
		if !requireTyped("delete trigger") {
			return 1
		}
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	if err := client.DeleteTriggersId(context.Background(), pos[0]); err != nil {
		return printErr("Could not delete trigger", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(map[string]string{"id": pos[0], "status": "deleted"}))
	}
	PrintOK(osStdout, "Deleted trigger %s", pos[0])
	return 0
}

func cmdTriggersToggle(args []string, resume bool) int {
	flags, pos := splitArgsForFlags(args)
	verb := "pause"
	if resume {
		verb = "resume"
	}
	usage := "usage: gregale triggers " + verb + " <id>"
	fs := triggerFlagSet("triggers-"+verb, usage)
	if err := fs.Parse(flags); err != nil {
		return 1
	}
	if len(pos) != 1 {
		return triggerUsageError(usage, "expected one trigger ID")
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	ctx := context.Background()
	if resume {
		err = client.PostTriggersIdResume(ctx, pos[0])
	} else {
		err = client.PostTriggersIdPause(ctx, pos[0])
	}
	if err != nil {
		return printErr("Could not "+verb+" trigger", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(map[string]any{"id": pos[0], "enabled": resume}))
	}
	PrintOK(osStdout, "Trigger %s %s", pos[0], map[bool]string{true: "resumed", false: "paused"}[resume])
	return 0
}

func cmdTriggersRecords(args []string) int {
	flags, pos := splitArgsForFlags(args)
	usage := "usage: gregale triggers records <id> [--state " + strings.Join(triggerRecordStateNames, "|") + "]"
	fs := triggerFlagSet("triggers-records", usage)
	state := fs.String("state", "", "filter by record state")
	if err := fs.Parse(flags); err != nil {
		return 1
	}
	if len(pos) != 1 {
		return triggerUsageError(usage, "expected one trigger ID")
	}
	if *state != "" && !triggerStateValid(*state) {
		return triggerUsageError(usage, "invalid --state %q", *state)
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	resp, err := client.GetTriggersIdRecords(context.Background(), pos[0], *state)
	if err != nil {
		return printErr("Could not list trigger records", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(resp))
	}
	if len(resp.Records) == 0 {
		_, _ = fmt.Fprintln(osStdout, "(no records)")
		return 0
	}
	for _, record := range resp.Records {
		_, _ = fmt.Fprintf(osStdout, "%-36s %-12s attempts=%-3d item=%s\n", record.ID, record.State, record.Attempts, record.ItemIdentifier)
	}
	return 0
}

func cmdTriggersRecordAction(args []string, retry bool) int {
	flags, pos := splitArgsForFlags(args)
	verb := "drop"
	if retry {
		verb = "retry"
	}
	usage := "usage: gregale triggers " + verb + " <trigger-id> <record-id>"
	fs := triggerFlagSet("triggers-"+verb, usage)
	if err := fs.Parse(flags); err != nil {
		return 1
	}
	if len(pos) != 2 {
		return triggerUsageError(usage, "expected a trigger ID and record ID")
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	ctx := context.Background()
	if retry {
		err = client.PostTriggersIdRecordsRidRetry(ctx, pos[0], pos[1])
	} else {
		err = client.PostTriggersIdRecordsRidDrop(ctx, pos[0], pos[1])
	}
	if err != nil {
		return printErr("Could not "+verb+" trigger record", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(map[string]string{"trigger_id": pos[0], "record_id": pos[1], "status": verb}))
	}
	PrintOK(osStdout, "Trigger record %s %s", pos[1], verb)
	return 0
}

func cmdTriggersDLQ(args []string) int {
	flags, pos := splitArgsForFlags(args)
	usage := "usage: gregale triggers dlq <id> [--reason REASON]"
	fs := triggerFlagSet("triggers-dlq", usage)
	reason := fs.String("reason", "", "filter by dead-letter reason")
	if err := fs.Parse(flags); err != nil {
		return 1
	}
	if len(pos) != 1 {
		return triggerUsageError(usage, "expected one trigger ID")
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	resp, err := client.GetTriggersIdDlq(context.Background(), pos[0], *reason)
	if err != nil {
		return printErr("Could not list trigger dead letter", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(resp))
	}
	if len(resp.Records) == 0 {
		_, _ = fmt.Fprintln(osStdout, "(no dead-letter records)")
		return 0
	}
	for _, record := range resp.Records {
		_, _ = fmt.Fprintf(osStdout, "%-36s %-24s %-10s %s\n", record.RecordID, record.Reason, record.RoutedTo, record.CreatedAt.Format("2006-01-02T15:04:05Z07:00"))
	}
	return 0
}

func cmdTriggersMetrics(args []string) int {
	flags, pos := splitArgsForFlags(args)
	usage := "usage: gregale triggers metrics <id>"
	fs := triggerFlagSet("triggers-metrics", usage)
	if err := fs.Parse(flags); err != nil {
		return 1
	}
	if len(pos) != 1 {
		return triggerUsageError(usage, "expected one trigger ID")
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	metrics, err := client.GetTriggersIdMetrics(context.Background(), pos[0])
	if err != nil {
		return printErr("Could not load trigger metrics", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(metrics))
	}
	_, _ = fmt.Fprintf(osStdout, "trigger:    %s\n", metrics.TriggerID)
	_, _ = fmt.Fprintf(osStdout, "pending:    %d\n", metrics.PendingCount)
	_, _ = fmt.Fprintf(osStdout, "claimed:    %d\n", metrics.ClaimedCount)
	_, _ = fmt.Fprintf(osStdout, "succeeded:  %d\n", metrics.SucceededCount)
	_, _ = fmt.Fprintf(osStdout, "retry:      %d\n", metrics.RetryCount)
	_, _ = fmt.Fprintf(osStdout, "dead-letter: %d\n", metrics.DeadLetterCount)
	return 0
}

func triggerExplicitFlags(fs *flag.FlagSet) map[string]bool {
	seen := make(map[string]bool)
	fs.Visit(func(f *flag.Flag) { seen[f.Name] = true })
	return seen
}

func triggerJSONFlag(value string) (json.RawMessage, error) {
	raw, err := resolvePayload(value)
	if err != nil {
		return nil, err
	}
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 {
		return nil, fmt.Errorf("expected a JSON value")
	}
	if !json.Valid(raw) {
		return nil, fmt.Errorf("invalid JSON")
	}
	return json.RawMessage(raw), nil
}

func validateTriggerOptionalFlags(explicit map[string]bool, enabled, disabled bool, batchSize, batchWindow, maxAttempts, payloadMaxBytes int, poisonStrategy string) error {
	if explicit["enabled"] && explicit["disabled"] {
		return fmt.Errorf("--enabled and --disabled are mutually exclusive")
	}
	for name, value := range map[string]int{
		"batch-size":        batchSize,
		"batch-window-ms":   batchWindow,
		"max-attempts":      maxAttempts,
		"payload-max-bytes": payloadMaxBytes,
	} {
		if explicit[name] && value <= 0 {
			return fmt.Errorf("--%s must be greater than zero", name)
		}
	}
	if explicit["broker-poison-strategy"] && poisonStrategy != api.BrokerPoisonStrategyCommit && poisonStrategy != api.BrokerPoisonStrategySeekToOffset {
		return fmt.Errorf("--broker-poison-strategy must be commit or seek-to-offset")
	}
	return nil
}

func renderTriggerListRow(w io.Writer, trigger api.Trigger) {
	slug := trigger.Slug
	if slug == "" {
		slug = GlyphEmDash
	}
	state := "disabled"
	if trigger.Enabled {
		state = "enabled"
	}
	_, _ = fmt.Fprintf(w, "%-36s %-14s %-26s %-10s %s\n", trigger.ID, trigger.Kind, slug, state, trigger.AppID)
}

func renderTriggerHuman(w io.Writer, trigger api.Trigger) {
	RenderTitle(w, fmt.Sprintf("trigger %s", trigger.ID))
	_, _ = fmt.Fprintf(w, "  kind:     %s\n", trigger.Kind)
	_, _ = fmt.Fprintf(w, "  app:      %s\n", trigger.AppID)
	if trigger.Slug != "" {
		_, _ = fmt.Fprintf(w, "  slug:     %s\n", trigger.Slug)
	}
	_, _ = fmt.Fprintf(w, "  enabled:  %t\n", trigger.Enabled)
	if trigger.Schedule != "" {
		_, _ = fmt.Fprintf(w, "  schedule: %s\n", trigger.Schedule)
	}
	if trigger.Path != "" {
		_, _ = fmt.Fprintf(w, "  path:     %s\n", trigger.Path)
	}
	if len(bytes.TrimSpace(trigger.Config)) > 0 && string(bytes.TrimSpace(trigger.Config)) != "null" {
		var compact bytes.Buffer
		if err := json.Compact(&compact, trigger.Config); err == nil {
			_, _ = fmt.Fprintf(w, "  config:   %s\n", compact.String())
		}
	}
	_, _ = fmt.Fprintf(w, "  batch:    %d records / %d ms\n", trigger.BatchSizeMax, trigger.BatchWindowMs)
	_, _ = fmt.Fprintf(w, "  attempts: %d\n", trigger.MaxAttempts)
}

func triggerIntPtr(value int) *int          { return &value }
func triggerStringPtr(value string) *string { return &value }
