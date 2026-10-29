package main

// ADR-20261019071733: deploy freezes and release windows.

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

const freezeUsage = "usage: gregale release freeze <create|list|lift|status>"

func cmdFreeze(args []string) int {
	if len(args) == 0 {
		PrintUsage(os.Stderr, freezeUsage, "freeze")
		return 1
	}
	switch args[0] {
	case "create":
		return freezeCreate(args[1:])
	case "list", "ls":
		return freezeList(args[1:])
	case "lift":
		return freezeLift(args[1:])
	case "status":
		return freezeStatus(args[1:])
	}
	printCommandValidation(os.Stderr, "gregale release freeze: unknown subcommand %q\n", args[0])
	return unknownSubcommandChoices(os.Stderr, "freeze", nestedCliCommand("freeze"), args[0])
}

// freezeAppFlag resolves --app from the flag or the linked project; an
// empty result means every app of the account.
func freezeAppFlag(app string) (string, error) {
	if strings.TrimSpace(app) != "" {
		return strings.TrimSpace(app), nil
	}
	resolved, err := resolveAppFlagOrContext("")
	if err != nil && !errors.Is(err, errProjectContextNotFound) {
		return "", err
	}
	return resolved, nil
}

// freezeRequestFromFlags builds a create request from the mutually
// exclusive window, weekly and after-rollback flag sets.
func freezeRequestFromFlags(forDur, from, until, weekly, at, tz, afterRollback string, now time.Time) (api.CreateReleaseFreezeRequest, error) {
	var req api.CreateReleaseFreezeRequest
	switch {
	case afterRollback != "":
		if forDur != "" || from != "" || until != "" || weekly != "" || at != "" {
			return req, errors.New("--after-rollback takes no other timing flags")
		}
		d, err := time.ParseDuration(afterRollback)
		if err != nil || d < time.Minute || d%time.Minute != 0 {
			return req, errors.New("--after-rollback must be a whole number of minutes, such as 30m or 2h")
		}
		req.Kind, req.AfterRollbackMinutes = api.ReleaseFreezeKindAfterRollback, int(d/time.Minute)
	case weekly != "":
		if from != "" || until != "" {
			return req, errors.New("--weekly takes --at, --for and --tz")
		}
		if at == "" || forDur == "" {
			return req, errors.New("--weekly needs --at HH:MM and --for DURATION")
		}
		d, err := time.ParseDuration(forDur)
		if err != nil || d < time.Minute || d%time.Minute != 0 {
			return req, errors.New("--for must be a whole number of minutes, such as 8h or 90m")
		}
		if tz == "" {
			tz = "UTC"
		}
		req.Kind = api.ReleaseFreezeKindWeekly
		req.Weekly = &api.ReleaseFreezeWeekly{Days: strings.Split(weekly, ","), Start: at, DurationMinutes: int(d / time.Minute), TimeZone: tz}
	default:
		if at != "" || tz != "" {
			return req, errors.New("--at and --tz apply to --weekly")
		}
		if forDur != "" && until != "" {
			return req, errors.New("use --for or --until, not both")
		}
		start := now
		req.Kind = api.ReleaseFreezeKindWindow
		if from != "" {
			parsed, err := time.Parse(time.RFC3339, from)
			if err != nil {
				return req, errors.New("--from must be an RFC 3339 time, such as 2026-12-24T00:00:00Z")
			}
			start = parsed
			req.StartsAt = parsed.UTC().Format(time.RFC3339)
		}
		switch {
		case forDur != "":
			d, err := time.ParseDuration(forDur)
			if err != nil || d <= 0 {
				return req, errors.New("--for must be a positive duration, such as 2h")
			}
			req.EndsAt = start.Add(d).UTC().Format(time.RFC3339)
		case until != "":
			parsed, err := time.Parse(time.RFC3339, until)
			if err != nil {
				return req, errors.New("--until must be an RFC 3339 time, such as 2026-12-27T09:00:00Z")
			}
			req.EndsAt = parsed.UTC().Format(time.RFC3339)
		}
	}
	return req, nil
}

func freezeCreate(args []string) int {
	fs := newFlagSet("freeze create", flag.ContinueOnError)
	app := fs.String("app", "", "app slug (defaults to linked project app)")
	all := fs.Bool("all-apps", false, "freeze every app of the account")
	reason := fs.String("reason", "", "why releases are frozen (required)")
	forDur := fs.String("for", "", "window or weekly length, such as 2h")
	from := fs.String("from", "", "window start (RFC 3339; default now)")
	until := fs.String("until", "", "window end (RFC 3339)")
	weekly := fs.String("weekly", "", "weekly days, such as fri or fri,sat")
	at := fs.String("at", "", "weekly start time HH:MM")
	tz := fs.String("tz", "", "weekly time zone (IANA; default UTC)")
	afterRollback := fs.String("after-rollback", "", "freeze an app for this long after Gregale rolls it back automatically")
	if err := fs.Parse(args); err != nil {
		return 1
	}
	usage := "usage: gregale release freeze create [--app <slug> | --all-apps] --reason TEXT (--for DUR | --until TIME | --weekly DAYS --at HH:MM --for DUR [--tz ZONE] | --after-rollback DUR)"
	if rejectUnexpectedFlagArgs(fs) || strings.TrimSpace(*reason) == "" {
		PrintUsage(os.Stderr, usage, "freeze")
		return 1
	}
	req, err := freezeRequestFromFlags(*forDur, *from, *until, *weekly, *at, *tz, *afterRollback, time.Now())
	if err != nil {
		return printErr("Invalid freeze", err)
	}
	if !*all {
		slug, err := freezeAppFlag(*app)
		if err != nil {
			return printErr("Could not read local project context", err)
		}
		if slug == "" {
			return printErr("App required", errors.New("pass --app <slug>, or --all-apps to freeze every app"))
		}
		req.App = slug
	} else if *app != "" {
		return printErr("Invalid freeze", errors.New("use --app or --all-apps, not both"))
	}
	req.Reason = strings.TrimSpace(*reason)
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	out, err := client.CreateReleaseFreeze(context.Background(), req)
	if err != nil {
		return printErr("Could not create freeze", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(out))
	}
	PrintOK(osStdout, "Freeze %s created: %s.", out.ID, freezeWhen(out))
	return 0
}

func freezeList(args []string) int {
	fs := newFlagSet("freeze list", flag.ContinueOnError)
	app := fs.String("app", "", "only freezes covering this app")
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if rejectUnexpectedFlagArgs(fs) {
		return 1
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	out, err := client.ListReleaseFreezes(context.Background(), strings.TrimSpace(*app))
	if err != nil {
		return printErr("Could not list freezes", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(out))
	}
	if len(out.Freezes) == 0 {
		_, _ = fmt.Fprintln(osStdout, "No freezes.")
		return 0
	}
	printFreezeTable(out.Freezes)
	return 0
}

func printFreezeTable(freezes []api.ReleaseFreeze) {
	w := tabwriter.NewWriter(osStdout, 0, 4, 2, ' ', 0)
	_, _ = fmt.Fprintln(w, "ID\tAPP\tWHEN\tACTIVE\tREASON\tBY")
	for _, f := range freezes {
		app := f.App
		if app == "" {
			app = "(all apps)"
		}
		active := "no"
		if f.Active {
			active = "yes"
		}
		_, _ = fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\n", f.ID, app, freezeWhen(f), active, f.Reason, f.CreatedBy)
	}
	_ = w.Flush()
}

// freezeWhen describes when a freeze applies in one phrase.
func freezeWhen(f api.ReleaseFreeze) string {
	switch f.Kind {
	case api.ReleaseFreezeKindWeekly:
		if f.Weekly == nil {
			return "weekly"
		}
		d := time.Duration(f.Weekly.DurationMinutes) * time.Minute
		return fmt.Sprintf("every %s at %s %s for %s", strings.Join(f.Weekly.Days, ","), f.Weekly.Start, f.Weekly.TimeZone, d)
	case api.ReleaseFreezeKindAfterRollback:
		return fmt.Sprintf("for %s after each automatic rollback", time.Duration(f.AfterRollbackMinutes)*time.Minute)
	}
	if f.EndsAt == "" {
		return "from " + f.StartsAt + " until lifted"
	}
	return f.StartsAt + " to " + f.EndsAt
}

func freezeLift(args []string) int {
	fs := newFlagSet("freeze lift", flag.ContinueOnError)
	var positional []string
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		positional, args = args[:1], args[1:]
	}
	if err := fs.Parse(args); err != nil {
		return 1
	}
	positional = append(positional, fs.Args()...)
	if len(positional) != 1 {
		PrintUsage(os.Stderr, "usage: gregale release freeze lift <id>", "freeze")
		return 1
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	out, err := client.LiftReleaseFreeze(context.Background(), positional[0])
	if err != nil {
		return printErr("Could not lift freeze", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(out))
	}
	PrintOK(osStdout, "Freeze %s lifted (%s).", out.ID, out.Reason)
	return 0
}

func freezeStatus(args []string) int {
	fs := newFlagSet("freeze status", flag.ContinueOnError)
	app := fs.String("app", "", "app slug (defaults to linked project app)")
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if rejectUnexpectedFlagArgs(fs) {
		return 1
	}
	slug, err := freezeAppFlag(*app)
	if err != nil {
		return printErr("Could not read local project context", err)
	}
	if slug == "" {
		// `freeze status` outside a linked checkout printed bare usage on
		// production-us rc.294 (Hunt 14 H14-64).
		return printErr("App required", errors.New("pass --app <slug>, or link this checkout with gregale link <project> --app <slug>"))
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	out, err := client.GetReleaseFreezeStatus(context.Background(), slug)
	if err != nil {
		return printErr("Could not read freeze status", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(out))
	}
	if !out.Frozen {
		_, _ = fmt.Fprintf(osStdout, "Production releases of %s are not frozen.\n", out.App)
	} else {
		_, _ = fmt.Fprintf(osStdout, "Production releases of %s are frozen:\n", out.App)
		printFreezeTable(out.Active)
		_, _ = fmt.Fprintln(osStdout, "Release anyway with `gregale deploy --override-freeze REASON` (audited), or lift a freeze with `gregale release freeze lift <id>`.")
	}
	if out.Next != nil {
		_, _ = fmt.Fprintf(osStdout, "Next freeze starts %s: %s.\n", out.Next.NextStartsAt, out.Next.Reason)
	}
	return 0
}
