// commands_app_static_egress_ip.go — ADR-119 CLI surface for the
// per-app static egress IP feature.
//
//	gregale app <slug> static-egress-ip show
//	gregale app <slug> static-egress-ip set <ip>
//	gregale app <slug> static-egress-ip clear
//
// Mirrors the per-app egress-allowlist CLI shape
// (commands_app_routes.go + commands_app_streaming_cap.go):
// sub-verb dispatch is internal to cmdAppStaticEgressIP, with
// the slug threaded in by cmdAppDispatch (commands5.go). The
// verb name `static-egress-ip` is exposed as the const
// subStaticEgressIP so the dispatcher's switch statement can
// route `static-egress-ip` -> cmdAppStaticEgressIP uniformly
// (same shape as subSecurity / subRoutes).
//
// Auth: Bearer + MFA + the customer's account scope, gated by
// the server-side FAAS_STATIC_EGRESS_IP_ENABLED env flag
// (cmd/apid/handlers_apps_static_egress_ip.go). The CLI does
// NOT pre-check the env flag — the server returns 402 if the
// feature is dark-launched. The CLI surfaces that as a regular
// error so the operator sees the same feedback shape as
// every other 4xx.
//
// Plan gate: the apid handler rejects non-Scale plans with
// 402 + CodePlanStaticEgressIPNotAllowed. The CLI surfaces
// the error verbatim; no pre-check here so the gate stays
// server-side (the spec is the canonical source of truth —
// pkg/api/limits.go's StaticEgressIPAllowed accessor is the
// fail-closed check the server enforces).
package main

import (
	"context"
	"flag"
	"fmt"
	"os"

	"github.com/onebox-faas/faas/pkg/api"
)

// subStaticEgressIP is the verb name for
// `gregale app <slug> static-egress-ip ...`. Co-located with
// the leaf (mirrors subSecurity's co-location pattern in
// commands_app_security.go) so the dispatcher edit and the
// verb name live one Edit apart for reviewers.
const subStaticEgressIP = "static-egress-ip"

// cmdAppStaticEgressIP implements the three sub-verbs for
// the per-app static egress IP feature (ADR-119).
//
//	show        — GET /v1/apps/{slug}/static-egress-ip
//	set   <ip>  — PUT /v1/apps/{slug}/static-egress-ip
//	clear       — DELETE /v1/apps/{slug}/static-egress-ip
//
// The leaf is registered with cmdAppDispatch in commands5.go
// (same slug-then-args threading as cmdAppSecurity /
// cmdAppsRoutes). Sub-verb dispatch happens locally so the
// dispatcher's switch stays flat.
func cmdAppStaticEgressIP(slug string, args []string) int {
	if slug == "" {
		PrintUsage(os.Stderr, "usage: gregale app <slug> static-egress-ip {show|set <ip>|clear}", "apps")
		return 1
	}
	if len(args) == 0 {
		PrintUsage(os.Stderr, "usage: gregale app <slug> static-egress-ip {show|set <ip>|clear}", "apps")
		return 1
	}
	switch args[0] {
	case "show":
		return cmdAppStaticEgressIPShow(slug, args[1:])
	case "set":
		return cmdAppStaticEgressIPSet(slug, args[1:])
	case "clear":
		return cmdAppStaticEgressIPClear(slug, args[1:])
	default:
		PrintUsage(os.Stderr, "usage: gregale app <slug> static-egress-ip {show|set <ip>|clear}", "apps")
		return 1
	}
}

// cmdAppStaticEgressIPShow — GET the current pin + plan cap.
// JSON envelope preserves the PlanCap field so
// `gregale --json app demo static-egress-ip show` can drive
// dashboards.
func cmdAppStaticEgressIPShow(slug string, args []string) int {
	fs := flag.NewFlagSet("app static-egress-ip show", flag.ContinueOnError)
	if err := fs.Parse(args); err != nil {
		return 1
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	resp, err := client.GetAppStaticEgressIP(context.Background(), slug)
	if err != nil {
		return printErr("Get failed", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(resp))
	}
	if resp.IP == nil {
		PrintOK(osStdout, "App %s has no static egress IP pinned.", slug)
		_, _ = fmt.Fprintf(osStdout, "  plan_cap: %d\n", resp.PlanCap)
		return 0
	}
	PrintOK(osStdout, "App %s static egress IP:", slug)
	_, _ = fmt.Fprintf(osStdout, "  ip: %s\n", resp.IP.String())
	if resp.SetAt != nil {
		_, _ = fmt.Fprintf(osStdout, "  set_at: %s\n", resp.SetAt.Format("2006-01-02 15:04:05 UTC"))
	}
	_, _ = fmt.Fprintf(osStdout, "  plan_cap: %d\n", resp.PlanCap)
	return 0
}

// cmdAppStaticEgressIPSet — PUT a fresh IP. Argument validation
// is intentionally minimal here: the apid handler enforces the
// full deny set (RFC1918, link-local, multicast, CGN, /0, IPv6).
// A pre-check on the CLI would duplicate the gate and risk
// drift — the server is the single source of truth.
func cmdAppStaticEgressIPSet(slug string, args []string) int {
	fs := flag.NewFlagSet("app static-egress-ip set", flag.ContinueOnError)
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if fs.NArg() != 1 {
		PrintUsage(os.Stderr, "usage: gregale app <slug> static-egress-ip set <ip>", "apps")
		return 1
	}
	ipStr := fs.Arg(0)
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	resp, err := client.SetAppStaticEgressIP(context.Background(), slug, api.SetAppStaticEgressIPRequest{IP: ipStr})
	if err != nil {
		return printErr("Set failed", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(resp))
	}
	if resp.IP == nil {
		PrintOK(osStdout, "App %s static egress IP cleared.", slug)
		return 0
	}
	PrintOK(osStdout, "App %s static egress IP set.", slug)
	_, _ = fmt.Fprintf(os.Stdout, "  ip: %s\n", resp.IP.String())
	return 0
}

// cmdAppStaticEgressIPClear — DELETE the pin. Idempotent:
// deleting an app that has no pin returns a no-op (200 with
// IP=nil) rather than 404, so a repeated `clear` is safe.
func cmdAppStaticEgressIPClear(slug string, args []string) int {
	fs := flag.NewFlagSet("app static-egress-ip clear", flag.ContinueOnError)
	if err := fs.Parse(args); err != nil {
		return 1
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	if err := client.ClearAppStaticEgressIP(context.Background(), slug); err != nil {
		return printErr("Clear failed", err)
	}
	if jsonOutput {
		return jsonOut(nil)
	}
	PrintOK(osStdout, "App %s static egress IP cleared.", slug)
	return 0
}
