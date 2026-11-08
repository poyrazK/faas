// adr: 20261022121642 — Data API change hooks: the change event SQL and the
// runtime switch that publishes recorded changes as events.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/onebox-faas/faas/cmd/gregale/templates"
	"github.com/onebox-faas/faas/pkg/api"
)

const dataAPIChangeEventsURLKey = "DATA_API_CHANGE_EVENTS_URL"

// dataAPIHooksReceipt is what enable, status and disable report. Source is
// the event source changes publish under, for subscriptions.
type dataAPIHooksReceipt struct {
	App       string   `json:"app"`
	Scope     string   `json:"scope"`
	Enabled   bool     `json:"enabled"`
	Source    string   `json:"source"`
	Changed   []string `json:"changed"`
	Restarted bool     `json:"restarted,omitempty"`
	Parked    bool     `json:"parked,omitempty"`
	// DrainEvery is the drain schedule that wakes a parked API, if any.
	DrainEvery string `json:"drain_every,omitempty"`
	// RuntimeReports is "unavailable" when the installation accepts no
	// runtime reports: changes then wait in gregale_change_events.
	RuntimeReports string `json:"runtime_reports,omitempty"`
	// Delivery is what the runtime last reported, when it reported.
	Delivery *api.DataAPIChangeHookStatus `json:"delivery,omitempty"`
}

func cmdDataAPIHooks(args []string) int {
	if len(args) == 0 {
		PrintUsage(os.Stderr, "usage: gregale data-api hooks <sql|enable|status|disable>", "data-api")
		return 1
	}
	switch args[0] {
	case "sql":
		return cmdDataAPIHooksSQL(args[1:])
	case "enable", "status", "disable":
		return cmdDataAPIHooksSetting(args[0], args[1:])
	default:
		printCommandValidation(os.Stderr, "unknown data-api hooks subcommand %q\n", args[0])
		return 1
	}
}

// dataAPIHooksSQL renders the change hooks migration for a schema.
func dataAPIHooksSQL(schema string) (string, error) {
	if !dataAPIStorageSchema.MatchString(schema) {
		return "", fmt.Errorf("schema %q must match %s", schema, dataAPIStorageSchema)
	}
	content, err := templates.FS.ReadFile("data-api/change-hooks.sql")
	if err != nil {
		return "", err
	}
	return strings.NewReplacer("{{schema}}", schema, "{{backlog_max}}", strconv.Itoa(api.DataAPIChangeHooksBacklogMax)).Replace(string(content)), nil
}

const dataAPIHooksUpgradeHeader = `-- Data API change hooks upgrade (ADR-20261022121642 amendments 2 and 3).
-- Apply as the schema owner in a migration to a database whose hooks were
-- installed by an earlier CLI; applying it again changes nothing. It adds the
-- backlog bound (changes stop recording while %d wait to publish, so a
-- backlog the platform never takes cannot fill the database), notes when
-- changes were left out, retries rejected changes under new identifiers and
-- adds gregale_hooks.discard_waiting(). Building the index holds writes to
-- gregale_change_events while it runs.
SET LOCAL lock_timeout = '5s';
CREATE INDEX IF NOT EXISTS gregale_change_events_pending ON %s.gregale_change_events (id) WHERE failed_at IS NULL;

`

// dataAPIHooksUpgradeSQL renders the upgrade for hooks installed by an
// earlier CLI: the pending index and the template's replayable section, from
// the same template as the full installation so the two never drift.
func dataAPIHooksUpgradeSQL(schema string) (string, error) {
	full, err := dataAPIHooksSQL(schema)
	if err != nil {
		return "", err
	}
	const start, end = "-- gregale:upgrade-begin", "-- gregale:upgrade-end\n"
	from, to := strings.Index(full, start), strings.Index(full, end)
	if from < 0 || to < from {
		return "", errors.New("the change hooks template has no upgrade section")
	}
	return fmt.Sprintf(dataAPIHooksUpgradeHeader, api.DataAPIChangeHooksBacklogMax, schema) + full[from:to+len(end)], nil
}

func cmdDataAPIHooksSQL(args []string) int {
	fs := newFlagSet("data-api hooks sql", flag.ContinueOnError)
	schema := fs.String("schema", "api", "the Data API's first exposed schema")
	upgrade := fs.Bool("upgrade", false, "print only the upgrade for hooks installed before the backlog bound")
	if err := parseInterspersed(fs, args); err != nil || fs.NArg() != 0 {
		PrintUsage(os.Stderr, "usage: gregale data-api hooks export [--schema api] [--upgrade]", "data-api")
		return 1
	}
	render := dataAPIHooksSQL
	if *upgrade {
		render = dataAPIHooksUpgradeSQL
	}
	sql, err := render(*schema)
	if err != nil {
		return printErr("Invalid schema", err)
	}
	_, _ = fmt.Fprint(osStdout, sql)
	return 0
}

func cmdDataAPIHooksSetting(verb string, args []string) int {
	fs := newFlagSet("data-api hooks "+verb, flag.ContinueOnError)
	scope := fs.String("scope", api.DefaultEnvScope, "environment scope")
	noRestart := fs.Bool("no-restart", false, "store settings without requesting a fresh restart")
	drainEvery := fs.String("drain-every", "", "wake a parked API this often to publish changes other writers recorded (e.g. 5m), or off")
	usage := fmt.Sprintf("usage: gregale data-api hooks %s NAME [--scope SCOPE] [--no-restart]", verb)
	switch verb {
	case "status":
		usage = "usage: gregale data-api hooks status NAME [--env SCOPE]"
	case "enable":
		usage = "usage: gregale data-api hooks enable NAME [--drain-every DURATION|off] [--scope SCOPE] [--no-restart]"
	}
	if err := parseInterspersed(fs, args); err != nil || fs.NArg() != 1 || !api.ValidAppSlug(fs.Arg(0)) || api.ValidateScope(*scope) != nil ||
		(verb == "status" && *noRestart) || (verb != "enable" && *drainEvery != "") {
		PrintUsage(os.Stderr, usage, "data-api")
		return 1
	}
	schedule := ""
	if *drainEvery != "" && *drainEvery != "off" {
		var err error
		if schedule, err = dataAPIDrainSchedule(*drainEvery); err != nil {
			return printErr("Invalid --drain-every", err)
		}
		if *scope != api.DefaultEnvScope {
			return printErr("Invalid --drain-every", errors.New("a drain schedule wakes the production deployment; set it on the default scope"))
		}
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	ctx := context.Background()
	var receipt dataAPIHooksReceipt
	switch verb {
	case "enable":
		// The drain schedule goes in before hooks turn on: a plan that allows
		// no crons (Free) refuses it while nothing has changed yet.
		var drain func(*dataAPIHooksReceipt) error
		if *drainEvery != "" {
			drain = func(receipt *dataAPIHooksReceipt) error {
				return applyDataAPIDrainSchedule(ctx, client, receipt, schedule, *drainEvery)
			}
		}
		receipt, err = enableDataAPIHooks(ctx, client, fs.Arg(0), *scope, !*noRestart, drain)
	case "disable":
		receipt, err = disableDataAPIHooks(ctx, client, fs.Arg(0), *scope, !*noRestart)
	default:
		receipt, err = dataAPIHooksStatus(ctx, client, fs.Arg(0), *scope)
	}
	if err != nil {
		return printErr("Could not "+verb+" change hooks", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(receipt))
	}
	printDataAPIHooks(verb, receipt)
	return 0
}

// enableDataAPIHooks stores the platform origin the runtime publishes to.
// drain, when set, stores the drain schedule after every check and before
// the setting, so a refused schedule leaves hooks as they were.
func enableDataAPIHooks(ctx context.Context, client *api.Client, slug, scope string, restart bool, drain func(*dataAPIHooksReceipt) error) (dataAPIHooksReceipt, error) {
	origin, ok := dataAPIUsageURL(client.BaseURL())
	if !ok {
		return dataAPIHooksReceipt{}, errors.New("change hooks need an HTTPS platform API; this CLI targets " + client.BaseURL())
	}
	// Only production publishes: the platform refuses a stage's changes, which
	// would otherwise publish under the production app's source.
	if scope != api.DefaultEnvScope {
		return dataAPIHooksReceipt{}, fmt.Errorf("change hooks publish only from production; enable them on the %s scope", api.DefaultEnvScope)
	}
	receipt, err := dataAPIHooksStatus(ctx, client, slug, scope)
	if err != nil {
		return receipt, err
	}
	// Without runtime reports nothing publishes and recorded changes only
	// pile up in the database; refuse before anything is stored.
	if receipt.RuntimeReports == api.DataAPIRuntimeReportsUnavailable {
		return receipt, fmt.Errorf("%w; nothing was changed", errDataAPIRuntimeReportsUnavailable("Publishing change events"))
	}
	if drain != nil {
		if err := drain(&receipt); err != nil {
			return receipt, err
		}
	}
	if err := client.SetSecretWithScope(ctx, slug, dataAPIChangeEventsURLKey, origin, scope); err != nil {
		return receipt, err
	}
	receipt.Enabled, receipt.Changed = true, append([]string{"change_hooks"}, receipt.Changed...)
	return receipt, restartDataAPIHooks(ctx, client, &receipt, restart)
}

// applyDataAPIDrainSchedule sets or (with "off") removes the drain cron.
func applyDataAPIDrainSchedule(ctx context.Context, client *api.Client, receipt *dataAPIHooksReceipt, schedule, every string) error {
	app, err := client.GetApp(ctx, receipt.App)
	if err != nil {
		return err
	}
	if err := setDataAPIDrainSchedule(ctx, client, receipt.App, app.ID, schedule); err != nil {
		return fmt.Errorf("the drain schedule was not stored, so change hooks were not changed: %w", err)
	}
	receipt.Changed = append(receipt.Changed, "drain_schedule")
	if schedule != "" {
		receipt.DrainEvery = every
	} else {
		receipt.DrainEvery = ""
	}
	return nil
}

// disableDataAPIHooks turns publishing off and removes the drain schedule,
// whose runs would otherwise fail against a runtime without hooks.
func disableDataAPIHooks(ctx context.Context, client *api.Client, slug, scope string, restart bool) (dataAPIHooksReceipt, error) {
	receipt, err := dataAPIHooksStatus(ctx, client, slug, scope)
	if err != nil {
		return receipt, err
	}
	if err := unsetDataAPISetting(ctx, client, slug, dataAPIChangeEventsURLKey, scope); err != nil {
		return receipt, err
	}
	receipt.Enabled, receipt.Changed = false, []string{"change_hooks"}
	if receipt.DrainEvery != "" && scope == api.DefaultEnvScope {
		app, err := client.GetApp(ctx, slug)
		if err == nil {
			err = setDataAPIDrainSchedule(ctx, client, slug, app.ID, "")
		}
		if err != nil {
			return receipt, fmt.Errorf("change hooks are off; the drain schedule was not removed: %w", err)
		}
		receipt.DrainEvery, receipt.Changed = "", append(receipt.Changed, "drain_schedule")
	}
	return receipt, restartDataAPIHooks(ctx, client, &receipt, restart)
}

func restartDataAPIHooks(ctx context.Context, client *api.Client, receipt *dataAPIHooksReceipt, restart bool) error {
	if !restart {
		return nil
	}
	_, parked, err := restartDataAPISettings(ctx, client, receipt.App)
	if err != nil {
		return fmt.Errorf("settings stored; fresh restart failed: %w", err)
	}
	receipt.Restarted, receipt.Parked = !parked, parked
	return nil
}

// dataAPIHooksStatus reads the setting and the app's producer source.
func dataAPIHooksStatus(ctx context.Context, client *api.Client, slug, scope string) (dataAPIHooksReceipt, error) {
	app, err := client.GetApp(ctx, slug)
	if err != nil {
		return dataAPIHooksReceipt{}, err
	}
	receipt := dataAPIHooksReceipt{App: slug, Scope: scope, Source: "app." + app.ID, Changed: []string{}}
	secrets, err := client.ListSecretsWithScope(ctx, slug, scope)
	if err != nil {
		return receipt, err
	}
	for _, secret := range secrets.Secrets {
		if secret.Key == dataAPIChangeEventsURLKey && (secret.Scope == "" || secret.Scope == scope) {
			receipt.Enabled = true
		}
	}
	// Delivery and the drain schedule are reported when the control plane
	// offers them; an older release leaves them out.
	if status, ok := dataAPIPlatformStatus(ctx, client, slug); ok {
		receipt.RuntimeReports, receipt.Delivery = status.RuntimeReports, status.ChangeHooks
	}
	if drain, err := dataAPIDrainCron(ctx, client, slug); err == nil && drain != nil && drain.Enabled {
		receipt.DrainEvery = dataAPIDrainEvery(drain.Schedule)
	}
	return receipt, nil
}

func printDataAPIHooks(verb string, receipt dataAPIHooksReceipt) {
	switch verb {
	case "disable":
		PrintOK(osStdout, "Change hooks off for %s; recorded changes stay in gregale_change_events until hooks are on again", receipt.App)
		_, _ = fmt.Fprintf(osStdout, "Hooked tables keep recording changes, up to %d, then leave them out; they publish when hooks are on again.\n"+
			"To stop recording, unhook each table in a migration: SELECT gregale_hooks.disable('api.orders');\n"+
			"To drop what waits: SELECT gregale_hooks.discard_waiting();\n", api.DataAPIChangeHooksBacklogMax)
	case "status":
		_, _ = fmt.Fprintf(osStdout, "change hooks  %s\nsource        %s\n", onOff(receipt.Enabled), receipt.Source)
		if receipt.Enabled {
			drain := "off (changes other writers record wait until a request wakes the API)"
			if receipt.DrainEvery != "" {
				drain = "every " + receipt.DrainEvery
			}
			_, _ = fmt.Fprintf(osStdout, "drain         %s\ndelivery      %s\n", drain, dataAPIHooksDelivery(receipt))
		}
	default:
		PrintOK(osStdout, "Change hooks on for %s: recorded changes publish as events from %s", receipt.App, receipt.Source)
		_, _ = fmt.Fprintf(osStdout, `Record changes with a migration: add the table (gregale data-api hooks export), then hook tables:
  SELECT gregale_hooks.enable('api.orders');
Subscribe from any app's gregale.yaml, in commit order per row:
  work_policies:
    - name: order-changes
      max_running_per_key: 1
      pending_updates: all
  event_triggers:
    - source: %s
      type: api.orders.*
      work_policy: order-changes
      work_key: data.key.id
      ordered: true
`, receipt.Source)
	}
	if verb == "enable" {
		if receipt.DrainEvery != "" {
			_, _ = fmt.Fprintf(osStdout, "A platform cron wakes the API every %s to publish changes other writers recorded while it was parked; each run counts as app time.\n", receipt.DrainEvery)
		}
	}
	if verb == "status" && receipt.Enabled && dataAPIHooksBacklogFull(receipt.Delivery) {
		PrintWarn(os.Stderr, "change hooks stop recording at %d waiting changes; changes beyond it are not recorded. Fix publishing (see delivery), or drop what waits with SELECT gregale_hooks.discard_waiting() (gregale data-api hooks export --upgrade adds it to older installations)", api.DataAPIChangeHooksBacklogMax)
	}
	if verb == "status" && receipt.Enabled && receipt.Delivery != nil && receipt.Delivery.LastDroppedAt != nil {
		PrintWarn(os.Stderr, "changes were left out at the backlog bound, most recently at %s; those changes were not recorded and will not publish", receipt.Delivery.LastDroppedAt.UTC().Format(time.RFC3339))
	}
	if verb != "status" && receipt.Parked {
		_, _ = fmt.Fprintln(osStdout, dataAPIParkedSettingsNote)
	} else if verb != "status" && !receipt.Restarted {
		_, _ = fmt.Fprintf(osStdout, "Run gregale data-api refresh %s to apply the settings.\n", receipt.App)
	}
}

// dataAPIHooksBacklogFull reports a backlog at or near the bound where the
// trigger stops recording (it counts identifiers, so it can stop a little
// before the exact count reaches the bound).
func dataAPIHooksBacklogFull(d *api.DataAPIChangeHookStatus) bool {
	return d != nil && d.Pending*100 >= int64(api.DataAPIChangeHooksBacklogMax)*api.DataAPIChangeHooksBacklogWarnPercent
}

// dataAPIHooksDelivery summarizes what the runtime last reported.
func dataAPIHooksDelivery(receipt dataAPIHooksReceipt) string {
	if receipt.RuntimeReports == api.DataAPIRuntimeReportsUnavailable {
		return "not published: this installation does not accept runtime reports yet (workload identity is not configured); changes wait in gregale_change_events"
	}
	d := receipt.Delivery
	if d == nil {
		return "not reported yet (the runtime reports while the API is awake)"
	}
	parts := []string{fmt.Sprintf("%d waiting", d.Pending)}
	if d.OldestPendingAt != nil {
		parts[0] += " (oldest recorded " + d.OldestPendingAt.UTC().Format(time.RFC3339) + ")"
	}
	failed := fmt.Sprintf("%d rejected", d.Failed)
	if d.Failed > 0 && d.LastFailureCode != "" {
		failed += " (" + d.LastFailureCode + "; retry with SELECT gregale_hooks.retry_failed())"
	}
	parts = append(parts, failed)
	if d.Publishing == api.DataAPIChangeHooksPublishingRetrying {
		parts = append(parts, "publications are failing and retrying")
	}
	if d.LastPublishedAt != nil {
		parts = append(parts, "last published "+d.LastPublishedAt.UTC().Format(time.RFC3339))
	}
	return strings.Join(parts, ", ") + "; reported " + d.ReportedAt.UTC().Format(time.RFC3339)
}
