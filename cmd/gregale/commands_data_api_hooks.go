// adr: 20261022121642 — Data API change hooks: the change event SQL and the
// runtime switch that publishes recorded changes as events.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"

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
	return strings.ReplaceAll(string(content), "{{schema}}", schema), nil
}

func cmdDataAPIHooksSQL(args []string) int {
	fs := newFlagSet("data-api hooks sql", flag.ContinueOnError)
	schema := fs.String("schema", "api", "the Data API's first exposed schema")
	if err := parseInterspersed(fs, args); err != nil || fs.NArg() != 0 {
		PrintUsage(os.Stderr, "usage: gregale data-api hooks sql [--schema api]", "data-api")
		return 1
	}
	sql, err := dataAPIHooksSQL(*schema)
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
	usage := fmt.Sprintf("usage: gregale data-api hooks %s NAME [--scope SCOPE] [--no-restart]", verb)
	if verb == "status" {
		usage = "usage: gregale data-api hooks status NAME [--scope SCOPE]"
	}
	if err := parseInterspersed(fs, args); err != nil || fs.NArg() != 1 || !api.ValidAppSlug(fs.Arg(0)) || api.ValidateScope(*scope) != nil || (verb == "status" && *noRestart) {
		PrintUsage(os.Stderr, usage, "data-api")
		return 1
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	ctx := context.Background()
	var receipt dataAPIHooksReceipt
	switch verb {
	case "enable":
		receipt, err = enableDataAPIHooks(ctx, client, fs.Arg(0), *scope, !*noRestart)
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
func enableDataAPIHooks(ctx context.Context, client *api.Client, slug, scope string, restart bool) (dataAPIHooksReceipt, error) {
	origin, ok := dataAPIUsageURL(client.BaseURL())
	if !ok {
		return dataAPIHooksReceipt{}, errors.New("change hooks need an HTTPS platform API; this CLI targets " + client.BaseURL())
	}
	receipt, err := dataAPIHooksStatus(ctx, client, slug, scope)
	if err != nil {
		return receipt, err
	}
	if err := client.SetSecretWithScope(ctx, slug, dataAPIChangeEventsURLKey, origin, scope); err != nil {
		return receipt, err
	}
	receipt.Enabled, receipt.Changed = true, []string{"change_hooks"}
	return receipt, restartDataAPIHooks(ctx, client, &receipt, restart)
}

func disableDataAPIHooks(ctx context.Context, client *api.Client, slug, scope string, restart bool) (dataAPIHooksReceipt, error) {
	receipt, err := dataAPIHooksStatus(ctx, client, slug, scope)
	if err != nil {
		return receipt, err
	}
	if err := unsetDataAPISetting(ctx, client, slug, dataAPIChangeEventsURLKey, scope); err != nil {
		return receipt, err
	}
	receipt.Enabled, receipt.Changed = false, []string{"change_hooks"}
	return receipt, restartDataAPIHooks(ctx, client, &receipt, restart)
}

func restartDataAPIHooks(ctx context.Context, client *api.Client, receipt *dataAPIHooksReceipt, restart bool) error {
	if !restart {
		return nil
	}
	if _, err := client.RestartAppFresh(ctx, receipt.App); err != nil {
		return fmt.Errorf("settings stored; fresh restart failed: %w", err)
	}
	receipt.Restarted = true
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
	return receipt, nil
}

func printDataAPIHooks(verb string, receipt dataAPIHooksReceipt) {
	switch {
	case verb == "disable":
		PrintOK(osStdout, "Change hooks off for %s; recorded changes stay in gregale_change_events until hooks are on again", receipt.App)
	case verb == "status":
		_, _ = fmt.Fprintf(osStdout, "change hooks  %s\nsource        %s\n", onOff(receipt.Enabled), receipt.Source)
	default:
		PrintOK(osStdout, "Change hooks on for %s: recorded changes publish as events from %s", receipt.App, receipt.Source)
		_, _ = fmt.Fprintf(osStdout, `Record changes with a migration: add the table (gregale data-api hooks sql), then hook tables:
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
	if verb != "status" && !receipt.Restarted {
		_, _ = fmt.Fprintf(osStdout, "Run gregale data-api refresh %s to apply the settings.\n", receipt.App)
	}
}
