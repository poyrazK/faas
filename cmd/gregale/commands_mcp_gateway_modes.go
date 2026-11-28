package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/mcphosting"
)

// mcpVerificationCredential names what --token-env must hold for a mode.
func mcpVerificationCredential(mode string) string {
	switch mode {
	case mcphosting.AuthAPIKey:
		return "consumer key (ck_…) with the server's mcp: scopes"
	case mcphosting.AuthOAuthProxy:
		return "client access token for this server from its OAuth issuer"
	default:
		return "client access token"
	}
}

// syncExternalOAuthGatewayPolicy stores an external-oauth server's gateway
// policy after a successful deploy, so Gregale also checks its tokens and
// knows who calls it: OAuth callers then become customers and are metered
// (ADR-20261124094745). The server keeps checking tokens itself, so a
// failure only warns; `gregale mcp policy` retries it.
func syncExternalOAuthGatewayPolicy(ctx context.Context, c *Client, path, slug string, cfg mcphosting.Config) {
	if cfg.Auth.Mode != mcphosting.AuthExternalOAuth {
		return
	}
	rule, err := applyMCPGatewayPolicy(ctx, c, slug, cfg, []string{"RS256", "ES256"})
	if err != nil {
		fmt.Fprintf(os.Stderr, "warning: Gregale does not check this server's tokens (%v); callers are served but cannot become customers. "+
			"Fix it, then run `gregale mcp policy --path %s --name %s`\n", err, path, slug)
		return
	}
	fmt.Fprintf(os.Stderr, "Gateway checks OAuth tokens for %s (edge rule %s)\n", slug, rule.ID)
}

// requireMCPGatewayPolicy refuses to deploy an api-key or oauth-proxy server
// until its gateway policy is stored and matches the source: in these modes
// the server itself admits every request, so without the policy it would
// serve anyone (ADR-20261122194324).
func requireMCPGatewayPolicy(ctx context.Context, c *Client, slug string, cfg mcphosting.Config) error {
	if !cfg.Auth.GatewayOnly() {
		return nil
	}
	enable := fmt.Sprintf("run `gregale mcp server auth enable --mode %s --app %s` first", cfg.Auth.Mode, slug)
	app, err := c.GetApp(ctx, slug)
	if err != nil {
		var problem *api.Problem
		if errors.As(err, &problem) && problem.Status == 404 {
			return fmt.Errorf("%s needs its gateway policy before the first deploy; create the app and %s", cfg.Auth.Mode, enable)
		}
		return err
	}
	endpoint, err := cfg.URL(canonicalAppURL(app))
	if err != nil {
		return err
	}
	if endpoint != cfg.Auth.Resource {
		return fmt.Errorf("auth.resource must match the canonical endpoint %s", endpoint)
	}
	rules, err := c.ListCoveringEdgeRulesForApp(ctx, slug)
	if err != nil {
		return fmt.Errorf("could not read the app's gateway rules: %w", err)
	}
	_, checks := auditMCPGatewayRules(app.ID, cfg, mcpAccessAuditOptions{AuthPlacement: "gateway", Algorithms: []string{"ES256"}}, c.BaseURL(), rules, time.Now(), nil)
	for _, check := range checks {
		if check.Status != "passed" {
			return fmt.Errorf("stored gateway policy does not match the source (%s); %s", check.Name, enable)
		}
	}
	return nil
}

// cmdMCPAuth is `gregale mcp server auth enable` (ADR-20261122194324).
func cmdMCPAuth(args []string) int {
	if len(args) == 0 || args[0] != "enable" {
		PrintUsage(os.Stderr, "usage: gregale mcp server auth enable --mode api-key|oauth-proxy --app SLUG [--path DIR] [--scopes A,B] [--auth-app NAME | --issuer URL [--audience AUD]] [--dry-run]", "mcp")
		return 2
	}
	fs := newFlagSet("mcp-auth-enable", flag.ContinueOnError)
	mode := fs.String("mode", "", "api-key or oauth-proxy")
	slug := fs.String("app", "", "existing app that serves the MCP server")
	path := fs.String("path", ".", "MCP source directory")
	scopes := fs.String("scopes", "", "comma-separated endpoint scopes")
	authApp := fs.String("auth-app", "", "Gregale Auth app that signs callers in (oauth-proxy)")
	issuerURL := fs.String("issuer", "", "OpenID Connect issuer that signs callers in, instead of a Gregale Auth app (oauth-proxy)")
	audience := fs.String("audience", "", "aud claim the issuer puts in access tokens when it is not the endpoint URL (oauth-proxy with --issuer)")
	dryRun := fs.Bool("dry-run", false, "print the change without writing it")
	if err := fs.Parse(args[1:]); err != nil {
		return 1
	}
	if fs.NArg() != 0 || *slug == "" {
		return printErr("MCP auth", errors.New("--app is required and positional arguments are not accepted"))
	}
	if (*mode == mcphosting.AuthOAuthProxy) != (*authApp != "" || *issuerURL != "") || (*authApp != "" && *issuerURL != "") {
		return printErr("MCP auth", errors.New("--mode oauth-proxy needs exactly one of --auth-app or --issuer; other modes take neither"))
	}
	if *audience != "" && *issuerURL == "" {
		return printErr("MCP auth", errors.New("--audience goes with --issuer"))
	}
	dir, err := filepath.Abs(*path)
	if err != nil {
		return printErr("MCP source", err)
	}
	cfg, err := mcphosting.Load(dir)
	if err != nil {
		return printErr("MCP configuration", err)
	}
	c, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	app, err := c.GetApp(ctx, *slug)
	if err != nil {
		return printErr("MCP auth", fmt.Errorf("find the app (create it first; the policy must exist before the first deploy): %w", err))
	}
	endpoint, err := cfg.URL(canonicalAppURL(app))
	if err != nil {
		return printErr("MCP auth", err)
	}
	var issuer mcpAuthIssuer
	switch {
	case *authApp != "":
		resolved, required, err := mcpAuthApp(*authApp, *scopes)
		if err != nil {
			return printErr("MCP auth", err)
		}
		issuer.url, *scopes = resolved.Issuer, required
	case *issuerURL != "":
		jwks, err := mcpDiscoverJWKS(ctx, *issuerURL)
		if err != nil {
			return printErr("MCP auth", err)
		}
		issuer = mcpAuthIssuer{url: *issuerURL, jwksURL: jwks, audience: *audience}
	}
	if err := mcpAuthEnableConfig(&cfg, *mode, endpoint, issuer, *scopes); err != nil {
		return printErr("MCP auth", err)
	}
	if *mode == mcphosting.AuthAPIKey && app.ConsumerAuthMode != api.ConsumerAuthModeOptional && app.ConsumerAuthMode != api.ConsumerAuthModeRequired {
		return printErr("MCP auth", fmt.Errorf("api-key needs consumer keys on %s; set its consumer auth mode to optional or required", *slug))
	}
	action := mcpExpectedGatewayAction(cfg, []string{"ES256"}, c.BaseURL())
	if *dryRun {
		return jsonOut(writeJSON(map[string]any{"auth": cfg.Auth, "gateway_action": action, "written": false}))
	}
	body, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return printErr("MCP configuration", err)
	}
	// The policy goes first: until it is stored the server would serve
	// anyone, so a failed write leaves the source unchanged.
	rule, err := applyMCPGatewayPolicy(ctx, c, *slug, cfg, []string{"ES256"})
	if err != nil {
		return printErr("MCP gateway policy", err)
	}
	if err := os.WriteFile(filepath.Join(dir, mcphosting.ConfigFile), append(body, '\n'), 0o644); err != nil {
		return printErr("MCP configuration", fmt.Errorf("gateway policy %s is active but %s was not updated: %w", rule.ID, mcphosting.ConfigFile, err))
	}
	if jsonOutput {
		return jsonOut(writeJSON(map[string]any{"auth": cfg.Auth, "rule_id": rule.ID, "written": true, "next": mcpAuthNext(cfg, *slug)}))
	}
	PrintOK(osStdout, "%s access is on for %s (gateway rule %s); %s updated", *mode, endpoint, rule.ID, mcphosting.ConfigFile)
	for _, line := range mcpAuthNext(cfg, *slug) {
		_, _ = fmt.Fprintln(osStdout, "  "+line)
	}
	return 0
}

// mcpAuthIssuer is the issuer of an oauth-proxy server: a Gregale Auth app
// (jwksURL empty) or an external OpenID Connect issuer.
type mcpAuthIssuer struct {
	url, jwksURL, audience string
}

// mcpAuthEnableConfig rewrites cfg.Auth for mode, keeping the reviewed tool,
// resource and prompt allowlists of api-key servers and of oauth-proxy
// servers with an external issuer, which can issue any scope.
func mcpAuthEnableConfig(cfg *mcphosting.Config, mode, endpoint string, issuer mcpAuthIssuer, scopes string) error {
	if cfg.Tasks != nil && cfg.Tasks.Enabled != nil && *cfg.Tasks.Enabled {
		return errors.New("servers with durable Tasks need per-caller ownership; keep external-oauth")
	}
	list := splitMCPScopes(scopes)
	auth := mcphosting.AuthConfig{Mode: mode, Resource: endpoint, ToolLimits: cfg.Auth.ToolLimits}
	switch mode {
	case mcphosting.AuthAPIKey:
		if len(list) == 0 {
			list = cfg.Auth.Scopes
		}
		if len(list) == 0 {
			list = []string{"tools"}
		}
		auth.ToolScopes, auth.ResourceScopes, auth.PromptScopes = cfg.Auth.ToolScopes, cfg.Auth.ResourceScopes, cfg.Auth.PromptScopes
	case mcphosting.AuthOAuthProxy:
		auth.Issuer, auth.JWKSURL, auth.Audience = issuer.url, issuer.jwksURL, issuer.audience
		if issuer.jwksURL != "" {
			if len(list) == 0 {
				list = cfg.Auth.Scopes
			}
			auth.ToolScopes, auth.ResourceScopes, auth.PromptScopes = cfg.Auth.ToolScopes, cfg.Auth.ResourceScopes, cfg.Auth.PromptScopes
			break
		}
		if len(cfg.Auth.ToolScopes)+len(cfg.Auth.ResourceScopes)+len(cfg.Auth.PromptScopes) > 0 {
			return errors.New("oauth-proxy cannot keep tool, resource or prompt scopes: Gregale Auth issues only OpenID scopes; remove them first")
		}
	default:
		return errors.New("--mode must be api-key or oauth-proxy")
	}
	auth.Scopes = list
	cfg.Auth = auth
	return cfg.Validate()
}

func splitMCPScopes(value string) []string {
	var out []string
	for _, scope := range strings.Split(value, ",") {
		if scope = strings.TrimSpace(scope); scope != "" {
			out = append(out, scope)
		}
	}
	return out
}

// mcpAuthNext lists what the owner does after enabling a mode.
func mcpAuthNext(cfg mcphosting.Config, slug string) []string {
	if cfg.Auth.Mode == mcphosting.AuthAPIKey {
		return []string{
			fmt.Sprintf("Give a caller a key: gregale mcp server keys create --app %s --new-consumer CALLER --name NAME", slug),
			"Callers send it as Authorization: Bearer ck_…",
			fmt.Sprintf("Deploy: gregale mcp server deploy --name %s --token-env MCP_CONSUMER_KEY", slug),
		}
	}
	if cfg.Auth.ExternalIssuer() {
		return []string{
			fmt.Sprintf("At %s, issue access tokens with aud %q and the scopes %s.", cfg.Auth.Issuer, cfg.Auth.TokenAudience(), strings.Join(cfg.Auth.Scopes, ", ")),
			"Clients discover the issuer from the server's protected-resource metadata and sign in there.",
			fmt.Sprintf("Deploy: gregale mcp server deploy --name %s --token-env MCP_ACCESS_TOKEN", slug),
		}
	}
	return []string{
		"Clients discover the Auth app from the server's protected-resource metadata and sign in there.",
		fmt.Sprintf("Deploy: gregale mcp server deploy --name %s --token-env MCP_ACCESS_TOKEN", slug),
	}
}
