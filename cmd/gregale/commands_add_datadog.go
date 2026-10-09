package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
)

// addDatadogResult is the --json shape of `gregale add datadog` (ADR-742).
// It never contains the API key.
type addDatadogResult struct {
	App      string            `json:"app"`
	Site     string            `json:"site"`
	DryRun   bool              `json:"dry_run"`
	LogDrain addDatadogOutcome `json:"log_drain"`
	Webhook  addDatadogOutcome `json:"webhook"`
}

type addDatadogOutcome struct {
	Action string `json:"action"` // created, updated, removed, unchanged, would_create, ...
	ID     string `json:"id,omitempty"`
	Target string `json:"target,omitempty"`
}

// cmdAddDatadog implements `gregale add datadog` (ADR-742): one datadog log
// drain and one datadog webhook per app, created or updated in place. The
// API key is read from an environment variable or stdin, never from a flag
// value, so it does not land in shell history.
func cmdAddDatadog(args []string) int {
	fs := newFlagSet("add datadog", flag.ContinueOnError)
	app := fs.String("app", "", "app slug")
	site := fs.String("site", "us1", "Datadog site: "+strings.Join(api.DatadogSiteNames(), "|"))
	keyEnv := fs.String("api-key-env", "DD_API_KEY", "environment variable holding the Datadog API key")
	keyStdin := fs.Bool("api-key-stdin", false, "read the Datadog API key from stdin instead")
	dryRun := fs.Bool("dry-run", false, "show what would change without changing it")
	remove := fs.Bool("remove", false, "remove the app's Datadog log drain and webhook")
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if rejectUnexpectedFlagArgs(fs) {
		return 1
	}
	if *app == "" {
		return printErr("Missing --app", errors.New("usage: gregale add datadog --app <slug> [--site us1] [--api-key-env DD_API_KEY|--api-key-stdin] [--dry-run] [--remove]"))
	}
	logsURL, ok := api.DatadogLogsIntakeURL(*site)
	if !ok {
		return printErr("Unknown --site", fmt.Errorf("supported sites: %s", strings.Join(api.DatadogSiteNames(), ", ")))
	}
	eventsURL, _ := api.DatadogEventsURL(*site)
	key := ""
	if !*remove && !*dryRun {
		var err error
		if key, err = readDatadogAPIKey(*keyEnv, *keyStdin); err != nil {
			return printErr("Datadog API key unavailable", err)
		}
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	plan := addDatadogPlan{client: client, app: *app, logsURL: logsURL, eventsURL: eventsURL, key: key, dryRun: *dryRun}
	result := addDatadogResult{App: *app, Site: *site, DryRun: *dryRun}
	if *remove {
		result.LogDrain, result.Webhook, err = plan.remove(context.Background())
	} else {
		result.LogDrain, result.Webhook, err = plan.apply(context.Background())
	}
	if err != nil {
		return printErr("Datadog integration failed", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(result))
	}
	_, _ = fmt.Fprintf(osStdout, "Datadog (%s) for %s%s\n", *site, *app, map[bool]string{true: " — dry run, nothing changed"}[*dryRun])
	_, _ = fmt.Fprintf(osStdout, "  Logs:   %s %s\n", result.LogDrain.Action, result.LogDrain.Target)
	_, _ = fmt.Fprintf(osStdout, "  Events: %s %s\n", result.Webhook.Action, result.Webhook.Target)
	return 0
}

// readDatadogAPIKey takes the first line of stdin, or the named variable.
func readDatadogAPIKey(env string, fromStdin bool) (string, error) {
	if fromStdin {
		line, err := bufio.NewReader(osStdin).ReadString('\n')
		if err != nil && line == "" {
			return "", errors.New("no API key on stdin")
		}
		if key := strings.TrimSpace(line); key != "" {
			return key, nil
		}
		return "", errors.New("no API key on stdin")
	}
	if key := strings.TrimSpace(os.Getenv(env)); key != "" {
		return key, nil
	}
	return "", fmt.Errorf("set %s or pass --api-key-stdin", env)
}

type addDatadogPlan struct {
	client             *api.Client
	app                string
	logsURL, eventsURL string
	key                string
	dryRun             bool
}

func (p addDatadogPlan) existing(ctx context.Context) (*api.AppLogDrainResponse, *api.AppWebhookResponse, error) {
	drains, err := p.client.ListAppLogDrains(ctx, p.app)
	if err != nil {
		return nil, nil, fmt.Errorf("list log drains: %w", err)
	}
	hooks, err := p.client.ListAppWebhooks(ctx, p.app)
	if err != nil {
		return nil, nil, fmt.Errorf("list webhooks: %w", err)
	}
	var drain *api.AppLogDrainResponse
	for i := range drains {
		if drains[i].Kind == api.AppLogDrainKindDatadog {
			drain = &drains[i]
			break
		}
	}
	var hook *api.AppWebhookResponse
	for i := range hooks {
		if hooks[i].DeliveryFormat == api.AppWebhookDeliveryFormatDatadog {
			hook = &hooks[i]
			break
		}
	}
	return drain, hook, nil
}

func (p addDatadogPlan) apply(ctx context.Context) (addDatadogOutcome, addDatadogOutcome, error) {
	drain, hook, err := p.existing(ctx)
	if err != nil {
		return addDatadogOutcome{}, addDatadogOutcome{}, err
	}
	header := api.DatadogAPIKeyHeader + ": " + p.key
	format, events := api.AppWebhookDeliveryFormatDatadog, append([]string(nil), api.DatadogWebhookEvents...)
	logs := addDatadogOutcome{Target: p.logsURL}
	switch {
	case p.dryRun && drain == nil:
		logs.Action = "would_create"
	case p.dryRun:
		logs.Action, logs.ID = "would_update", drain.ID
	case drain == nil:
		row, err := p.client.CreateAppLogDrain(ctx, p.app, api.CreateAppLogDrainRequest{Kind: api.AppLogDrainKindDatadog, TargetURL: p.logsURL, AuthHeader: header})
		if err != nil {
			return logs, addDatadogOutcome{}, fmt.Errorf("create log drain: %w", err)
		}
		logs.Action, logs.ID = "created", row.ID
	default:
		enabled := true
		if _, err := p.client.UpdateAppLogDrain(ctx, p.app, drain.ID, api.UpdateAppLogDrainRequest{TargetURL: &p.logsURL, AuthHeader: &header, Enabled: &enabled}); err != nil {
			return logs, addDatadogOutcome{}, fmt.Errorf("update log drain: %w", err)
		}
		logs.Action, logs.ID = "updated", drain.ID
	}
	ev := addDatadogOutcome{Target: p.eventsURL}
	switch {
	case p.dryRun && hook == nil:
		ev.Action = "would_create"
	case p.dryRun:
		ev.Action, ev.ID = "would_update", hook.ID
	case hook == nil:
		row, err := p.client.CreateAppWebhook(ctx, p.app, api.CreateAppWebhookRequest{TargetURL: p.eventsURL, WebhookSecret: p.key, EventFilter: events, DeliveryFormat: format})
		if err != nil {
			return logs, ev, fmt.Errorf("create webhook: %w", err)
		}
		ev.Action, ev.ID = "created", row.ID
	default:
		enabled := true
		if _, err := p.client.UpdateAppWebhook(ctx, p.app, hook.ID, api.UpdateAppWebhookRequest{TargetURL: &p.eventsURL, WebhookSecret: &p.key, EventFilter: &events, DeliveryFormat: &format, Enabled: &enabled}); err != nil {
			return logs, ev, fmt.Errorf("update webhook: %w", err)
		}
		ev.Action, ev.ID = "updated", hook.ID
	}
	return logs, ev, nil
}

func (p addDatadogPlan) remove(ctx context.Context) (addDatadogOutcome, addDatadogOutcome, error) {
	drain, hook, err := p.existing(ctx)
	if err != nil {
		return addDatadogOutcome{}, addDatadogOutcome{}, err
	}
	logs, ev := addDatadogOutcome{Action: "unchanged"}, addDatadogOutcome{Action: "unchanged"}
	if drain != nil {
		logs = addDatadogOutcome{Action: "would_remove", ID: drain.ID, Target: drain.TargetURL}
		if !p.dryRun {
			if err := p.client.DeleteAppLogDrain(ctx, p.app, drain.ID); err != nil {
				return logs, ev, fmt.Errorf("remove log drain: %w", err)
			}
			logs.Action = "removed"
		}
	}
	if hook != nil {
		ev = addDatadogOutcome{Action: "would_remove", ID: hook.ID, Target: hook.TargetURL}
		if !p.dryRun {
			if err := p.client.DeleteAppWebhook(ctx, p.app, hook.ID); err != nil {
				return logs, ev, fmt.Errorf("remove webhook: %w", err)
			}
			ev.Action = "removed"
		}
	}
	return logs, ev, nil
}
