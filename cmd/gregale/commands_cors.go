// commands_cors.go - CORS improvements D5 (ADR-091 appendix).
//
// `gregale cors allow <slug> <origin...>` attaches a kind=cors edge rule.
// `gregale cors ls <slug>` lists the app's kind=cors rules.
// `gregale cors rm <rule-id>` deletes one rule by id.
// `gregale cors show <slug>` renders the per-app default CORS fields plus
// the active kind=cors rules.
//
// The verb is a thin shim over pkg/api.CreateCORSEdgeRule (the typed
// SDK helper added in Step 7) - it does NOT introduce a parallel wire
// surface. Customers who want the full edge-rule power (priority,
// enable/disable, multi-host matching) still go through
// `gregale edge-rules create --kind cors` directly; the verb here
// targets the "configure-cors-and-stop-thinking-about-it" crowd.
//
// Match host always defaults to the platform subdomain shape
// `<slug>.<tenant-host>`. The CLI has no easy way to read the
// app's verified custom domains (they live on AccountExportResponse,
// not AppResponse), so the helper falls back unconditionally and
// documents --host as the override. The placeholder trips the
// gateway's host-name validator before any rule is persisted, so a
// customer who accepts the placeholder sees a clear
// "host not routable" in their audit log instead of a silent
// misroute. Operators who want the dashboard-driven shape can copy
// the match_host value from the per-app detail page and pass
// --host on the CLI call.
//
// Match methods default to [GET, POST, OPTIONS, PUT, PATCH, DELETE]
// so every common HTTP verb is preflight-allowed out of the box; the
// list is overridable via --method (repeatable) when the customer
// needs a stricter shape. Allow-credentials defaults to false (the
// secure-by-default posture the dashboard uses); --credentials flips
// it. Max-age defaults to 600 (10 min) - the same default the SDK
// helper applies - and is overridable via --max-age.
//
// This file does NOT own the underlying wire shape; that lives in
// pkg/api/client.go (CreateCORSEdgeRule). Keep the two in sync: any
// field added to CreateCORSEdgeRuleOpts grows a --flag here.

package main

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
)

// corsDefaultMethods is the default match_methods list when the
// customer does not pass --method. Mirrors the dashboard's default
// shape so the CLI and the UI land on the same rule for the same
// input. Allowed verbs are restricted to the standard CORS-method set;
// anything else is rejected locally (the server would 422 anyway, but
// failing fast avoids a round-trip).
var corsDefaultMethods = []string{"GET", "POST", "OPTIONS", "PUT", "PATCH", "DELETE"}

// corsRuleKind is the edge-rule kind string the CLI filters for in
// ListEdgeRulesForApp responses. Mirrors state.EdgeRuleKindCORSA but
// stays in this file so the CLI doesn't import pkg/state (the
// CLI is HTTP-only — see cli-is-http-only-not-direct-db). The
// goconst lint wants a named const so a wire-shape regression
// (renaming "cors" to "cors_v2") shows up at compile time on
// the kind comparisons below.
const corsRuleKind = "cors"

// corsAllowedMethods is the closed set of HTTP methods the --method
// flag accepts. Anything else fails fast (local 422-equivalent).
// Mirrors the apid-side gate (EdgeRuleCORSAction.Validate at
// pkg/api/dto.go).
var corsAllowedMethods = map[string]struct{}{
	"GET": {}, "POST": {}, "PUT": {}, "PATCH": {},
	"DELETE": {}, "HEAD": {}, "OPTIONS": {},
}

// cmdCors dispatches the four subcommands. Mirrors cmdEdgeRules
// (commands_edge_rules.go:67-90) so the dispatcher shape is consistent
// across `gregale edge-rules` and `gregale cors`.
func cmdCors(args []string) int {
	parent, _ := lookupCliCommand("cors")
	if len(args) == 0 {
		PrintUsage(os.Stderr,
			"usage: gregale cors <allow|ls|rm|show> [args]",
			"cors")
		return 1
	}
	switch args[0] {
	case "allow":
		return cmdCorsAllow(args[1:])
	case "ls":
		return cmdCorsLs(args[1:])
	case "rm":
		return cmdCorsRm(args[1:])
	case "show":
		return cmdCorsShow(args[1:])
	}
	fmt.Fprintf(os.Stderr, "unknown cors subcommand %q\n", args[0])
	if sug, _ := suggestSubcommand(args[0], parent); sug != "" {
		maybeSuggestSub(sug)
	}
	return 1
}

// cmdCorsAllow attaches a kind=cors edge rule to the slug's app.
//
// Usage:
//
//	gregale cors allow <slug> <origin> [<origin>...]
//	  [--method GET] [--method POST] ...
//	  [--credentials] [--allow-header NAME] [--max-age 600] [--host <match-host>]
//
// Each <origin> creates one rule. Repeated --method flags extend the
// default method set. --credentials flips allow_credentials on every
// created rule (rare; most CORS APIs run without creds). --max-age
// accepts the same int the SDK helper does (0 = use default 600;
// otherwise the server caps at 86400). --host defaults to the
// platform subdomain shape `<slug>.<host>` (always, since
// AppResponse doesn't surface the app's verified custom domains).
func cmdCorsAllow(args []string) int {
	flags, positional := splitArgsForFlags(args)
	if len(positional) < 2 {
		PrintUsage(os.Stderr,
			"usage: gregale cors allow <slug> <origin> [<origin>...] [--method VERB] [--credentials] [--max-age N] [--host HOST]",
			"cors")
		return 1
	}
	slug := positional[0]
	origins := positional[1:]

	methodSet := map[string]struct{}{}
	for _, m := range corsDefaultMethods {
		methodSet[m] = struct{}{}
	}
	fs := flag.NewFlagSet("cors allow", flag.ContinueOnError)
	fs.Func("method", "allowed HTTP method (repeatable; extends the default set)", func(s string) error {
		if _, ok := corsAllowedMethods[s]; !ok {
			return fmt.Errorf("unsupported HTTP method %q; allowed: %s",
				s, sortedAllowedMethods())
		}
		methodSet[s] = struct{}{}
		return nil
	})
	credentials := fs.Bool("credentials", false, "enable Access-Control-Allow-Credentials (rare)")
	var allowedHeaders stringListFlag
	fs.Var(&allowedHeaders, "allow-header", "allowed request header (repeatable; explicit names are required for custom credentialed headers)")
	maxAge := fs.Int("max-age", 600, "Access-Control-Max-Age in seconds (0 = SDK default 600; capped server-side at 86400)")
	host := fs.String("host", "", "match_host override (default: app's first verified custom domain)")
	if err := fs.Parse(flags); err != nil {
		return 1
	}
	for i, header := range allowedHeaders {
		header = strings.TrimSpace(header)
		if !api.CorsHeaderNamePattern.MatchString(header) {
			return printErr("Invalid CORS header", fmt.Errorf("%q is not a valid HTTP header name", header))
		}
		if *credentials && header == "*" {
			return printErr("Invalid CORS header", fmt.Errorf("--credentials requires explicit --allow-header names; wildcard headers are not valid for credentialed browser requests"))
		}
		allowedHeaders[i] = http.CanonicalHeaderKey(header)
	}
	if len(allowedHeaders) == 0 {
		if *credentials {
			allowedHeaders = stringListFlag{"Accept", "Authorization", "Content-Type", "X-Requested-With"}
		} else {
			allowedHeaders = stringListFlag{"*"}
		}
	}

	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}

	matchHost := *host
	if matchHost == "" {
		app, err := client.GetApp(context.Background(), slug)
		if err != nil {
			return printErr("Failed to resolve app", err)
		}
		matchHost = primaryDomainOrFallback(app)
	}

	for _, o := range origins {
		if !api.CorsOriginPattern.MatchString(o) {
			return printErr("Invalid origin", fmt.Errorf(
				"%q does not match the origin grammar (scheme://host[:port], '*', or 'scheme://*.host[:port]' / 'scheme://host:*')", o))
		}
	}

	methods := make([]string, 0, len(methodSet))
	for m := range methodSet {
		methods = append(methods, m)
	}
	sort.Strings(methods)

	opts := api.CreateCORSEdgeRuleOpts{
		MatchHost:        matchHost,
		MatchPath:        "/*",
		MatchMethods:     methods,
		AllowOrigins:     origins,
		AllowMethods:     methods,
		AllowHeaders:     []string(allowedHeaders),
		AllowCredentials: *credentials,
		MaxAgeSeconds:    *maxAge,
	}
	resp, err := client.CreateCORSEdgeRule(context.Background(), slug, opts)
	if err != nil {
		return printErr("Failed to attach CORS rule", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(resp))
	}
	PrintOK(osStdout, "Attached CORS rule to %s.", slug)
	_, _ = fmt.Fprintf(osStdout, "  id:            %s\n", resp.ID)
	_, _ = fmt.Fprintf(osStdout, "  match_host:    %s\n", resp.MatchHost)
	_, _ = fmt.Fprintf(osStdout, "  match_path:    %s\n", resp.MatchPath)
	_, _ = fmt.Fprintf(osStdout, "  allow_origins: %s\n", strings.Join(origins, ", "))
	return 0
}

// cmdCorsLs lists kind=cors rules for the slug's app. The SDK exposes
// ListEdgeRulesForApp (every rule bound to the app). We filter
// client-side by Kind == "cors" because the wire API does not expose
// a kind filter; the volume per app is bounded by the EdgeRulesPerApp
// quota so an in-memory filter stays cheap.
func cmdCorsLs(args []string) int {
	if len(args) < 1 {
		PrintUsage(os.Stderr, "usage: gregale cors ls <slug>", "cors")
		return 1
	}
	slug := args[0]
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	rules, err := client.ListEdgeRulesForApp(context.Background(), slug)
	if err != nil {
		return printErr("Failed to list CORS rules", err)
	}
	cors := make([]api.EdgeRuleResponse, 0, len(rules))
	for _, r := range rules {
		if r.Kind == corsRuleKind {
			cors = append(cors, r)
		}
	}
	if jsonOutput {
		return jsonOut(writeJSON(cors))
	}
	if len(cors) == 0 {
		_, _ = fmt.Fprintln(osStdout, "(no CORS rules)")
		return 0
	}
	_, _ = fmt.Fprintf(osStdout, "CORS rules for %s (%d):\n", slug, len(cors))
	for _, r := range cors {
		_, _ = fmt.Fprintf(osStdout, "  - %s  host=%s path=%s enabled=%t\n",
			r.ID, r.MatchHost, r.MatchPath, r.Enabled)
	}
	return 0
}

// cmdCorsRm removes one rule by id. The CLI does not require the id
// to be tied to a CORS rule specifically - the server enforces
// ownership via the same IDOR-safe path every DeleteEdgeRule call
// uses (404 on foreign-account rule ids; no 403 to avoid leaking
// existence).
func cmdCorsRm(args []string) int {
	if len(args) < 1 {
		PrintUsage(os.Stderr, "usage: gregale cors rm <rule-id>", "cors")
		return 1
	}
	id := args[0]
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	if err := client.DeleteEdgeRule(context.Background(), id); err != nil {
		return printErr("Failed to remove CORS rule", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(map[string]string{"deleted": id}))
	}
	PrintOK(osStdout, "Removed CORS rule %s.", id)
	return 0
}

// cmdCorsShow renders the per-app default-CORS fields (D1) plus the
// list of active kind=cors rules. Mirrors `gregale app <slug>` for
// consistency (the per-app detail page renders the same fields).
// Output is dual-mode: --json dumps a synthetic envelope
// {app:{...}, cors_rules:[...]}, human mode prints the per-app
// defaults first then the rule list.
func cmdCorsShow(args []string) int {
	if len(args) < 1 {
		PrintUsage(os.Stderr, "usage: gregale cors show <slug>", "cors")
		return 1
	}
	slug := args[0]
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	app, err := client.GetApp(context.Background(), slug)
	if err != nil {
		return printErr("Failed to fetch app", err)
	}
	rules, err := client.ListEdgeRulesForApp(context.Background(), slug)
	if err != nil {
		return printErr("Failed to list CORS rules", err)
	}
	cors := make([]api.EdgeRuleResponse, 0, len(rules))
	for _, r := range rules {
		if r.Kind == corsRuleKind {
			cors = append(cors, r)
		}
	}
	if jsonOutput {
		return jsonOut(writeJSON(map[string]any{
			"app":        app,
			"cors_rules": cors,
		}))
	}
	_, _ = fmt.Fprintf(osStdout, "CORS configuration for %s:\n", slug)
	if app.CORSDefaultEnabled != nil && *app.CORSDefaultEnabled {
		_, _ = fmt.Fprintf(osStdout, "  default_enabled: true\n")
		_, _ = fmt.Fprintf(os.Stdout, "  default_origins: %s\n",
			strings.Join(app.CORSDefaultOrigins, ", "))
	} else {
		_, _ = fmt.Fprintf(osStdout, "  default_enabled: false\n")
	}
	_, _ = fmt.Fprintf(osStdout, "  active_rules: %d\n", len(cors))
	for _, r := range cors {
		_, _ = fmt.Fprintf(osStdout, "    - %s  host=%s path=%s enabled=%t\n",
			r.ID, r.MatchHost, r.MatchPath, r.Enabled)
	}
	return 0
}

// primaryDomainOrFallback returns the platform hostname from the API's
// canonical app URL. This keeps the CORS helper aligned with the fleet's
// configured wildcard suffix instead of baking a second domain contract
// into the CLI. The customer can always override via --host.
func primaryDomainOrFallback(app api.AppResponse) string {
	if parsed, err := url.Parse(app.URL); err == nil && parsed.Hostname() != "" {
		return parsed.Hostname()
	}
	return app.Slug + ".gregale.dev"
}

// sortedAllowedMethods renders the closed set of HTTP methods the
// --method flag accepts in stable order. Used by the fast-fail error
// to surface the closed set without relying on Go's randomised map
// iteration order.
func sortedAllowedMethods() string {
	keys := make([]string, 0, len(corsAllowedMethods))
	for k := range corsAllowedMethods {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return strings.Join(keys, ", ")
}
