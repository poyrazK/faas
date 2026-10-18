package main

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/mcphosting"
	"os"
	"os/signal"
	"sort"
	"strings"
	"time"
)

func cmdMCPCatalogMonitor(args []string) int {
	if len(args) > 0 && (args[0] == "connect" || args[0] == "reconnect" || args[0] == "disconnect" || args[0] == "provider-check") {
		return cmdMCPMonitorConnect(args)
	}
	if len(args) == 0 {
		return printErr("MCP catalog monitor", errors.New("use mcp monitor setup, disable, status, run, pause, resume, restart, stop, connect, reconnect, disconnect or provider-check"))
	}
	fs := newFlagSet("mcp-monitor-"+args[0], flag.ContinueOnError)
	app := fs.String("app", "", "HTTP MCP app slug")
	scope := fs.String("scope", "", "connection environment (setup only; default when omitted)")
	role := fs.String("role", "default", "reviewed MCP authorization role")
	allRoles := fs.Bool("all-roles", false, "aggregate all configured roles (status only)")
	oauthRevocation := fs.String("oauth-revocation-endpoint", "", "explicit HTTPS RFC 7009 endpoint")
	oauthRevocationAuth := fs.String("oauth-revocation-auth-method", "", "revocation authentication: none, client_secret_basic or client_secret_post")
	oauthProfile := fs.String("oauth-profile", "", "provider profile: generic (default), google or oidc")
	oauthAuthMethod := fs.String("oauth-token-auth-method", "", "none, client_secret_basic or client_secret_post")
	oauthBrowserRedirect := fs.String("oauth-browser-redirect-uri", "", "registered HTTPS console MCP callback URL")
	oauthCallbackPort := fs.Int("oauth-callback-port", 0, "fixed loopback port; 0 chooses a dynamic port")
	oauthResource := fs.String("oauth-resource", "", "reviewed HTTPS MCP resource indicator")
	oauthIssuer := fs.String("oauth-issuer", "", "reviewed HTTPS provider issuer")
	oauthDiscovery := fs.String("oauth-discovery-url", "", "explicit HTTPS provider metadata URL")
	oauthGuided := fs.Bool("oauth-guided", false, "use browser authorization instead of a refresh secret")
	oauthAuthorization := fs.String("oauth-authorization-endpoint", "", "explicit HTTPS browser authorization endpoint")
	oauthScopes := fs.String("oauth-scopes", "", "space-separated OAuth scopes")
	oauthEndpoint := fs.String("oauth-token-endpoint", "", "explicit HTTPS OAuth token endpoint (managed setup)")
	oauthClient := fs.String("oauth-client-id", "", "OAuth client ID (managed setup)")
	oauthRefresh := fs.String("oauth-refresh-secret-ref", "", "app secret holding initial OAuth refresh token")
	oauthSecret := fs.String("oauth-client-secret-ref", "", "optional app secret for confidential client authentication")
	oauthRequested := func() bool {
		return *oauthBrowserRedirect != "" || *oauthRevocation != "" || *oauthRevocationAuth != "" || *oauthProfile != "" || *oauthAuthMethod != "" || *oauthCallbackPort != 0 || *oauthResource != "" || *oauthIssuer != "" || *oauthDiscovery != "" || *oauthGuided || *oauthAuthorization != "" || *oauthScopes != "" || *oauthEndpoint != "" || *oauthClient != "" || *oauthRefresh != "" || *oauthSecret != ""
	}
	managed := fs.Bool("managed", false, "run a platform-managed observer (setup only)")
	secretRef := fs.String("secret-ref", "", "app environment secret:NAME for the managed MCP token (setup only)")
	baseline := fs.String("baseline", "", "reviewed format-2 catalog baseline (setup only)")
	freshness := fs.Int("freshness-seconds", api.MCPCatalogMonitorDefaultFreshnessSeconds, "maximum observation age (setup only)")
	tokenEnv := fs.String("token-env", "", "MCP client token environment variable name (setup/run)")
	endpoint := fs.String("endpoint", "/mcp", "MCP endpoint path (managed setup/run)")
	interval := fs.Duration("interval", 30*time.Second, "catalog check interval (managed setup/run)")
	timeout := fs.Duration("timeout", 10*time.Second, "per-check read and report deadline")
	if err := fs.Parse(args[1:]); err != nil {
		return 1
	}
	if api.ValidateScope(api.MCPConnectionScope(*scope)) != nil || !api.ValidCatalogMonitorRole(*role) || !api.ValidCatalogMonitorTokenEnv(*tokenEnv) || (*allRoles && args[0] != "status") || (*allRoles && *role != "default") || fs.NArg() != 0 || !api.ValidAppSlug(*app) || *timeout <= 0 || *timeout > 2*time.Minute {
		return printErr("Invalid MCP monitor flags", errors.New("supply --app and a positive --timeout up to 2m"))
	}
	switch args[0] {
	case "setup", "disable", "status", "run", "pause", "resume", "restart", "stop":
	default:
		return printErr("MCP catalog monitor", errors.New("use setup, disable, status, run, pause, resume, restart, stop, connect, reconnect, disconnect or provider-check"))
	}
	if args[0] != "setup" && (*scope != "" || *managed || *secretRef != "" || oauthRequested()) {
		return printErr("Invalid MCP monitor flags", errors.New("--managed, --secret-ref and OAuth flags apply to setup"))
	}
	if (*secretRef != "" || oauthRequested()) && !*managed {
		return printErr("MCP monitor secret", errors.New("credential references and OAuth flags require --managed"))
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	if args[0] == "setup" {
		contract, err := readMCPContract(*baseline)
		if err != nil || contract.Version != 2 {
			return printErr("MCP monitor baseline", errors.New("supply a reviewed format-2 --baseline"))
		}
		raw, err := mcphosting.MarshalContract(contract)
		if err != nil {
			return printErr("MCP monitor baseline", errors.New("invalid baseline"))
		}
		readCtx, cancel := context.WithTimeout(ctx, *timeout)
		defer cancel()
		req := api.MCPCatalogMonitorConfigRequest{Scope: *scope, Role: *role, TokenEnv: *tokenEnv, Enabled: true, Baseline: raw, FreshnessSeconds: *freshness}
		if *managed {
			if *interval%time.Second != 0 || *timeout%time.Second != 0 {
				return printErr("MCP managed observer", errors.New("use whole-second intervals and timeouts"))
			}
			req.Observer = &api.MCPCatalogObserverConfig{Enabled: true, IntervalSeconds: int(*interval / time.Second), TimeoutSeconds: int(*timeout / time.Second), Endpoint: *endpoint, SecretRef: *secretRef}
			if oauthRequested() {
				req.Observer.OAuth = &api.MCPCatalogObserverOAuthConfig{RevocationEndpoint: *oauthRevocation, RevocationAuthMethod: *oauthRevocationAuth, Profile: *oauthProfile, TokenAuthMethod: *oauthAuthMethod, BrowserRedirectURI: *oauthBrowserRedirect, CallbackPort: *oauthCallbackPort, Resource: *oauthResource, Issuer: *oauthIssuer, DiscoveryURL: *oauthDiscovery, Guided: *oauthGuided, AuthorizationEndpoint: *oauthAuthorization, Scopes: strings.Fields(*oauthScopes), TokenEndpoint: *oauthEndpoint, ClientID: *oauthClient, RefreshTokenSecretRef: *oauthRefresh, ClientSecretRef: *oauthSecret}
			}
			if problem := req.Observer.NormalizeAndValidate(*freshness, *tokenEnv); problem != nil {
				return printErr("MCP managed observer", errors.New(problem.Detail))
			}
		}
		out, err := client.ConfigureMCPCatalogMonitor(readCtx, *app, req)
		if err != nil {
			return printErr("MCP monitor setup", errors.New("could not save monitor configuration"))
		}
		return writeMCPCatalogMonitor(out)
	}
	readCtx, cancel := context.WithTimeout(ctx, *timeout)
	if *allRoles {
		out, err := client.GetMCPCatalogMonitorSummary(readCtx, *app)
		cancel()
		if err != nil {
			return printErr("MCP catalog monitor", errors.New("could not read role summary"))
		}
		if jsonOutput {
			return jsonOut(writeJSON(out))
		}
		_, _ = fmt.Fprintf(osStdout, "MCP catalog monitor: %s (%d configured roles)\n", out.State, len(out.Roles))
		names := make([]string, 0, len(out.Roles))
		for name := range out.Roles {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			writeMCPCatalogMonitor(out.Roles[name])
		}
		return 0
	}
	snapshot, err := client.GetMCPCatalogMonitorRole(readCtx, *app, *role)
	cancel()
	if err != nil {
		return printErr("MCP catalog monitor", errors.New("could not read monitor configuration"))
	}
	if args[0] == "disable" || args[0] == "stop" || args[0] == "pause" || args[0] == "resume" || args[0] == "restart" {
		req, err := managedMonitorAction(snapshot.Config, args[0])
		if err != nil {
			return printErr("MCP monitor lifecycle", err)
		}
		readCtx, cancel := context.WithTimeout(ctx, *timeout)
		defer cancel()
		out, err := client.ConfigureMCPCatalogMonitor(readCtx, *app, req)
		if err != nil {
			return printErr("MCP monitor lifecycle", errors.New("could not update monitor lifecycle"))
		}
		return writeMCPCatalogMonitor(out)
	}
	if args[0] == "status" {
		return writeMCPCatalogMonitor(snapshot)
	}
	if snapshot.Config.Observer != nil && snapshot.Config.Observer.Enabled {
		return printErr("MCP monitor run", errors.New("this role has a managed observer; use pause, resume, restart or stop"))
	}
	if !snapshot.Config.Enabled || *interval <= 0 || *interval+2*(*timeout) >= time.Duration(snapshot.Config.FreshnessSeconds)*time.Second {
		return printErr("MCP monitor interval", errors.New("enable the monitor and keep interval plus two timeouts below its freshness limit"))
	}
	readCtx, cancel = context.WithTimeout(ctx, *timeout)
	resolved, err := mcpEndpoint(readCtx, "", *app, *endpoint)
	cancel()
	if err != nil {
		return printErr("MCP monitor endpoint", errors.New("could not resolve the app endpoint"))
	}
	selectedEnv, err := monitorTokenEnv(snapshot.Config, *tokenEnv)
	if err != nil {
		return printErr("MCP monitor token", err)
	}
	token, err := mcpToken(selectedEnv)
	if err != nil {
		return printErr("MCP monitor token", errors.New("could not load the selected MCP token environment"))
	}
	err = runMCPCatalogMonitor(ctx, client, *app, resolved, token, snapshot.Config, *interval, *timeout)
	if errors.Is(err, context.Canceled) && ctx.Err() != nil {
		return 0
	}
	if err != nil {
		return printErr("MCP catalog monitor", errors.New("monitor reporting stopped; check connectivity and reload the configured baseline revision"))
	}
	return 0
}
func runMCPCatalogMonitor(ctx context.Context, platform *Client, slug, endpoint, token string, config api.MCPCatalogMonitorConfig, interval, timeout time.Duration) error {
	contract, _, err := readMonitorBaseline(config)
	if err != nil {
		return err
	}
	lastID := ""
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		checkCtx, cancel := context.WithTimeout(ctx, timeout)
		observed, probeErr := observeMCPCatalog(checkCtx, endpoint, token, contract.ProtocolVersion)
		cancel()
		reportCtx, reportCancel := context.WithTimeout(ctx, timeout)
		req := api.MCPCatalogObservationRequest{Role: config.Role, Revision: config.Revision, Observed: observed, Unavailable: probeErr != nil}
		receipt, err := platform.ReportMCPCatalogObservation(reportCtx, slug, req)
		reportCancel()
		if err != nil {
			return err
		}
		if receipt.ID != lastID {
			if jsonOutput {
				if err := writeJSON(receipt); err != nil {
					return err
				}
			} else {
				_, err := fmt.Fprintf(osStdout, "MCP catalog monitor [%s]: %s (%d changes, evidence %s)\n", api.CatalogMonitorRole(receipt.Role), receipt.State, receipt.ChangedCount, receipt.ID)
				if err != nil {
					return err
				}
			}
			lastID = receipt.ID
		}
		timer := time.NewTimer(interval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}
func observeMCPCatalog(ctx context.Context, endpoint, token, version string) ([]byte, error) {
	return mcphosting.ObserveCatalog(ctx, endpoint, token, version, nil)
}
func writeMCPCatalogMonitor(snapshot api.MCPCatalogMonitorSnapshot) int {
	if jsonOutput {
		return jsonOut(writeJSON(snapshot))
	}
	_, _ = fmt.Fprintf(osStdout, "MCP catalog monitor [%s]: %s (revision %s, freshness %ds)\n", api.CatalogMonitorRole(snapshot.Config.Role), snapshot.State, snapshot.Config.Revision, snapshot.Config.FreshnessSeconds)
	if observer := snapshot.ObserverStatus; observer != nil {
		_, _ = fmt.Fprintf(osStdout, "  Observer: %s (%d consecutive failures)\n", observer.State, observer.ConsecutiveFailures)
		if observer.LastSuccessAt != nil {
			_, _ = fmt.Fprintf(osStdout, "  Last successful check: %s\n", observer.LastSuccessAt.Format(time.RFC3339))
		}
		if observer.NextCheckAt != nil {
			_, _ = fmt.Fprintf(osStdout, "  Next check: %s\n", observer.NextCheckAt.Format(time.RFC3339))
		}
		if renewal := observer.Renewal; renewal != nil {
			_, _ = fmt.Fprintf(osStdout, "  OAuth renewal recovery: %s (started %s)\n", renewal.State, renewal.StartedAt.Format(time.RFC3339))
			if renewal.State == "reauthorization_required" {
				_, _ = fmt.Fprintln(osStdout, "  Renewal outcome unknown or grant rejected: reconnect this role")
			}
		}
		if oauth := observer.OAuth; oauth != nil {
			if oauth.ReauthorizationNeeded {
				_, _ = fmt.Fprintln(osStdout, "  Reauthorization needed: reconnect this role")
			}
			_, _ = fmt.Fprintf(osStdout, "  OAuth connection: %s\n", oauth.State)
			if len(oauth.GrantedScopes) > 0 {
				_, _ = fmt.Fprintf(osStdout, "  Granted scopes: %s\n", strings.Join(oauth.GrantedScopes, " "))
			}
			if oauth.ExpiresAt != nil {
				_, _ = fmt.Fprintf(osStdout, "  Token expires: %s\n", oauth.ExpiresAt.Format(time.RFC3339))
			}
			if oauth.LastRenewalAt != nil {
				_, _ = fmt.Fprintf(osStdout, "  Last renewal: %s\n", oauth.LastRenewalAt.Format(time.RFC3339))
			}
		}
		if observer.FailureReason != "" {
			_, _ = fmt.Fprintf(osStdout, "  Failure reason: %s\n", observer.FailureReason)
		}
	}
	if lifecycle := snapshot.OAuthLifecycle; lifecycle != nil {
		if lifecycle.CredentialState != "" {
			_, _ = fmt.Fprintf(osStdout, "  OAuth credential recovery: %s\n", lifecycle.CredentialState)
		}
		_, _ = fmt.Fprintf(osStdout, "  Local OAuth state: %s\n", lifecycle.LocalState)
		if lifecycle.Revocation != nil {
			_, _ = fmt.Fprintf(osStdout, "  Provider revocation: %s (%d attempts)\n", lifecycle.Revocation.State, lifecycle.Revocation.Attempts)
		}
		for _, event := range lifecycle.History {
			_, _ = fmt.Fprintf(osStdout, "  OAuth event: %s %s\n", event.At.Format(time.RFC3339), event.Kind)
		}
	}
	for _, receipt := range snapshot.History {
		_, _ = fmt.Fprintf(osStdout, "  %s: %s (%d changes, evidence %s)\n", receipt.CapturedAt.Format(time.RFC3339), receipt.State, receipt.ChangedCount, receipt.ID)
	}
	return 0
}

func readMonitorBaseline(config api.MCPCatalogMonitorConfig) (mcphosting.Contract, []byte, error) {
	contract, err := mcphosting.ReadContract(bytes.NewReader(config.Baseline))
	if err != nil || contract.Version != 2 {
		return contract, nil, errors.New("invalid monitor baseline")
	}
	raw, err := mcphosting.MarshalContract(contract)
	return contract, raw, err
}

func monitorTokenEnv(config api.MCPCatalogMonitorConfig, override string) (string, error) {
	if config.TokenEnv != "" {
		if override != "" && override != config.TokenEnv {
			return "", errors.New("token environment must match the configured role reference; reconfigure to change it")
		}
		return config.TokenEnv, nil
	}
	// Existing default-role monitors allowed a run-time token selection.
	if api.CatalogMonitorRole(config.Role) != "default" && override != "" {
		return "", errors.New("configure the role token environment reference before running")
	}
	return override, nil
}

func managedMonitorAction(config api.MCPCatalogMonitorConfig, action string) (api.MCPCatalogMonitorConfigRequest, error) {
	req := api.MCPCatalogMonitorConfigRequest{Scope: config.Scope, Role: config.Role, TokenEnv: config.TokenEnv, Enabled: config.Enabled, Baseline: config.Baseline, FreshnessSeconds: config.FreshnessSeconds}
	if config.Observer != nil {
		observer := *config.Observer
		req.Observer = &observer
	}
	if action != "disable" && req.Observer == nil {
		return req, errors.New("this role has no managed observer")
	}
	switch action {
	case "disable", "stop":
		req.Enabled = false
		if req.Observer != nil {
			req.Observer.Enabled = false
			req.Observer.Paused = false
		}
	case "pause":
		req.Enabled = false
		req.Observer.Enabled = true
		req.Observer.Paused = true
	case "resume", "restart":
		req.Enabled = true
		req.Observer.Enabled = true
		req.Observer.Paused = false
	default:
		return req, errors.New("unknown managed observer action")
	}
	return req, nil
}
