// adr: 20261016151947 — Data API live reads: live queries on the Data API's
// database, usage reporting (which carries the Data API's token settings and
// public-read allowlist to realtime) and a realtime endpoint on the Data API
// app whose connections carry live frames only.
package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"slices"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
)

// dataAPILiveReceipt is what data-api live enable and status report.
type dataAPILiveReceipt struct {
	App            string   `json:"app"`
	Scope          string   `json:"scope"`
	Database       string   `json:"database"`
	DatabaseID     string   `json:"database_id"`
	LiveQueries    bool     `json:"live_queries"`
	UsageReporting bool     `json:"usage_reporting"`
	EndpointID     string   `json:"endpoint_id,omitempty"`
	EndpointAuth   string   `json:"endpoint_auth_mode,omitempty"`
	URL            string   `json:"url,omitempty"`
	Ready          bool     `json:"ready"`
	Changed        []string `json:"changed"`
	Warnings       []string `json:"warnings,omitempty"`
	Restarted      bool     `json:"restarted,omitempty"`
}

func cmdDataAPILive(args []string) int {
	if len(args) == 0 {
		PrintUsage(os.Stderr, "usage: gregale data-api live <enable|status> NAME [--scope SCOPE]", "data-api")
		return 1
	}
	switch args[0] {
	case "enable":
		return cmdDataAPILiveRun(args[1:], true)
	case "status":
		return cmdDataAPILiveRun(args[1:], false)
	default:
		printCommandValidation(os.Stderr, "unknown data-api live subcommand %q\n", args[0])
		return unknownSubcommandChoices(os.Stderr, "data-api live", nestedCliCommand("data-api", "live"), args[0])
	}
}

func cmdDataAPILiveRun(args []string, enable bool) int {
	name := "status"
	if enable {
		name = "enable"
	}
	fs := newFlagSet("data-api live "+name, flag.ContinueOnError)
	scope := fs.String("scope", api.DefaultEnvScope, "environment scope of the Data API's database binding")
	noRestart := fs.Bool("no-restart", false, "store settings without requesting a fresh restart")
	if err := parseInterspersed(fs, args); err != nil || fs.NArg() != 1 || !api.ValidAppSlug(fs.Arg(0)) || api.ValidateScope(*scope) != nil || (!enable && *noRestart) {
		PrintUsage(os.Stderr, "usage: gregale data-api live enable NAME [--scope SCOPE] [--no-restart] | gregale data-api live status NAME [--scope SCOPE]", "data-api")
		return 1
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	ctx := context.Background()
	app, err := client.GetApp(ctx, fs.Arg(0))
	if err != nil {
		return printErr("Could not load the Data API app", err)
	}
	receipt, err := readDataAPILive(ctx, client, app, *scope)
	if err != nil {
		return printErr("Could not read live-read settings", err)
	}
	if enable {
		if err := enableDataAPILive(ctx, client, app, &receipt); err != nil {
			return printErr("Could not enable live reads", err)
		}
		if slices.Contains(receipt.Changed, "usage_reporting") && !*noRestart {
			if _, err := client.RestartAppFresh(ctx, app.Slug); err != nil {
				return printErr("Settings stored; fresh restart failed", err)
			}
			receipt.Restarted = true
		}
	}
	if jsonOutput {
		return jsonOut(writeJSON(receipt))
	}
	renderDataAPILive(receipt, enable)
	return 0
}

// readDataAPILive finds the Data API's database from its data_api binding and
// reads each part live reads need.
func readDataAPILive(ctx context.Context, client *api.Client, app api.AppResponse, scope string) (dataAPILiveReceipt, error) {
	receipt := dataAPILiveReceipt{App: app.Slug, Scope: scope, Changed: []string{}}
	inventory, err := client.GetAppBindingInventory(ctx, app.Slug, scope)
	if err != nil {
		return receipt, err
	}
	for _, item := range inventory.Bindings {
		if item.Type == api.BindingTypePostgres && item.Access == "data_api" && item.Scope == scope {
			if receipt.Database != "" && receipt.Database != item.Name {
				return receipt, errors.New("the app has Data API bindings to more than one database in this scope")
			}
			receipt.Database = item.Name
		}
	}
	if receipt.Database == "" {
		return receipt, fmt.Errorf("%s has no Data API database binding in scope %s; live reads need the data_api access of gregale data-api create", app.Slug, scope)
	}
	database, err := resolveManagedPostgresDatabase(ctx, client, receipt.Database)
	if err != nil {
		return receipt, err
	}
	receipt.DatabaseID = database.ID
	live, err := client.GetManagedPostgresLiveQueries(ctx, database.ID)
	var problem *api.APIError
	if errors.As(err, &problem) && problem.Problem.Status == http.StatusNotFound {
		// The database exists, so a 404 is the live-query rollout gate.
		return receipt, errors.New("live queries are not available to this account yet")
	}
	if err != nil {
		return receipt, err
	}
	receipt.LiveQueries = live.Enabled
	secrets, err := client.ListSecretsWithScope(ctx, app.Slug, scope)
	if err != nil {
		return receipt, err
	}
	for _, secret := range secrets.Secrets {
		if secret.Key == dataAPIUsageURLKey && (secret.Scope == "" || secret.Scope == scope) {
			receipt.UsageReporting = true
		}
	}
	endpoints, err := client.ListManagedRealtimeEndpoints(ctx, app.Slug)
	if err != nil {
		return receipt, err
	}
	for _, endpoint := range endpoints {
		// A live-only endpoint (auth mode none) serves Data API reads: its
		// connections never call the app, which answers no realtime callbacks.
		if receipt.EndpointID == "" || (endpoint.AuthMode == api.RealtimeAuthModeNone && receipt.EndpointAuth != api.RealtimeAuthModeNone) {
			receipt.EndpointID, receipt.EndpointAuth = endpoint.ID, endpoint.AuthMode
		}
	}
	if receipt.EndpointID != "" {
		receipt.URL = dataAPILiveURL(app, receipt.EndpointID)
		if receipt.EndpointAuth != api.RealtimeAuthModeNone {
			receipt.Warnings = append(receipt.Warnings, fmt.Sprintf("realtime endpoint %s uses auth mode %s: its connections ask the app to accept them, and a Data API does not answer, so live reads need auth mode none (gregale realtime update %s %s --auth-mode none)", receipt.EndpointID, receipt.EndpointAuth, app.Slug, receipt.EndpointID))
		}
	}
	receipt.Ready = receipt.LiveQueries && receipt.UsageReporting && receipt.EndpointAuth == api.RealtimeAuthModeNone
	return receipt, nil
}

// enableDataAPILive turns on whatever readDataAPILive found missing.
func enableDataAPILive(ctx context.Context, client *api.Client, app api.AppResponse, receipt *dataAPILiveReceipt) error {
	if !receipt.LiveQueries {
		// The issued live secret seals backend queries; Data API reads do not
		// use it, so it is not stored. postgres live rotate --app issues one to keep.
		if _, err := client.EnableManagedPostgresLiveQueries(ctx, receipt.DatabaseID); err != nil {
			return err
		}
		receipt.LiveQueries = true
		receipt.Changed = append(receipt.Changed, "live_queries")
	}
	if !receipt.UsageReporting {
		origin, ok := dataAPIUsageURL(client.BaseURL())
		if !ok {
			return errors.New("live reads need usage reporting, which needs an HTTPS platform API; this CLI targets " + client.BaseURL())
		}
		if err := client.SetSecretWithScope(ctx, app.Slug, dataAPIUsageURLKey, origin, receipt.Scope); err != nil {
			return err
		}
		receipt.UsageReporting = true
		receipt.Changed = append(receipt.Changed, "usage_reporting")
	}
	if receipt.EndpointID == "" {
		callback, enabled := make([]byte, 32), true
		if _, err := rand.Read(callback); err != nil {
			return err
		}
		// The callback token is required but unused: live-only connections
		// never call the app.
		endpoint, err := client.CreateManagedRealtimeEndpoint(ctx, app.Slug, api.CreateManagedRealtimeEndpointRequest{
			CallbackURL: app.URL, CallbackAuthToken: hex.EncodeToString(callback), AuthMode: api.RealtimeAuthModeNone, Enabled: &enabled,
		})
		if err != nil {
			return err
		}
		receipt.EndpointID, receipt.EndpointAuth, receipt.URL = endpoint.ID, endpoint.AuthMode, dataAPILiveURL(app, endpoint.ID)
		receipt.Changed = append(receipt.Changed, "realtime_endpoint")
	}
	receipt.Ready = receipt.LiveQueries && receipt.UsageReporting && receipt.EndpointAuth == api.RealtimeAuthModeNone
	return nil
}

// dataAPILiveURL is the WebSocket URL clients open: the realtime endpoint on
// the app's canonical host.
func dataAPILiveURL(app api.AppResponse, endpointID string) string {
	base := app.CanonicalURL
	if base == "" {
		base = app.URL
	}
	u, err := url.Parse(base)
	if err != nil || u.Host == "" {
		return ""
	}
	return "wss://" + u.Host + "/__gregale/realtime/" + url.PathEscape(endpointID)
}

func renderDataAPILive(r dataAPILiveReceipt, enabled bool) {
	if enabled && len(r.Changed) > 0 {
		PrintOK(osStdout, "Live reads set up for %s (%s)", r.App, strings.Join(r.Changed, ", "))
	}
	_, _ = fmt.Fprintf(osStdout, "database          %s (%s)\n", r.Database, r.DatabaseID)
	_, _ = fmt.Fprintf(osStdout, "live queries      %s\n", onOff(r.LiveQueries))
	_, _ = fmt.Fprintf(osStdout, "usage reporting   %s\n", onOff(r.UsageReporting))
	if r.EndpointID != "" {
		_, _ = fmt.Fprintf(osStdout, "realtime endpoint %s (auth %s)\n", r.EndpointID, r.EndpointAuth)
		_, _ = fmt.Fprintf(osStdout, "url               %s\n", r.URL)
	} else {
		_, _ = fmt.Fprintln(osStdout, "realtime endpoint none")
	}
	for _, warning := range r.Warnings {
		PrintWarn(osStderr, "%s", warning)
	}
	switch {
	case !r.Ready:
		_, _ = fmt.Fprintf(osStdout, "Run gregale data-api live enable %s to set up the rest.\n", r.App)
	case enabled:
		if r.Restarted {
			_, _ = fmt.Fprintln(osStdout, "The Data API restarts to report its settings; live reads without a token work once it has.")
		}
		_, _ = fmt.Fprintf(osStdout, "\nIn the browser:\n  const connection = new LiveConnection({ connect: liveSocket('%s') })\n  connection.query({ database: '%s', token: () => session.accessToken, ...liveRead(data.from('messages').select('id, body')) })\n", r.URL, r.DatabaseID)
	}
}

func onOff(on bool) string {
	if on {
		return "on"
	}
	return "off"
}
