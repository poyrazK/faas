package main

// `gregale edge-rules <list|create|get|update|delete> ...` —
// customer-facing CLI for the Edge Rules resource (ADR-089, issue #561).
// PR 1 of the rollout shipped the schema, state, apid CRUD, SDK, and
// OpenAPI surface (PR #799). PR 2 (this file) ships the CLI wrapper
// around the same SDK methods so customers (and the e2e tests in
// PR 9) can drive the surface from a shell.
//
// Mirrors `commands_alerts.go` for the dispatcher shape and
// `commands_crons_runs.go` for the test idiom (httptest server +
// osStdout swap). Verb-name constants come from commands2.go so
// the `--quiet` short-flag dispatch and zsh completion see the
// same name space. All per-kind action validation is delegated
// to the server-side `Validate() *Problem` methods on the PR 1
// DTO structs in pkg/api — the CLI reuses them client-side to
// surface a malformed action locally instead of round-tripping
// a 422.

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"net"
	"os"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
)

// edgeRuleKindVocab mirrors the closed `kind` set in
// migrations/00192_edge_rules.sql's CHECK constraint (extended by
// migrations/00214 for validate, 00219 for limit, 00229 for geo,
// 00236 for maintenance, 00244 for throttle, and 00244 for budget
// (post-merge the budget migration renumbered to the same slot as
// the throttle migration — see ADR-091 D20.5 amendment, issue #881
// + ADR-093). Surfacing a typo locally avoids a 400 round-trip on
// every `edge-rules create` call (same posture as webhookClosedVocab
// in commands_webhooks.go).
var edgeRuleKindVocab = []string{
	"route", "rewrite", "redirect", "headers", "cors", "jwt", "ip",
	"validate", "limit", "geo", "maintenance", "throttle", "budget",
	"cache",
}

// edgeRuleJWTAlgVocab is the closed `algorithm` set for kind=jwt.
// Mirrors pkg/api.EdgeRuleJWTAction.Validate() so the typo case
// fails fast. HS* is intentionally excluded (ADR-091 D11); HS*
// over JWKS would mean a symmetric key served from a public
// endpoint, where anyone with the URL can forge tokens. If a future
// ADR introduces a `secret_ref` action shape for HMAC-signed JWTs,
// HS* would return here alongside a sibling secret.alg vocabulary.
var edgeRuleJWTAlgVocab = []string{
	"RS256", "RS384", "RS512",
	"ES256", "ES384", "ES512",
}

// isEdgeRuleKind reports whether k is in the closed kind vocabulary.
// Used by every leaf's local pre-validation step.
func isEdgeRuleKind(k string) bool {
	for _, v := range edgeRuleKindVocab {
		if v == k {
			return true
		}
	}
	return false
}

// cmdEdgeRules dispatches `gregale edge-rules <sub>` to the right
// leaf. Mirrors the `cmdWebhooks` / `cmdAlerts` shape: parent
// validates there's at least one subcommand, looks up the cliCommand
// for `suggestSubcommand`, then hands off to the leaf.
func cmdEdgeRules(args []string) int {
	parent, _ := lookupCliCommand("edge-rules")
	if len(args) == 0 {
		PrintUsage(os.Stderr, "usage: gregale edge-rules <list|create|get|update|delete> [args]", "edge-rules")
		return 1
	}
	switch args[0] {
	case subList:
		return cmdEdgeRulesList(args[1:])
	case subCreate:
		return cmdEdgeRulesCreate(args[1:])
	case subGet:
		return cmdEdgeRulesGet(args[1:])
	case subUpdate:
		return cmdEdgeRulesUpdate(args[1:])
	case subRm:
		return cmdEdgeRulesRm(args[1:])
	}
	fmt.Fprintf(os.Stderr, "unknown edge-rules subcommand %q\n", args[0])
	if sug, _ := suggestSubcommand(args[0], parent); sug != "" {
		maybeSuggestSub(sug)
	}
	return 1
}

// cmdEdgeRulesList lists edge rules for the current account or for
// a specific app (when --app is provided). Without --app it hits the
// account-wide endpoint; with --app it hits the per-app endpoint.
// Both produce the same DTO shape; the JSON envelope is identical.
func cmdEdgeRulesList(args []string) int {
	fs := flag.NewFlagSet("edge-rules list", flag.ContinueOnError)
	slug := fs.String("app", "", "filter to a single app slug")
	kind := fs.String("kind", "", "filter to a single kind (route|rewrite|redirect|headers|cors|jwt|ip|validate|limit|geo|throttle)")
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if *kind != "" && !isEdgeRuleKind(*kind) {
		return printErr("Invalid --kind", fmt.Errorf("must be one of %s; got %q", strings.Join(edgeRuleKindVocab, ", "), *kind))
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	var items []api.EdgeRuleResponse
	if *slug != "" {
		items, err = client.ListEdgeRulesForApp(context.Background(), *slug)
	} else {
		items, err = client.ListEdgeRules(context.Background())
	}
	if err != nil {
		return printErr("List failed", err)
	}
	if *kind != "" {
		filtered := items[:0]
		for _, it := range items {
			if it.Kind == *kind {
				filtered = append(filtered, it)
			}
		}
		items = filtered
	}
	if jsonOutput {
		return jsonOut(writeNDJSON(items))
	}
	if len(items) == 0 {
		_, _ = fmt.Fprintln(osStdout, "(no edge rules)")
		return 0
	}
	_, _ = fmt.Fprintf(osStdout, "%-36s %-12s %-9s %-32s %s\n", "ID", "KIND", "PRIORITY", "MATCH HOST", "MATCH PATH")
	for _, it := range items {
		enabled := secretScanOn
		if !it.Enabled {
			enabled = secretScanOff
		}
		_, _ = fmt.Fprintf(osStdout, "%-36s %-12s %-9d %-32s %s  [%s]\n",
			it.ID, it.Kind, it.Priority, truncate(it.MatchHost, 32), it.MatchPath, enabled)
	}
	return 0
}

// cmdEdgeRulesCreate builds a CreateEdgeRuleRequest from the per-kind
// flags, validates the action shape locally via the PR 1 Validate()
// methods, then POSTs to /v1/apps/{slug}/edge-rules. Each per-kind
// flag set is declared up-front; only the set matching --kind is
// marshaled into the action struct. Reject mismatched-kind flags
// before the round-trip (e.g. --rewrite-from passed with
// --kind=cors).
func cmdEdgeRulesCreate(args []string) int {
	fs := flag.NewFlagSet("edge-rules create", flag.ContinueOnError)
	slug := fs.String("app", "", "app slug (required)")
	kind := fs.String("kind", "", "rule kind: route|rewrite|redirect|headers|cors|jwt|ip|validate|limit|geo|throttle (required)")
	matchHost := fs.String("match-host", "", "host to match (required)")
	matchPath := fs.String("match-path", "/", "path to match")
	var matchMethods multiFlag
	fs.Var(&matchMethods, "match-method", "HTTP method (repeat for multiple)")
	priority := fs.Int("priority", 100, "match priority (lower wins; default 100)")
	enabled := fs.Bool("enabled", true, "whether the rule is enabled (default true)")

	// route
	routeTarget := fs.String("route-target-slug", "", "kind=route: target app slug (required)")

	// rewrite
	rewriteFrom := fs.String("rewrite-from", "", "kind=rewrite: from path (required)")
	rewriteTo := fs.String("rewrite-to", "", "kind=rewrite: to path (required)")

	// redirect
	redirectStatus := fs.Int("redirect-status", 0, "kind=redirect: status code (301|302|307|308; required)")
	redirectTo := fs.String("redirect-to", "", "kind=redirect: Location URL (required)")
	var redirectHeaders multiFlag
	fs.Var(&redirectHeaders, "redirect-header", "kind=redirect: extra header (Name:Value; repeat)")

	// headers
	var headersReqAdd, headersReqSet multiFlag
	var headersReqRm multiFlag
	var headersResAdd, headersResSet multiFlag
	var headersResRm multiFlag
	fs.Var(&headersReqAdd, "headers-request-add", "kind=headers: request header to add (Name:Value; repeat)")
	fs.Var(&headersReqSet, "headers-request-set", "kind=headers: request header to set (Name:Value; repeat)")
	fs.Var(&headersReqRm, "headers-request-remove", "kind=headers: request header to remove (Name; repeat)")
	fs.Var(&headersResAdd, "headers-response-add", "kind=headers: response header to add (Name:Value; repeat)")
	fs.Var(&headersResSet, "headers-response-set", "kind=headers: response header to set (Name:Value; repeat)")
	fs.Var(&headersResRm, "headers-response-remove", "kind=headers: response header to remove (Name; repeat)")

	// cors
	var corsOrigins, corsMethods, corsHeaders, corsExpose multiFlag
	fs.Var(&corsOrigins, "cors-allow-origin", "kind=cors: allowed origin (repeat)")
	fs.Var(&corsMethods, "cors-allow-method", "kind=cors: allowed method (repeat)")
	fs.Var(&corsHeaders, "cors-allow-header", "kind=cors: allowed header (repeat)")
	fs.Var(&corsExpose, "cors-expose-header", "kind=cors: exposed header (repeat)")
	corsCreds := fs.Bool("cors-allow-credentials", false, "kind=cors: allow credentials")
	corsMaxAge := fs.Int("cors-max-age-seconds", 0, "kind=cors: preflight max age (default 0)")

	// jwt
	jwtIssuer := fs.String("jwt-issuer", "", "kind=jwt: token issuer (required)")
	jwtJWKS := fs.String("jwt-jwks-url", "", "kind=jwt: JWKS URL (required; must be https://)")
	var jwtAudience, jwtAlgorithms multiFlag
	fs.Var(&jwtAudience, "jwt-audience", "kind=jwt: required audience (repeat)")
	fs.Var(&jwtAlgorithms, "jwt-algorithm", "kind=jwt: allowed algorithm (RS256|RS384|RS512|ES256|ES384|ES512; repeat). HS* excluded (ADR-091 D11).")
	var jwtClaims multiFlag
	fs.Var(&jwtClaims, "jwt-required-claim", "kind=jwt: required claim (Name=Value; repeat)")

	// ip
	var ipAllow, ipDeny multiFlag
	fs.Var(&ipAllow, "ip-allow", "kind=ip: allow CIDR (repeat)")
	fs.Var(&ipDeny, "ip-deny", "kind=ip: deny CIDR (repeat)")

	// limit (ADR-091 D24). Standalone per-route body cap — the
	// primitive for "POST /upload ≤ 5 MB" without a JSON Schema.
	// Both flags are required-as-a-pair at the action-struct
	// level: --limit-max-body-bytes must be > 0 (otherwise
	// apid-Validate returns 422). --limit-max-body-bytes-streaming
	// is optional (0 = no streaming carve-out, falls back to the
	// buffered cap). Clamps are enforced server-side by
	// pkg/api.EdgeRuleLimitAction.Validate().
	limitMaxBodyBytes := fs.Int("limit-max-body-bytes", 0, "kind=limit: required buffered body cap in bytes (>0, <=25MiB)")
	limitMaxBodyBytesStreaming := fs.Int("limit-max-body-bytes-streaming", 0, "kind=limit: optional streaming body cap in bytes (0=inherit buffered; <=100MiB)")
	// geo (ADR-091 D21). ISO 3166-1 alpha-2 country codes; the
	// validator in pkg/api/dto.go enforces the closed vocab.
	var geoAllow, geoDeny multiFlag
	fs.Var(&geoAllow, "geo-allow", "kind=geo: allow country code (ISO 3166-1 alpha-2; repeat)")
	fs.Var(&geoDeny, "geo-deny", "kind=geo: deny country code (ISO 3166-1 alpha-2; repeat)")

	// throttle (ADR-091 D20.5 amendment, issue #881). Per-route
	// token-bucket cap. rps is required-as-positive (the apid
	// validator rejects 0 / negative with 422 to prevent a
	// permanently unevictable bucket under the LRU invariant —
	// see pkg/gateway/ratelimit.go::NewLimiterWithLRU). burst is
	// required-as-positive for the same reason. Sub-plan ceiling
	// check happens server-side (the CLI is HTTP-only and doesn't
	// have the plan row; the apid sub-plan validator against
	// acct.Plan is the authoritative gate).
	throttleRPS := fs.Float64("throttle-requests-per-second", 0, "kind=throttle: refill rate (req/s; >0; <=plan.RateLimitRPS)")
	throttleBurst := fs.Int("throttle-burst", 0, "kind=throttle: token-bucket burst (>0; <=plan.RateLimitBurst)")

	// cache (ADR-122 §Decision). Per-route TTL primitive.
	// max-age-seconds is the fresh window (default 60); stale-
	// if-error-seconds is the post-fresh window where stale
	// entries may serve on origin failure (default 300). Both
	// are server-capped (3600 and 300 respectively — see
	// pkg/api.ResponseCacheMaxAgeMaxSeconds /
	// ResponseCacheStaleIfErrorMaxSeconds); the CLI does the
	// structural checks so the user gets the same error locally.
	var cacheVaryOn, cacheMethods multiFlag
	cacheMaxAge := fs.Int("cache-max-age-seconds", 0, "kind=cache: fresh window in seconds (default 60; max 3600)")
	cacheStaleIfError := fs.Int("cache-stale-if-error-seconds", 0, "kind=cache: stale-on-error window in seconds (default 300; max 300)")
	fs.Var(&cacheVaryOn, "cache-vary-on", "kind=cache: header to vary on (Accept-Language|Accept-Encoding; repeat)")
	fs.Var(&cacheMethods, "cache-methods", "kind=cache: cacheable method (GET|HEAD; repeat; default GET,HEAD)")

	// budget (ADR-093 §Decision). Per-request wall-clock deadline.
	// budget-ms is required-as-positive: the server rejects 0 because
	// a kind=budget rule with no budget is a silent no-op. The
	// override header defaults to api.RequestBudgetDefaultOverrideHeader
	// server-side when left empty.
	budgetMs := fs.Int("budget-ms", 0, "kind=budget: per-request wall-clock budget in ms (>0; max 30000)")
	budgetOverrideHeader := fs.String("budget-allow-override-header", "", "kind=budget: header that may override budget-ms per request (default x-faas-budget-ms)")

	// maintenance (ADR-091 D20 / issue #881). Per-route 503 with a
	// Retry-After. Both fields are optional — a bare maintenance rule
	// is a valid "hard down, no hint" shape.
	maintenanceRetryAfter := fs.Int("maintenance-retry-after-seconds", 0, "kind=maintenance: Retry-After hint in seconds (>=0; max 86400)")
	maintenanceMessage := fs.String("maintenance-message", "", "kind=maintenance: operator message surfaced to callers (<=512 bytes)")

	if err := fs.Parse(args); err != nil {
		return 1
	}
	if *slug == "" || *kind == "" || *matchHost == "" {
		PrintUsage(os.Stderr, "usage: gregale edge-rules create --app <slug> --kind <K> --match-host <H> [--match-path <P>] [--match-method M]... [--priority N] [--enabled] <kind-specific flags>", "edge-rules")
		return 1
	}
	if !isEdgeRuleKind(*kind) {
		return printErr("Invalid --kind", fmt.Errorf("must be one of %s; got %q", strings.Join(edgeRuleKindVocab, ", "), *kind))
	}
	actionBytes, err := buildEdgeRuleAction(*kind, edgeRuleActionInputs{
		RouteTarget:                *routeTarget,
		RewriteFrom:                *rewriteFrom,
		RewriteTo:                  *rewriteTo,
		RedirectStatus:             *redirectStatus,
		RedirectTo:                 *redirectTo,
		RedirectHeaders:            redirectHeaders,
		HeadersReqAdd:              headersReqAdd,
		HeadersReqSet:              headersReqSet,
		HeadersReqRm:               headersReqRm,
		HeadersResAdd:              headersResAdd,
		HeadersResSet:              headersResSet,
		HeadersResRm:               headersResRm,
		CORSOrigins:                corsOrigins,
		CORSMethods:                corsMethods,
		CORSHeaders:                corsHeaders,
		CORSExpose:                 corsExpose,
		CORSCreds:                  *corsCreds,
		CORSMaxAge:                 *corsMaxAge,
		JWTIssuer:                  *jwtIssuer,
		JWTJWKS:                    *jwtJWKS,
		JWTAudience:                jwtAudience,
		JWTAlgorithms:              jwtAlgorithms,
		JWTClaims:                  jwtClaims,
		IPAllow:                    ipAllow,
		IPDeny:                     ipDeny,
		LimitMaxBodyBytes:          *limitMaxBodyBytes,
		LimitMaxBodyBytesStreaming: *limitMaxBodyBytesStreaming,
		GeoAllow:                   geoAllow,
		GeoDeny:                    geoDeny,
		ThrottleRPS:                *throttleRPS,
		ThrottleBurst:              *throttleBurst,
		CacheMaxAgeSeconds:         *cacheMaxAge,
		CacheStaleIfErrorSeconds:   *cacheStaleIfError,
		CacheVaryOn:                cacheVaryOn,
		CacheMethods:               cacheMethods,
		BudgetMs:                   *budgetMs,
		BudgetOverrideHeader:       *budgetOverrideHeader,
		MaintenanceRetryAfter:      *maintenanceRetryAfter,
		MaintenanceMessage:         *maintenanceMessage,
	})
	if err != nil {
		return printErr("Invalid flags for --kind="+*kind, err)
	}
	req := api.CreateEdgeRuleRequest{
		MatchHost:    *matchHost,
		MatchPath:    *matchPath,
		MatchMethods: matchMethods,
		Priority:     priority,
		Enabled:      enabled,
		Kind:         *kind,
		Action:       actionBytes,
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	out, err := client.CreateEdgeRule(context.Background(), *slug, req)
	if err != nil {
		return printErr("Create failed", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(out))
	}
	PrintOK(osStdout, "Edge rule %s created for app %s (kind=%s, priority=%d).", out.ID, *slug, out.Kind, out.Priority)
	return 0
}

// cmdEdgeRulesGet fetches a single edge rule by ID.
func cmdEdgeRulesGet(args []string) int {
	fs := flag.NewFlagSet("edge-rules get", flag.ContinueOnError)
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if fs.NArg() != 1 {
		PrintUsage(os.Stderr, "usage: gregale edge-rules get <id>", "edge-rules")
		return 1
	}
	id := fs.Arg(0)
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	out, err := client.GetEdgeRule(context.Background(), id)
	if err != nil {
		return printErr("Get failed", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(out))
	}
	_, _ = fmt.Fprintf(osStdout, "ID:          %s\n", out.ID)
	_, _ = fmt.Fprintf(osStdout, "Account:     %s\n", out.AccountID)
	_, _ = fmt.Fprintf(osStdout, "App:         %s\n", out.AppID)
	_, _ = fmt.Fprintf(osStdout, "Match host:  %s\n", out.MatchHost)
	_, _ = fmt.Fprintf(osStdout, "Match path:  %s\n", out.MatchPath)
	if len(out.MatchMethods) > 0 {
		_, _ = fmt.Fprintf(osStdout, "Methods:     %s\n", strings.Join(out.MatchMethods, ", "))
	}
	_, _ = fmt.Fprintf(osStdout, "Priority:    %d\n", out.Priority)
	_, _ = fmt.Fprintf(osStdout, "Enabled:     %t\n", out.Enabled)
	_, _ = fmt.Fprintf(osStdout, "Kind:        %s\n", out.Kind)
	_, _ = fmt.Fprintf(osStdout, "Action:      %s\n", string(out.Action))
	_, _ = fmt.Fprintf(osStdout, "Created:     %s\n", out.CreatedAt.Format("2006-01-02 15:04:05 MST"))
	_, _ = fmt.Fprintf(osStdout, "Updated:     %s\n", out.UpdatedAt.Format("2006-01-02 15:04:05 MST"))
	return 0
}

// cmdEdgeRulesUpdate sends a partial PATCH. Uses fs.Visit to
// distinguish "flag not passed" (omit from request) from "flag
// passed with empty value" (send zero value). The triple-state
// enabled flag is tracked via an enabledSet boolean.
func cmdEdgeRulesUpdate(args []string) int {
	fs := flag.NewFlagSet("edge-rules update", flag.ContinueOnError)
	matchHost := fs.String("match-host", "", "new host to match")
	matchPath := fs.String("match-path", "", "new path to match")
	var matchMethods multiFlag
	fs.Var(&matchMethods, "match-method", "new method set (repeat)")
	priority := fs.Int("priority", 0, "new priority (0 = unset)")
	enable := fs.Bool("enable", false, "enable the rule")
	disable := fs.Bool("disable", false, "disable the rule")
	// Per-kind action re-marshaling on PATCH. PATCHing the action
	// requires the full new action shape — no partial sub-keys.
	kind := fs.String("kind", "", "rule kind (required when patching --*-action flags)")
	routeTarget := fs.String("route-target-slug", "", "kind=route: target app slug")
	rewriteFrom := fs.String("rewrite-from", "", "kind=rewrite: from path")
	rewriteTo := fs.String("rewrite-to", "", "kind=rewrite: to path")
	redirectStatus := fs.Int("redirect-status", 0, "kind=redirect: status code")
	redirectTo := fs.String("redirect-to", "", "kind=redirect: Location URL")
	var redirectHeaders multiFlag
	fs.Var(&redirectHeaders, "redirect-header", "kind=redirect: extra header (Name:Value; repeat)")
	var headersReqAdd, headersReqSet, headersReqRm multiFlag
	var headersResAdd, headersResSet, headersResRm multiFlag
	fs.Var(&headersReqAdd, "headers-request-add", "kind=headers: request header to add")
	fs.Var(&headersReqSet, "headers-request-set", "kind=headers: request header to set")
	fs.Var(&headersReqRm, "headers-request-remove", "kind=headers: request header to remove")
	fs.Var(&headersResAdd, "headers-response-add", "kind=headers: response header to add")
	fs.Var(&headersResSet, "headers-response-set", "kind=headers: response header to set")
	fs.Var(&headersResRm, "headers-response-remove", "kind=headers: response header to remove")
	var corsOrigins, corsMethods, corsHeaders, corsExpose multiFlag
	fs.Var(&corsOrigins, "cors-allow-origin", "kind=cors: allowed origin")
	fs.Var(&corsMethods, "cors-allow-method", "kind=cors: allowed method")
	fs.Var(&corsHeaders, "cors-allow-header", "kind=cors: allowed header")
	fs.Var(&corsExpose, "cors-expose-header", "kind=cors: exposed header")
	corsCreds := fs.Bool("cors-allow-credentials", false, "kind=cors: allow credentials")
	corsMaxAge := fs.Int("cors-max-age-seconds", 0, "kind=cors: preflight max age")
	jwtIssuer := fs.String("jwt-issuer", "", "kind=jwt: token issuer")
	jwtJWKS := fs.String("jwt-jwks-url", "", "kind=jwt: JWKS URL")
	var jwtAudience, jwtAlgorithms multiFlag
	fs.Var(&jwtAudience, "jwt-audience", "kind=jwt: required audience")
	fs.Var(&jwtAlgorithms, "jwt-algorithm", "kind=jwt: allowed algorithm")
	var jwtClaims multiFlag
	fs.Var(&jwtClaims, "jwt-required-claim", "kind=jwt: required claim")
	var ipAllow, ipDeny multiFlag
	fs.Var(&ipAllow, "ip-allow", "kind=ip: allow CIDR")
	fs.Var(&ipDeny, "ip-deny", "kind=ip: deny CIDR")
	var geoAllow, geoDeny multiFlag
	fs.Var(&geoAllow, "geo-allow", "kind=geo: allow country code (ISO 3166-1 alpha-2)")
	fs.Var(&geoDeny, "geo-deny", "kind=geo: deny country code (ISO 3166-1 alpha-2)")

	// limit (ADR-091 D24). Same flags as Create; either flag
	// present flips anyKindFlagVisited() true so the action jsonb
	// gets re-marshaled and shipped in the PATCH body.
	limitMaxBodyBytes := fs.Int("limit-max-body-bytes", 0, "kind=limit: buffered body cap in bytes")
	limitMaxBodyBytesStreaming := fs.Int("limit-max-body-bytes-streaming", 0, "kind=limit: streaming body cap in bytes (0=inherit buffered)")

	// throttle (ADR-091 D20.5 amendment, issue #881). Mirror of
	// the create-side flags. Both flags carry a "0 means save-as-
	// is platform default" semantics — but for throttle, 0 is a
	// 422 from the apid validator (a 0-rps rule is a leak under
	// the LRU invariant), so the CLI explicitly rejects 0/negative
	// here AND the validator rejects it server-side.
	throttleRPS := fs.Float64("throttle-requests-per-second", 0, "kind=throttle: new refill rate (req/s; >0; <=plan.RateLimitRPS)")
	throttleBurst := fs.Int("throttle-burst", 0, "kind=throttle: new token-bucket burst (>0; <=plan.RateLimitBurst)")

	// cache (ADR-122 §Decision). Mirror of the create-side
	// flags. Same closed-set + cap semantics — the CLI does
	// structural checks, the server enforces the ceiling.
	var cacheVaryOn, cacheMethods multiFlag
	cacheMaxAge := fs.Int("cache-max-age-seconds", 0, "kind=cache: new fresh window in seconds (max 3600)")
	cacheStaleIfError := fs.Int("cache-stale-if-error-seconds", 0, "kind=cache: new stale-on-error window in seconds (max 300)")
	fs.Var(&cacheVaryOn, "cache-vary-on", "kind=cache: header to vary on (Accept-Language|Accept-Encoding; repeat)")
	fs.Var(&cacheMethods, "cache-methods", "kind=cache: cacheable method (GET|HEAD; repeat)")

	// budget + maintenance (ADR-093 / ADR-091 D20). Mirror of the
	// create-side flags; same structural checks run in
	// buildEdgeRuleAction for both paths.
	budgetMs := fs.Int("budget-ms", 0, "kind=budget: new per-request wall-clock budget in ms (>0; max 30000)")
	budgetOverrideHeader := fs.String("budget-allow-override-header", "", "kind=budget: header that may override budget-ms per request (default x-faas-budget-ms)")
	maintenanceRetryAfter := fs.Int("maintenance-retry-after-seconds", 0, "kind=maintenance: new Retry-After hint in seconds (>=0; max 86400)")
	maintenanceMessage := fs.String("maintenance-message", "", "kind=maintenance: new operator message (<=512 bytes)")

	if err := fs.Parse(args); err != nil {
		return 1
	}
	if fs.NArg() != 1 {
		PrintUsage(os.Stderr, "usage: gregale edge-rules update <id> [--match-host H] [--match-path P] [--match-method M]... [--priority N] [--enable|--disable] [kind-specific flags]", "edge-rules")
		return 1
	}
	if *enable && *disable {
		return printErr("Invalid flags", fmt.Errorf("--enable and --disable are mutually exclusive"))
	}

	id := fs.Arg(0)
	visited := map[string]bool{}
	fs.Visit(func(f *flag.Flag) { visited[f.Name] = true })

	req := api.UpdateEdgeRuleRequest{}
	if visited["match-host"] {
		s := *matchHost
		req.MatchHost = &s
	}
	if visited["match-path"] {
		s := *matchPath
		req.MatchPath = &s
	}
	if visited["match-method"] {
		m := []string(matchMethods)
		req.MatchMethods = &m
	}
	if visited["priority"] {
		// Send even when the user passed --priority 0; 0 is a legal
		// priority (DB CHECK `BETWEEN 0 AND 10000` per
		// migrations/00192_edge_rules.sql:46, "lower wins"), so the
		// previous `&& *priority != 0` guard silently dropped a
		// legitimate highest-precedence update.
		p := *priority
		req.Priority = &p
	}
	if *enable {
		t := true
		req.Enabled = &t
	} else if *disable {
		f := false
		req.Enabled = &f
	}

	// Per-kind action re-marshal. Only attempt if at least one
	// per-kind flag was visited; otherwise leave req.Action nil so
	// the server preserves the existing jsonb.
	if anyKindFlagVisited(visited) {
		if *kind == "" {
			return printErr("Invalid flags", fmt.Errorf("--kind is required when patching action fields"))
		}
		if !isEdgeRuleKind(*kind) {
			return printErr("Invalid --kind", fmt.Errorf("must be one of %s; got %q", strings.Join(edgeRuleKindVocab, ", "), *kind))
		}
		actionBytes, err := buildEdgeRuleAction(*kind, edgeRuleActionInputs{
			RouteTarget:                *routeTarget,
			RewriteFrom:                *rewriteFrom,
			RewriteTo:                  *rewriteTo,
			RedirectStatus:             *redirectStatus,
			RedirectTo:                 *redirectTo,
			RedirectHeaders:            redirectHeaders,
			HeadersReqAdd:              headersReqAdd,
			HeadersReqSet:              headersReqSet,
			HeadersReqRm:               headersReqRm,
			HeadersResAdd:              headersResAdd,
			HeadersResSet:              headersResSet,
			HeadersResRm:               headersResRm,
			CORSOrigins:                corsOrigins,
			CORSMethods:                corsMethods,
			CORSHeaders:                corsHeaders,
			CORSExpose:                 corsExpose,
			CORSCreds:                  *corsCreds,
			CORSMaxAge:                 *corsMaxAge,
			JWTIssuer:                  *jwtIssuer,
			JWTJWKS:                    *jwtJWKS,
			JWTAudience:                jwtAudience,
			JWTAlgorithms:              jwtAlgorithms,
			JWTClaims:                  jwtClaims,
			IPAllow:                    ipAllow,
			IPDeny:                     ipDeny,
			LimitMaxBodyBytes:          *limitMaxBodyBytes,
			LimitMaxBodyBytesStreaming: *limitMaxBodyBytesStreaming,
			GeoAllow:                   geoAllow,
			GeoDeny:                    geoDeny,
			ThrottleRPS:                *throttleRPS,
			ThrottleBurst:              *throttleBurst,
			CacheMaxAgeSeconds:         *cacheMaxAge,
			CacheStaleIfErrorSeconds:   *cacheStaleIfError,
			CacheVaryOn:                cacheVaryOn,
			CacheMethods:               cacheMethods,
			BudgetMs:                   *budgetMs,
			BudgetOverrideHeader:       *budgetOverrideHeader,
			MaintenanceRetryAfter:      *maintenanceRetryAfter,
			MaintenanceMessage:         *maintenanceMessage,
		})
		if err != nil {
			return printErr("Invalid flags for --kind="+*kind, err)
		}
		req.Action = &actionBytes
	}

	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	out, err := client.UpdateEdgeRule(context.Background(), id, req)
	if err != nil {
		return printErr("Update failed", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(out))
	}
	PrintOK(osStdout, "Edge rule %s updated.", out.ID)
	return 0
}

// cmdEdgeRulesRm deletes an edge rule by ID. Requires interactive
// typed confirmation ("delete edge rule") unless --quiet is passed
// for CI/scripted paths (issue #312 pattern). Returns 1 if the
// user cancels (per requireTyped semantics).
func cmdEdgeRulesRm(args []string) int {
	fs := flag.NewFlagSet("edge-rules delete", flag.ContinueOnError)
	quiet := fs.Bool("quiet", false, "skip the typed confirmation (for scripts)")
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if fs.NArg() != 1 {
		PrintUsage(os.Stderr, "usage: gregale edge-rules delete <id> [--quiet]", "edge-rules")
		return 1
	}
	id := fs.Arg(0)
	if !*quiet {
		_, _ = fmt.Fprintf(osStderr, "About to delete edge rule %s.\n", id)
		if !requireTyped("delete edge rule") {
			return 1
		}
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	if err := client.DeleteEdgeRule(context.Background(), id); err != nil {
		return printErr("Delete failed", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(map[string]string{"id": id, "status": "deleted"}))
	}
	PrintOK(osStdout, "Edge rule %s deleted.", id)
	return 0
}

// edgeRuleActionInputs bundles the per-kind flag values passed by
// the caller. Every leaf passes the same struct so buildEdgeRuleAction
// has a single signature and the per-kind switch is the only source
// of branching.
type edgeRuleActionInputs struct {
	// route
	RouteTarget string
	// rewrite
	RewriteFrom, RewriteTo string
	// redirect
	RedirectStatus  int
	RedirectTo      string
	RedirectHeaders []string
	// headers
	HeadersReqAdd, HeadersReqSet []string
	HeadersReqRm                 []string
	HeadersResAdd, HeadersResSet []string
	HeadersResRm                 []string
	// cors
	CORSOrigins, CORSMethods []string
	CORSHeaders, CORSExpose  []string
	CORSCreds                bool
	CORSMaxAge               int
	// jwt
	JWTIssuer                  string
	JWTJWKS                    string
	JWTAudience, JWTAlgorithms []string
	JWTClaims                  []string
	// ip
	IPAllow, IPDeny []string
	// limit (ADR-091 D24). Both fields are int — pointer types
	// would force a "was-it-passed" triple-state distinction
	// that's already handled by anyKindFlagVisited(). A literal
	// 0 from a missed flag is harmless because kind=limit without
	// a buffered cap is rejected by apid-Validate (422), so the
	// server-side guard catches the omission cleanly.
	LimitMaxBodyBytes          int
	LimitMaxBodyBytesStreaming int
	// geo (ADR-091 D21). ISO 3166-1 alpha-2 country codes;
	// uppercased + space-stripped before the validator runs.
	GeoAllow, GeoDeny []string
	// throttle (ADR-091 D20.5 amendment, issue #881). Per-route
	// token-bucket. Float64 for rps so the recommendation
	// endpoint's ceil(observed_rps * 2) hands over a non-integer
	// without coercing the customer's intent. The apid sub-plan
	// validator (PlanMaxRPS / PlanMaxBurst) is the authoritative
	// ceiling check — the CLI does the structural checks only
	// (positive rps, positive burst) so the local error mirrors
	// the server's "0-rps is a leak" message.
	ThrottleRPS   float64
	ThrottleBurst int
	// cache (ADR-122 §Decision). Per-route TTL primitive.
	// MaxAgeSeconds defaults to 60 server-side when 0 is passed
	// (the apid validator applies the default in
	// pkg/api.EdgeRuleCacheAction.Validate). The CLI does the
	// structural checks (positive, ≤ ResponseCacheMaxAgeMaxSeconds)
	// so the local error mirrors the server's.
	CacheMaxAgeSeconds       int
	CacheStaleIfErrorSeconds int
	CacheVaryOn              []string
	CacheMethods             []string
	// budget (ADR-093 §Decision). Per-request wall-clock deadline.
	// BudgetMs is required-as-positive: pkg/api.EdgeRuleBudgetAction
	// .Validate rejects 0 because a kind=budget rule with no budget
	// is a silent no-op — the worst shape for a safety primitive.
	// BudgetOverrideHeader is optional; empty means the runtime uses
	// api.RequestBudgetDefaultOverrideHeader (`x-faas-budget-ms`).
	BudgetMs             int
	BudgetOverrideHeader string
	// maintenance (ADR-091 D20). Per-route 503 + Retry-After. Both
	// fields are optional — a bare maintenance rule is the valid
	// "hard down, no hint" shape, so neither is checked for
	// presence, only for range.
	MaintenanceRetryAfter int
	MaintenanceMessage    string
}

// buildEdgeRuleAction marshals the per-kind inputs into the matching
// PR 1 action struct (or returns a validation error before the
// round-trip). Every kind has a closed shape; closed-set and
// structural validation runs against the same predicates as
// pkg/api.*Action.Validate() so the user gets the same error
// locally that the server would return over the wire.
func buildEdgeRuleAction(kind string, in edgeRuleActionInputs) (json.RawMessage, error) {
	switch kind {
	case "route":
		a := api.EdgeRuleRouteAction{TargetAppSlug: in.RouteTarget}
		if err := a.Validate(); err != nil {
			return nil, errToError(err)
		}
		return marshalAction(a)
	case "rewrite":
		a := api.EdgeRuleRewriteAction{From: in.RewriteFrom, To: in.RewriteTo}
		if err := a.Validate(); err != nil {
			return nil, errToError(err)
		}
		return marshalAction(a)
	case "redirect":
		headers, err := parseKVList(in.RedirectHeaders, "redirect-header")
		if err != nil {
			return nil, err
		}
		a := api.EdgeRuleRedirectAction{StatusCode: in.RedirectStatus, To: in.RedirectTo, Headers: headers}
		if err := a.Validate(); err != nil {
			return nil, errToError(err)
		}
		return marshalAction(a)
	case "headers":
		req, err := parseHeaderOps(in.HeadersReqAdd, in.HeadersReqSet, in.HeadersReqRm, "request")
		if err != nil {
			return nil, err
		}
		res, err := parseHeaderOps(in.HeadersResAdd, in.HeadersResSet, in.HeadersResRm, "response")
		if err != nil {
			return nil, err
		}
		a := api.EdgeRuleHeadersAction{RequestHeaders: req, ResponseHeaders: res}
		if err := a.Validate(); err != nil {
			return nil, errToError(err)
		}
		return marshalAction(a)
	case "cors":
		a := api.EdgeRuleCORSAction{
			AllowOrigins:     in.CORSOrigins,
			AllowMethods:     in.CORSMethods,
			AllowHeaders:     in.CORSHeaders,
			ExposeHeaders:    in.CORSExpose,
			AllowCredentials: in.CORSCreds,
			MaxAgeSeconds:    in.CORSMaxAge,
		}
		if err := a.Validate(); err != nil {
			return nil, errToError(err)
		}
		return marshalAction(a)
	case "jwt":
		for _, alg := range in.JWTAlgorithms {
			if !strInSlice(alg, edgeRuleJWTAlgVocab) {
				return nil, fmt.Errorf("jwt algorithm %q must be one of %s", alg, strings.Join(edgeRuleJWTAlgVocab, ","))
			}
		}
		claims, err := parseKVList(in.JWTClaims, "jwt-required-claim")
		if err != nil {
			return nil, err
		}
		a := api.EdgeRuleJWTAction{
			Issuer:         in.JWTIssuer,
			Audience:       in.JWTAudience,
			JWKSURL:        in.JWTJWKS,
			Algorithms:     in.JWTAlgorithms,
			RequiredClaims: claims,
		}
		if err := a.Validate(); err != nil {
			return nil, errToError(err)
		}
		return marshalAction(a)
	case "ip":
		for _, cidr := range in.IPAllow {
			if _, _, err := net.ParseCIDR(cidr); err != nil {
				return nil, fmt.Errorf("ip-allow %q: not a valid CIDR (%w)", cidr, err)
			}
		}
		for _, cidr := range in.IPDeny {
			if _, _, err := net.ParseCIDR(cidr); err != nil {
				return nil, fmt.Errorf("ip-deny %q: not a valid CIDR (%w)", cidr, err)
			}
		}
		a := api.EdgeRuleIPAction{Allow: in.IPAllow, Deny: in.IPDeny}
		if err := a.Validate(); err != nil {
			return nil, errToError(err)
		}
		return marshalAction(a)
	case "limit":
		// Standalone per-route body cap. The Validate() call runs
		// the same closed-set predicates as the apid-side action
		// (0 → 422, negative streaming → 422, streaming-tighter-
		// than-buffered → 422, over-cap → 422) so a CLI user gets
		// the same error message they'd get over the wire. No
		// regex/CIDR parse here; the only check is the cap range.
		a := api.EdgeRuleLimitAction{
			MaxBodyBytes:          in.LimitMaxBodyBytes,
			MaxBodyBytesStreaming: in.LimitMaxBodyBytesStreaming,
		}
		if err := a.Validate(); err != nil {
			return nil, errToError(err)
		}
		return marshalAction(a)
	case "geo":
		// ISO 3166-1 alpha-2 country codes. The validator in
		// pkg/api/dto.go enforces the closed vocab + the 50-entry
		// cardinality cap + the no-dupes invariant; the CLI just
		// uppercases here so the wire shape is consistent regardless
		// of how the customer typed the flag.
		allow := upperCountryCodes(in.GeoAllow)
		deny := upperCountryCodes(in.GeoDeny)
		a := api.EdgeRuleGeoAction{Allow: allow, Deny: deny}
		if err := a.Validate(); err != nil {
			return nil, errToError(err)
		}
		return marshalAction(a)
	case "throttle":
		// ADR-091 D20.5 amendment / issue #881. Per-route
		// token-bucket cap. The CLI does the structural checks
		// (positive rps, positive burst) so the user gets the
		// same error locally as the server would return.
		//
		// The CLI does NOT take a plan row (HTTP-only per
		// memory/cli-is-http-only-not-direct-db) so the
		// sub-plan ceiling check is performed server-side by the
		// apid validator against acct.Plan. A "0" or "negative"
		// rps is rejected here because the server would 422 with
		// the same rationale — surfacing it locally saves a
		// round-trip.
		if in.ThrottleRPS <= 0 {
			return nil, fmt.Errorf("throttle action: requests_per_second must be > 0 (got %g) — a 0-rps rule is a silent no-op AND would create a permanently unevictable bucket", in.ThrottleRPS)
		}
		if in.ThrottleBurst <= 0 {
			return nil, fmt.Errorf("throttle action: burst must be > 0 (got %d) — same rationale as a 0-rps rule", in.ThrottleBurst)
		}
		a := api.EdgeRuleThrottleAction{
			RequestsPerSecond: in.ThrottleRPS,
			Burst:             in.ThrottleBurst,
		}
		// The server's EdgeRuleThrottleAction.Validate takes a
		// ThrottleValidationContext (per-plan ceiling). The CLI
		// calls Validate with a zero context so the structural
		// checks fire (ctx.PlanMaxRPS / ctx.PlanMaxBurst are 0,
		// which the validator treats as "no ceiling", so the
		// sub-plan check is skipped at the CLI and left to the
		// server). This matches the "fail OPEN on unknown /
		// unavailable context" posture documented on the
		// Validate() method.
		if err := a.Validate(api.ThrottleValidationContext{}); err != nil {
			return nil, errToError(err)
		}
		return marshalAction(a)
	case "cache":
		// ADR-122 §Decision. Per-route TTL primitive.
		// Structural checks mirror pkg/api.EdgeRuleCacheAction.Validate:
		//
		//   - max_age_seconds: server default 60 when 0;
		//     ceiling 3600
		//   - stale_if_error_seconds: server default 300 when
		//     0; ceiling 300
		//   - vary_on: closed subset of {Accept-Language,
		//     Accept-Encoding}; empty list = no vary
		//   - methods: closed subset of {GET, HEAD}; empty
		//     list = any method
		//
		// The CLI does the cap + closed-set checks so the
		// user gets the same error locally as the server.
		if in.CacheMaxAgeSeconds < 0 || in.CacheMaxAgeSeconds > api.ResponseCacheMaxAgeMaxSeconds {
			return nil, fmt.Errorf("cache action: max_age_seconds must be in [0, %d] (0 = use default 60); got %d", api.ResponseCacheMaxAgeMaxSeconds, in.CacheMaxAgeSeconds)
		}
		if in.CacheStaleIfErrorSeconds < 0 || in.CacheStaleIfErrorSeconds > api.ResponseCacheStaleIfErrorMaxSeconds {
			return nil, fmt.Errorf("cache action: stale_if_error_seconds must be in [0, %d] (0 = use default 300); got %d", api.ResponseCacheStaleIfErrorMaxSeconds, in.CacheStaleIfErrorSeconds)
		}
		for _, v := range in.CacheVaryOn {
			if !isCacheVaryOnVocab(v) {
				return nil, fmt.Errorf("cache action: vary_on %q not in closed vocabulary (Accept-Language|Accept-Encoding)", v)
			}
		}
		for _, m := range in.CacheMethods {
			if !isCacheMethodVocab(m) {
				return nil, fmt.Errorf("cache action: methods %q not in closed vocabulary (GET|HEAD)", m)
			}
		}
		a := api.EdgeRuleCacheAction{
			MaxAgeSeconds:       in.CacheMaxAgeSeconds,
			StaleIfErrorSeconds: in.CacheStaleIfErrorSeconds,
			VaryOn:              in.CacheVaryOn,
			Methods:             in.CacheMethods,
		}
		// CLI-side defaults for fields the user omitted
		// (flag == 0). The server's EdgeRuleCacheAction.Validate
		// intentionally does NOT default these — 0 means
		// "disable stale-on-error" per the spec, so a server-
		// side default would make that documented semantic
		// unreachable. We default here so omitting the flag
		// still gives the user the friendly 300-s default.
		if a.MaxAgeSeconds == 0 {
			a.MaxAgeSeconds = api.ResponseCacheDefaultMaxAgeSeconds
		}
		if a.StaleIfErrorSeconds == 0 {
			a.StaleIfErrorSeconds = api.ResponseCacheDefaultStaleIfErrorSeconds
		}
		return marshalAction(a)
	case "budget":
		// ADR-093 §Decision. Per-request wall-clock deadline.
		// budget_ms is checked here as well as server-side because
		// omitting --budget-ms is the overwhelmingly likely mistake
		// and the local message can name the flag; the server's
		// Validate only knows the wire field name.
		//
		// No CLI-side default: unlike kind=cache, a zero budget has
		// no defensible meaning (it would expire every request
		// immediately), so there is nothing to default TO. Rejecting
		// is the only correct move and matches
		// pkg/api.EdgeRuleBudgetAction.Validate.
		if in.BudgetMs <= 0 {
			return nil, fmt.Errorf("budget action: --budget-ms must be > 0 (got %d) — a kind=budget rule with no budget is a silent no-op; drop the rule if you want the platform default (%s) to apply", in.BudgetMs, api.RequestBudgetDefault)
		}
		a := api.EdgeRuleBudgetAction{
			BudgetMs:            in.BudgetMs,
			AllowOverrideHeader: in.BudgetOverrideHeader,
		}
		if err := a.Validate(); err != nil {
			return nil, errToError(err)
		}
		return marshalAction(a)
	case "maintenance":
		// ADR-091 D20. Per-route 503 + Retry-After. Both fields are
		// optional, so there is no presence check — only the range
		// checks the server would run, surfaced locally.
		a := api.EdgeRuleMaintenanceAction{
			RetryAfterSeconds: in.MaintenanceRetryAfter,
			Message:           in.MaintenanceMessage,
		}
		if err := a.Validate(); err != nil {
			return nil, errToError(err)
		}
		return marshalAction(a)
	case "validate":
		// kind=validate is a real server-side kind
		// (pkg/api.EdgeRuleValidateAction) but has no CLI flag
		// surface yet: its action carries a JSON Schema 2020-12
		// document, which needs a file/stdin loading UX rather than
		// a scalar flag. Say so explicitly — the previous
		// fallthrough reported "unknown kind", which contradicted
		// edgeRuleKindVocab accepting it two checks earlier.
		return nil, fmt.Errorf("kind=validate is not yet constructible from the CLI (its action carries a JSON Schema document); create it via the API, or use `gregale edge-rules update <id>` to toggle an existing rule")
	}
	return nil, fmt.Errorf("unknown kind %q", kind)
}

// isCacheVaryOnVocab reports whether v is in the closed cache
// vary_on vocabulary. Mirrors pkg/api.edgeRuleCacheVaryOnVocab
// so the CLI surfaces a typo locally.
func isCacheVaryOnVocab(v string) bool {
	for _, x := range []string{"Accept-Language", "Accept-Encoding"} {
		if v == x {
			return true
		}
	}
	return false
}

// isCacheMethodVocab reports whether m is in the closed cache
// method vocabulary. Mirrors pkg/api.edgeRuleCacheMethodVocab.
func isCacheMethodVocab(m string) bool {
	for _, x := range []string{"GET", "HEAD"} {
		if m == x {
			return true
		}
	}
	return false
}

// marshalAction encodes a into json.RawMessage. Errors are
// returned to the caller; only json.TypeError is expected (e.g.
// unmarshalable types), and the action structs are all simple
// value types so the call cannot fail in practice.
func marshalAction(a interface{}) (json.RawMessage, error) {
	b, err := json.Marshal(a)
	if err != nil {
		return nil, err
	}
	return json.RawMessage(b), nil
}

// errToError extracts a Go error from a *api.Problem's Detail field.
// The PR 1 Validate() methods return *Problem (a struct, not error);
// unwrap the Detail so it surfaces through printErr with the same
// wording the server would emit.
func errToError(p *api.Problem) error {
	if p == nil {
		return nil
	}
	return fmt.Errorf("%s", p.Detail)
}

// upperCountryCodes (ADR-091 D21) uppercases + trims each
// ISO 3166-1 alpha-2 country code so the wire shape is consistent
// regardless of how the customer typed the flag ("de" → "DE").
// Empty / whitespace-only entries are dropped. The closed-vocab
// check lives in pkg/api.EdgeRuleGeoAction.Validate (the
// 2-letter rule + the reserved-code set).
func upperCountryCodes(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	out := make([]string, 0, len(in))
	for _, c := range in {
		trimmed := strings.TrimSpace(c)
		if trimmed == "" {
			continue
		}
		out = append(out, strings.ToUpper(trimmed))
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// parseKVList splits each entry on the first `:` into Name/Value.
// Used by --redirect-header (map[string]string) and
// --jwt-required-claim (map[string]string). Returns nil map for
// empty input.
func parseKVList(items []string, flagName string) (map[string]string, error) {
	if len(items) == 0 {
		return nil, nil
	}
	out := make(map[string]string, len(items))
	for _, raw := range items {
		idx := strings.IndexByte(raw, ':')
		if idx < 0 {
			return nil, fmt.Errorf("%s %q: expected Name:Value (no ':' found)", flagName, raw)
		}
		name := raw[:idx]
		value := raw[idx+1:]
		if name == "" {
			return nil, fmt.Errorf("%s %q: Name is empty", flagName, raw)
		}
		if _, dup := out[name]; dup {
			return nil, fmt.Errorf("%s: duplicate name %q", flagName, name)
		}
		out[name] = value
	}
	return out, nil
}

// parseHeaderOps converts the three flag sets (add H:V, set H:V,
// remove H) into []EdgeRuleHeaderOp. Validates each op against
// pkg/api.EdgeRuleHeaderOp.Validate() so the user gets the same
// forbidden-header error locally.
func parseHeaderOps(add, set, rm []string, dir string) ([]api.EdgeRuleHeaderOp, error) {
	var out []api.EdgeRuleHeaderOp
	seen := map[string]string{} // name → action ("add"|"set"|"remove")
	check := func(action, raw string) (api.EdgeRuleHeaderOp, error) {
		var op api.EdgeRuleHeaderOp
		op.Action = action
		if action == "remove" {
			op.Name = raw
		} else {
			idx := strings.IndexByte(raw, ':')
			if idx < 0 {
				return op, fmt.Errorf("headers-%s-%s %q: expected Name:Value (no ':' found)", dir, action, raw)
			}
			op.Name = raw[:idx]
			op.Value = raw[idx+1:]
			if op.Name == "" {
				return op, fmt.Errorf("headers-%s-%s %q: Name is empty", dir, action, raw)
			}
		}
		if prev, dup := seen[op.Name]; dup {
			return op, fmt.Errorf("headers-%s: %q specified for both %s and %s", dir, op.Name, prev, action)
		}
		seen[op.Name] = action
		if err := op.Validate(); err != nil {
			return op, fmt.Errorf("headers-%s: %w", dir, errToError(err))
		}
		return op, nil
	}
	for _, raw := range add {
		op, err := check("add", raw)
		if err != nil {
			return nil, err
		}
		out = append(out, op)
	}
	for _, raw := range set {
		op, err := check("set", raw)
		if err != nil {
			return nil, err
		}
		out = append(out, op)
	}
	for _, raw := range rm {
		op, err := check("remove", raw)
		if err != nil {
			return nil, err
		}
		out = append(out, op)
	}
	return out, nil
}

// anyKindFlagVisited returns true if at least one per-kind action
// flag was passed to the update command. Used to decide whether
// the action jsonb should be re-marshaled or left untouched.
func anyKindFlagVisited(visited map[string]bool) bool {
	kindFlagNames := []string{
		"route-target-slug",
		"rewrite-from", "rewrite-to",
		"redirect-status", "redirect-to", "redirect-header",
		"headers-request-add", "headers-request-set", "headers-request-remove",
		"headers-response-add", "headers-response-set", "headers-response-remove",
		"cors-allow-origin", "cors-allow-method", "cors-allow-header", "cors-expose-header",
		"cors-allow-credentials", "cors-max-age-seconds",
		"jwt-issuer", "jwt-jwks-url", "jwt-audience", "jwt-algorithm", "jwt-required-claim",
		"ip-allow", "ip-deny",
		"limit-max-body-bytes", "limit-max-body-bytes-streaming",
		"throttle-requests-per-second", "throttle-burst",
		// geo + cache were added to the create/update flag sets but
		// never to this list, so `edge-rules update <id> --geo-allow X`
		// silently skipped the action rebuild and sent a metadata-only
		// PATCH. Kept alongside the budget/maintenance entries below.
		"geo-allow", "geo-deny",
		"cache-max-age-seconds", "cache-stale-if-error-seconds",
		"cache-vary-on", "cache-methods",
		"budget-ms", "budget-allow-override-header",
		"maintenance-retry-after-seconds", "maintenance-message",
	}
	for _, name := range kindFlagNames {
		if visited[name] {
			return true
		}
	}
	return false
}

// truncate is implemented in commands_webhooks.go:420 — re-used here
// so the edge-rules list table column widths line up with the
// webhooks table.
