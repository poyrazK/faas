package main

// ADR-1451: config history, rollback and the config guard notice.

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// configTarget resolves --app and --scope the way the env commands do.
func configTarget(app, scope *string, usage string) bool {
	resolvedApp, err := resolveAppFlagOrContext(*app)
	if err == nil {
		*app = resolvedApp
	} else if !errors.Is(err, errProjectContextNotFound) {
		printErr("Could not read local project context", err)
		return false
	}
	if *app == "" {
		PrintUsage(os.Stderr, usage, "env")
		return false
	}
	resolvedScope, err := resolveEnvironmentFlagOrContext(*scope)
	if err != nil {
		printErr("Could not read local project context", err)
		return false
	}
	*scope = resolvedScope
	return true
}

func envHistory(args []string) int {
	fs := newFlagSet("env history", flag.ContinueOnError)
	app := fs.String("app", "", "app slug (defaults to linked project app)")
	scope := fs.String("scope", "", "env scope (defaults to linked project environment)")
	limit := fs.Int("limit", 20, "revisions to show (1-100)")
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if rejectUnexpectedFlagArgs(fs) || !configTarget(app, scope, "usage: gregale env history [--app <slug>] [--scope <name>] [--limit N]") {
		return 1
	}
	if *limit < 1 || *limit > api.ConfigRevisionsListMax {
		return printErr("Invalid --limit", fmt.Errorf("--limit must be between 1 and %d", api.ConfigRevisionsListMax))
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	out, err := client.ListConfigRevisions(context.Background(), *app, *scope, *limit)
	if err != nil {
		return printErr("Could not list config history", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(out))
	}
	if out.Guard != nil {
		_, _ = fmt.Fprintln(osStdout, configGuardLine(*out.Guard))
	}
	if len(out.Revisions) == 0 {
		_, _ = fmt.Fprintln(osStdout, "No config changes recorded yet.")
		return 0
	}
	w := tabwriter.NewWriter(osStdout, 0, 4, 2, ' ', 0)
	_, _ = fmt.Fprintln(w, "REV\tWHEN\tCHANGE\tBY\tGUARD")
	for _, r := range out.Revisions {
		_, _ = fmt.Fprintf(w, "%d\t%s\t%s\t%s\t%s\n", r.Revision, r.CreatedAt, configChangeLabel(r), r.Actor, dashIfEmpty(r.GuardStatus))
	}
	_ = w.Flush()
	_, _ = fmt.Fprintf(osStdout, "\nRestore variables with `gregale env rollback <rev>`.\n")
	return 0
}

func configChangeLabel(r api.ConfigRevision) string {
	switch r.Source {
	case "baseline":
		return "baseline (config before the next change)"
	case "config.rollback":
		return fmt.Sprintf("rollback to %d", r.RestoredRevision)
	case "config.guard_restore":
		return fmt.Sprintf("guard restored %d", r.RestoredRevision)
	}
	return r.Source + " " + r.Key
}

// configGuardLine summarizes a scope's newest guard in one line.
func configGuardLine(g api.ConfigGuard) string {
	switch g.Status {
	case "watching":
		if g.WindowEndsAt != "" {
			return fmt.Sprintf("Config guard: judging traffic since revision %d until %s.", g.HeadRevision, g.WindowEndsAt)
		}
		return fmt.Sprintf("Config guard: waiting for the first traffic since revision %d.", g.HeadRevision)
	case "restored":
		return fmt.Sprintf("Config guard: restored revision %d after %s.", g.BaseRevision, guardDecisionSummary(g))
	case "regressed":
		return fmt.Sprintf("Config guard: the app failed after revision %d and was not restored: %s.", g.HeadRevision, guardDecisionSummary(g))
	}
	return fmt.Sprintf("Config guard: %s (%s).", strings.ReplaceAll(g.Status, "_", " "), guardDecisionSummary(g))
}

func guardDecisionSummary(g api.ConfigGuard) string {
	if g.Decision == nil {
		return "no decision"
	}
	return g.Decision.Summary
}

func envRollback(args []string) int {
	fs := newFlagSet("env rollback", flag.ContinueOnError)
	app := fs.String("app", "", "app slug (defaults to linked project app)")
	scope := fs.String("scope", "", "env scope (defaults to linked project environment)")
	yes := fs.Bool("yes", false, "skip the typed confirmation")
	noGuard := fs.Bool("no-guard", false, "do not open a config guard for the rollback")
	// The revision may come before the flags (`env rollback 12 --yes`).
	var positional []string
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		positional, args = args[:1], args[1:]
	}
	if err := fs.Parse(args); err != nil {
		return 1
	}
	positional = append(positional, fs.Args()...)
	usage := "usage: gregale env rollback <revision> [--app <slug>] [--scope <name>] [--yes] [--no-guard]"
	if len(positional) != 1 {
		PrintUsage(os.Stderr, usage, "env")
		return 1
	}
	revision, err := strconv.ParseInt(positional[0], 10, 64)
	if err != nil || revision < 1 {
		return printErr("Invalid revision", errors.New("revision must be a positive integer from `gregale env history`"))
	}
	if !configTarget(app, scope, usage) {
		return 1
	}
	if code := requireAutomationConfirmation(*yes, "--yes"); code != 0 {
		return code
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	ctx := context.Background()
	if !*yes && !jsonOutput {
		target, err := client.GetConfigRevision(ctx, *app, *scope, revision)
		if err != nil {
			return printErr("Could not read revision", err)
		}
		vars := "no variables"
		if len(target.Variables) > 0 {
			vars = fmt.Sprintf("%d variables: %s", len(target.Variables), strings.Join(sortedKeys(target.Variables), ", "))
		}
		_, _ = fmt.Fprintf(os.Stderr, "Revision %d (%s, %s) has %s\n", revision, configChangeLabel(target.ConfigRevision), target.CreatedAt, vars)
		if !requireTypedOr(strconv.FormatInt(revision, 10), "--yes") {
			return 1
		}
	}
	if *noGuard {
		client = client.WithoutConfigGuard()
	}
	out, err := client.RollbackConfig(ctx, *app, *scope, revision)
	if err != nil {
		return printErr("Config rollback failed", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(out))
	}
	printConfigRollback(out, *app, *scope)
	return 0
}

func printConfigRollback(out api.ConfigRollbackResponse, app, scope string) {
	if len(out.Set) == 0 && len(out.Deleted) == 0 {
		PrintOK(osStdout, "Variables already match revision %d; nothing changed.", out.RestoredRevision)
	} else {
		PrintOK(osStdout, "Restored variables of revision %d (set %s; deleted %s).", out.RestoredRevision,
			dashIfEmpty(strings.Join(out.Set, ", ")), dashIfEmpty(strings.Join(out.Deleted, ", ")))
		if out.Change != nil && out.Change.Guard != nil {
			_, _ = fmt.Fprintf(osStdout, "The config guard watches the next %d minutes of traffic.\n", out.Change.Guard.WindowMinutes)
		}
	}
	scopeArg := ""
	if scope != "" {
		scopeArg = " --scope " + quoteCLIArg(scope)
	}
	for _, s := range out.SecretsNotRestored {
		// Version 0 means the secret did not exist. A secret added after
		// the revision is removed, not set, to match it (Hunt 12 H12-57).
		switch {
		case s.TargetVersion == 0:
			_, _ = fmt.Fprintf(osStdout, "  secret %s not removed (revision %d did not have it, now version %d); remove it with `%s env unset --app %s%s %s`.\n",
				s.Key, out.RestoredRevision, s.CurrentVersion, recoveryCLI(), quoteCLIArg(app), scopeArg, quoteCLIArg(s.Key))
		case s.CurrentVersion == 0:
			_, _ = fmt.Fprintf(osStdout, "  secret %s not restored (revision %d had version %d, now removed); set it with `%s env set`.\n",
				s.Key, out.RestoredRevision, s.TargetVersion, recoveryCLI())
		default:
			_, _ = fmt.Fprintf(osStdout, "  secret %s not restored (revision %d had version %d, now %d); set it with `%s env set`.\n",
				s.Key, out.RestoredRevision, s.TargetVersion, s.CurrentVersion, recoveryCLI())
		}
	}
}

// printConfigGuardNotice tells the person whether a config guard is
// watching the change they just made. It reads the scope's guard once and
// says nothing when none watches, or when the read fails.
func printConfigGuardNotice(ctx context.Context, client *Client, app, scope string) {
	if jsonOutput {
		return
	}
	g, err := client.GetConfigGuard(ctx, app, scope)
	if err != nil || g.Status != "watching" {
		return
	}
	changed, err := time.Parse(time.RFC3339Nano, g.LastChangeAt)
	if err != nil || time.Since(changed) > 2*time.Minute {
		return
	}
	_, _ = fmt.Fprintf(osStdout, "Config guard: if the app starts failing with the new values, Gregale restores the previous ones (`gregale env history`).\n")
}
