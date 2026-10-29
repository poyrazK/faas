package main

// cli_tree_flag_folds.go — ADR-20261026145832 rounds 8 and 9.
//
// Some of today's commands are another command's dry run, view or input
// form. The tree runs them as that command plus a flag: `event backfill
// create --dry-run` runs `events replay-preview`, `event subscription health
// --view delivery` runs `events subscription-delivery-health`.

import (
	"fmt"
	"slices"
	"strings"
)

// cliTreeFlagFold runs today's Old for the tree path New when the args set
// Flag ("dry-run", or "view=delivery" for one value).
type cliTreeFlagFold struct {
	New, Old string
	Flag     string
	// Also is a second flag that must be set too. It is consumed unless
	// KeepAlso: Old takes it (`--workflow-name`).
	Also     string
	KeepAlso bool
	// Keep leaves Flag for Old, which takes it (`event publish --file`);
	// otherwise the flag is consumed.
	Keep bool
	// Value moves the flag's value to Old's first argument
	// (`event publish --app my-app` runs `events publish-app my-app`).
	Value bool
	// Short is the flag's help on New.
	Short string
}

// cliTreeFlagFolds are matched in order: a fold that needs two flags comes
// before the fold that needs one of them.
var cliTreeFlagFolds = []cliTreeFlagFold{
	{New: "event backfill create", Flag: "dry-run", Also: "workflow-name", KeepAlso: true, Old: "events workflow-replay-preview", Short: "show what it would deliver without delivering"},
	{New: "event backfill create", Flag: "workflow-name", Keep: true, Old: "events workflow-backfill", Short: "start runs of this event-triggered workflow for past events"},
	{New: "event backfill create", Flag: "dry-run", Old: "events replay-preview", Short: "show what it would deliver without delivering"},
	{New: "event recovery create", Flag: "dry-run", Old: "events recovery-preview", Short: "show what it would select without changing anything"},
	{New: "event publish", Flag: "dry-run", Old: "events preview", Short: "show where the event would route without publishing it"},
	{New: "event publish", Flag: "app", Value: true, Old: "events publish-app", Short: "publish the --file event as this app"},
	{New: "event publish", Flag: "file", Keep: true, Old: "events publish-batch", Short: "publish a JSONL batch (- reads stdin)"},
	{New: "event subscription-capacity set", Flag: "dry-run", Old: "events subscription-capacity-preview", Short: "show the capacity and receipt budget without changing them"},
	{New: "event recovery-notification retry", Flag: "dry-run", Old: "events recovery-notification-retry-preview", Short: "show the receivers it would retry"},
	{New: "event notification-retry apply", Flag: "dry-run", Old: "events notification-retry-plan", Short: "show the retries and save them as a plan (--output)"},
	{New: "event lineage case retry", Flag: "dry-run", Old: "events subscription-lineage-retry-preview", Short: "show the unresolved cases it would retry"},
	{New: "event delivery retry", Flag: "retryable", Old: "events replay-retryable", Short: "retry a bounded batch of retryable terminal failures"},
	{New: "event subscription health", Flag: "view=delivery", Old: "events subscription-delivery-health", Short: "show routing, executions, retry jobs and blockers"},
	{New: "event subscription health", Flag: "view=execution", Old: "events subscription-execution-health", Short: "show executions, handler outcomes and retry-job health"},
	{New: "automation recovery create", Flag: "dry-run", Old: "automations recovery-preview", Short: "show the recovery and catch-up without writing"},
	{New: "automation event-recovery start", Flag: "dry-run", Old: "automations event-recovery-plan", Short: "save the selected routing actions for review"},
	{New: "automation recovery-response apply", Flag: "dry-run", Old: "automations recovery-response-plan", Short: "save the cancellation and retry actions for review"},
	{New: "automation recovery cancel", Flag: "dry-run", Old: "automations recovery-cancel-preview", Short: "show the recovery generation it would cancel"},
	{New: "api route monitor set", Flag: "dry-run", Old: "routes monitor preview", Short: "evaluate the budgets against recent production traffic"},
	{New: "api spec apply", Flag: "dry-run", Old: "openapi preview", Short: "show routes, edge policies and the contract diff"},
	{New: "api spec import", Flag: "dry-run", Old: "openapi dry-run", Short: "show uncovered routes without importing"},
	{New: "environment promote", Flag: "dry-run", Old: "projects environments preview", Short: "plan the promotion without running it"},
	{New: "environment gitops adopt", Flag: "dry-run", Old: "projects environments gitops adoption-preview", Short: "show the ownership transfer without changing values"},
	{New: "environment gitops promotion create", Flag: "dry-run", Old: "projects environments gitops promotion-plan", Short: "preview the promotion and export it as a Git proposal"},
	{New: "environment gitops recovery create", Flag: "dry-run", Old: "projects environments gitops rollback-plan", Short: "preview the code rollback and export it as a Git proposal"},
	{New: "workflow cancel", Flag: "dry-run", Also: "queued", Old: "workflows cancel-queued-preview", Short: "with --queued: show the runs it would cancel"},
	{New: "workflow cancel", Flag: "queued", Old: "workflows cancel-queued", Short: "cancel selected runs that are still pending and never started"},
	{New: "workflow schedule replay", Flag: "dry-run", Old: "workflows schedule-history replay-preview", Short: "check skipped occurrences without replaying them"},
	{New: "cron get", Flag: "next", Old: "crons next", Short: "show upcoming expression times across app tasks"},
	{New: "job get", Flag: "next", Old: "jobs next", Short: "show upcoming recurring Job times and readiness"},
	{New: "org invitation list", Flag: "all", Old: "orgs invitations list-all", Short: "list invitations in every state"},
}

// rewriteFlagFold runs the command a flag fold names, or reports false.
func rewriteFlagFold(args []string) ([]string, bool) {
	words := leadingTreeWords(args)
	for _, fold := range cliTreeFlagFolds {
		path := strings.Fields(fold.New)
		if len(path) > len(words) || !slices.Equal(path, words[:len(path)]) {
			continue
		}
		rest := args[len(path):]
		at, value, ok := findFoldFlag(rest, fold.Flag)
		if !ok {
			continue
		}
		drop := []foldFlagSpan{}
		if !fold.Keep {
			drop = append(drop, at)
		}
		if fold.Also != "" {
			alsoAt, _, also := findFoldFlag(rest, fold.Also)
			if !also {
				continue
			}
			if !fold.KeepAlso {
				drop = append(drop, alsoAt)
			}
		}
		out := strings.Fields(fold.Old)
		if fold.Value {
			out = append(out, value)
		}
		for i, arg := range rest {
			if !slices.ContainsFunc(drop, func(span foldFlagSpan) bool { return i >= span.start && i < span.end }) {
				out = append(out, arg)
			}
		}
		return out, true
	}
	return nil, false
}

type foldFlagSpan struct{ start, end int }

// findFoldFlag finds --name, --name=value or --name value in args before
// "--". A want of "name=value" matches that value only. A boolean flag
// (dry-run, queued, next, all, retryable) never takes the next word.
func findFoldFlag(args []string, want string) (foldFlagSpan, string, bool) {
	name, wantValue, valued := strings.Cut(want, "=")
	boolean := !valued && cliTreeFoldBoolean[name]
	for i, arg := range args {
		if arg == "--" {
			break
		}
		flagName, value, inline := strings.Cut(strings.TrimPrefix(arg, "--"), "=")
		if !strings.HasPrefix(arg, "--") || flagName != name {
			continue
		}
		end := i + 1
		switch {
		case boolean:
			if inline && value != "true" {
				return foldFlagSpan{}, "", false
			}
		case !inline && i+1 < len(args):
			value, end = args[i+1], i+2
		case !inline:
			return foldFlagSpan{}, "", false
		}
		if valued && value != wantValue {
			continue
		}
		return foldFlagSpan{i, end}, value, true
	}
	return foldFlagSpan{}, "", false
}

// cliTreeFoldBoolean are the fold flags that take no value.
var cliTreeFoldBoolean = map[string]bool{"dry-run": true, "queued": true, "next": true, "all": true, "retryable": true}

// cliTreeFlagFoldOf returns the fold that runs today's path old.
func cliTreeFlagFoldOf(old string) (cliTreeFlagFold, bool) {
	for _, fold := range cliTreeFlagFolds {
		if fold.Old == old {
			return fold, true
		}
	}
	return cliTreeFlagFold{}, false
}

// spelling is the tree command line a fold stands for, for hints and the
// migration guide.
func (fold cliTreeFlagFold) spelling() string {
	name, value, valued := strings.Cut(fold.Flag, "=")
	flag := "--" + name
	switch {
	case valued:
		flag += " " + value
	case !cliTreeFoldBoolean[name]:
		flag += " <" + strings.ToUpper(name) + ">"
	}
	if fold.Also != "" {
		flag = "--" + fold.Also + " <" + strings.ToUpper(fold.Also) + "> " + flag
	}
	return fold.New + " " + flag
}

// foldFlags are the flags folds add to the tree path path, for help. Each
// names the flags of the command it runs that path does not take, so the
// reference documents them.
func (d cliTreeDiscovery) foldFlags(path string, own []cliFlag) []cliFlag {
	var flags []cliFlag
	for _, fold := range cliTreeFlagFolds {
		name, value, valued := strings.Cut(fold.Flag, "=")
		if fold.New != path || slices.ContainsFunc(own, func(f cliFlag) bool { return f.Name == name }) {
			continue
		}
		short := fold.Short + d.foldExtraFlags(fold, own)
		if at := slices.IndexFunc(flags, func(f cliFlag) bool { return f.Name == name }); at >= 0 {
			if valued {
				flags[at].Value += "|" + value
				flags[at].Short += "; " + value + ": " + short
			}
			continue
		}
		flag := cliFlag{Name: name, Short: short}
		switch {
		case valued:
			flag.Value, flag.Short = value, value+": "+short
		case !cliTreeFoldBoolean[name]:
			flag.Value = strings.ToUpper(name)
		}
		flags = append(flags, flag)
	}
	return flags
}

// foldExtraFlags lists the flags of fold's command that own lacks.
func (d cliTreeDiscovery) foldExtraFlags(fold cliTreeFlagFold, own []cliFlag) string {
	target, ok := d.subAt(fold.Old)
	if !ok {
		return ""
	}
	name, _, _ := strings.Cut(fold.Flag, "=")
	var extra []string
	for _, flag := range target.Flags {
		if flag.Name != name && flag.Name != fold.Also && !slices.ContainsFunc(own, func(f cliFlag) bool { return f.Name == flag.Name }) {
			extra = append(extra, "--"+flag.Name)
		}
	}
	switch {
	case len(extra) == 0:
		return ""
	case len(extra) > 6:
		return fmt.Sprintf(" (with it, %d more flags: see gregale %s --help)", len(extra), fold.spelling())
	}
	return " (with it: " + strings.Join(extra, ", ") + ")"
}
