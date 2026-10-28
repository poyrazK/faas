package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// attackModeTimeLayout is how the CLI prints attack mode's end.
const attackModeTimeLayout = "2006-01-02 15:04 MST"

const edgeRulesAttackModeUsage = "usage: gregale edge rule attack-mode [on|off|status] [--app SLUG] [--for 1h] [--json]"

// cmdEdgeRulesAttackMode implements `gregale edge-rules attack-mode`
// (ADR-20261021220456): switch the app's attack mode on for a while, extend
// it, switch it off, or show it. Without a verb it shows the status.
func cmdEdgeRulesAttackMode(args []string) int {
	fs := newFlagSet("edge-rules attack-mode", flag.ContinueOnError)
	appFlag := fs.String("app", "", "app slug (defaults to linked project context)")
	forFlag := fs.Duration("for", time.Duration(api.EdgeAttackModeDefaultSeconds)*time.Second,
		"with on: how long attack mode stays on, 15m to 24h")
	flags, positional := splitArgsForFlags(args)
	if err := fs.Parse(flags); err != nil {
		return 1
	}
	if len(positional) == 0 {
		positional = []string{"status"}
	}
	if len(positional) != 1 {
		PrintUsage(os.Stderr, edgeRulesAttackModeUsage, "edge-rules")
		return 1
	}
	verb := positional[0]
	if verb != "on" && verb != "off" && verb != "status" {
		PrintUsage(os.Stderr, edgeRulesAttackModeUsage, "edge-rules")
		return 1
	}
	seconds := int(forFlag.Seconds())
	if verb == "on" && (*forFlag%time.Second != 0 || seconds < api.MinEdgeAttackModeSeconds || seconds > api.MaxEdgeAttackModeSeconds) {
		return printErr("Invalid --for", fmt.Errorf("must be whole seconds from 15m to 24h; got %s", *forFlag))
	}
	slug, err := resolveAppFlagOrContext(strings.TrimSpace(*appFlag))
	if err != nil {
		return printErr("Could not resolve app slug", fmt.Errorf("pass --app <slug> or run inside a linked project: %w", err))
	}
	if !validCLISlug(slug) {
		return printErr("Invalid app slug", fmt.Errorf("%q is not a valid app slug", slug))
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	var status api.EdgeAttackModeStatus
	switch verb {
	case "on":
		status, err = client.EnableAppEdgeAttackMode(context.Background(), slug, seconds)
	case "off":
		status, err = client.DisableAppEdgeAttackMode(context.Background(), slug)
	default:
		status, err = client.GetAppEdgeAttackMode(context.Background(), slug)
	}
	if err != nil {
		return printErr("Attack mode "+verb+" failed", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(status))
	}
	renderEdgeAttackMode(osStdout, slug, status, time.Now())
	return 0
}

// renderEdgeAttackMode says whether attack mode is on and until when, and
// what it does to clients that are not browsers.
func renderEdgeAttackMode(w io.Writer, slug string, s api.EdgeAttackModeStatus, now time.Time) {
	if !s.Active {
		if s.ExpiresAt != nil && s.ExpiresAt.Before(now) {
			_, _ = fmt.Fprintf(w, "Attack mode is off for %s (last ended %s).\n", slug, s.ExpiresAt.Local().Format(attackModeTimeLayout))
			return
		}
		_, _ = fmt.Fprintf(w, "Attack mode is off for %s.\n", slug)
		return
	}
	until := ""
	if s.ExpiresAt != nil {
		until = fmt.Sprintf(" until %s (%s left)", s.ExpiresAt.Local().Format(attackModeTimeLayout), s.ExpiresAt.Sub(now).Round(time.Minute))
	}
	_, _ = fmt.Fprintf(w, "Attack mode is ON for %s%s.\n", slug, until)
	_, _ = fmt.Fprintln(w, "  Every visitor is challenged; browsers solve it once and continue.")
	_, _ = fmt.Fprintln(w, "  Verified search crawlers and requests with a verified API key or JWT pass straight through.")
	_, _ = fmt.Fprintln(w, "  Other clients (scripts, webhooks, uptime checks) get 403 challenge_required until it ends.")
	_, _ = fmt.Fprintf(w, "  Extend: gregale edge rule attack-mode on --app %s --for 6h   Stop: gregale edge rule attack-mode off --app %s\n", slug, slug)
}
