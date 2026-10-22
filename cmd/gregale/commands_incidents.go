package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

const incidentsUsage = "usage: gregale incidents [list|show]"

// cmdIncidents implements `gregale incidents` (ADR-20261022193512): one list
// of alert firing periods, route monitor incidents and platform incidents.
func cmdIncidents(args []string) int {
	if len(args) == 0 || strings.HasPrefix(args[0], "-") {
		return cmdIncidentsList(args)
	}
	switch args[0] {
	case subList:
		return cmdIncidentsList(args[1:])
	case "show":
		return cmdIncidentsShow(args[1:])
	}
	printCommandValidation(os.Stderr, "unknown incidents subcommand %q\n", args[0])
	PrintUsage(os.Stderr, incidentsUsage, "incidents")
	return 1
}

func cmdIncidentsList(args []string) int {
	fs := newFlagSet("incidents list", flag.ContinueOnError)
	app := fs.String("app", "", "keep one app's incidents")
	open := fs.Bool("open", false, "open incidents only")
	since := fs.String("since", "", "resolved incidents from this long ago, e.g. 24h or 3d (default 7d, at most 30d)")
	if err := fs.Parse(args); err != nil || fs.NArg() != 0 {
		PrintUsage(os.Stderr, "usage: gregale incidents list [--app SLUG] [--open] [--since 24h]", "incidents")
		return 1
	}
	q := api.IncidentsQuery{App: *app, OpenOnly: *open}
	if *since != "" {
		lookback, err := parsePositiveLogsDuration(*since)
		if err != nil {
			printCommandValidation(os.Stderr, "--since must be a positive duration such as 24h or 3d\n")
			return 1
		}
		q.Since = time.Now().Add(-lookback)
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	out, err := client.ListIncidents(context.Background(), q)
	if err != nil {
		return printErr("Could not list incidents", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(out))
	}
	renderIncidents(osStdout, out, time.Now())
	return 0
}

func renderIncidents(w io.Writer, out api.IncidentsResponse, now time.Time) {
	if len(out.Incidents) == 0 {
		_, _ = fmt.Fprintf(w, "No incidents since %s.\n", out.Since.UTC().Format("2006-01-02 15:04 UTC"))
	} else {
		_, _ = fmt.Fprintf(w, "%-9s  %-14s  %-16s  %-10s  %-28s  %s\n", "STATUS", "SOURCE", "APP", "DURATION", "TITLE", "ID")
		for _, inc := range out.Incidents {
			app := inc.App
			if app == "" {
				app = "-"
			}
			_, _ = fmt.Fprintf(w, "%-9s  %-14s  %-16s  %-10s  %-28s  %s\n", inc.Status, inc.Source, app,
				incidentDuration(inc, now), truncateRunes(inc.Title, 28), inc.ID)
		}
	}
	if out.Truncated {
		_, _ = fmt.Fprintln(w, "More incidents exist; narrow with --app, --open or --since.")
	}
	if len(out.UnavailableSources) > 0 {
		_, _ = fmt.Fprintf(w, "Could not read: %s.\n", strings.Join(out.UnavailableSources, ", "))
	}
}

// incidentDuration is how long the incident lasted, or has lasted so far.
func incidentDuration(inc api.Incident, now time.Time) string {
	end := now
	if inc.ResolvedAt != nil {
		end = *inc.ResolvedAt
	}
	d := end.Sub(inc.OpenedAt).Round(time.Minute)
	switch {
	case d < time.Minute:
		return "<1m"
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("%dh%02dm", int(d.Hours()), int(d.Minutes())%60)
	}
	return fmt.Sprintf("%dd", int(d.Hours()/24))
}

// incidentLogLine summarises an alert's log context
// (ADR-20261022172052) the way its notification does.
func incidentLogLine(raw json.RawMessage) (string, string) {
	var lc struct {
		Errors            int    `json:"errors"`
		Warnings          int    `json:"warnings"`
		HTTP5xx           int    `json:"http_5xx"`
		SearchPath        string `json:"search_path"`
		BusiestErrorRoute *struct {
			Route string `json:"route"`
			Count int    `json:"count"`
		} `json:"busiest_error_route"`
	}
	if len(raw) == 0 || json.Unmarshal(raw, &lc) != nil {
		return "", ""
	}
	line := fmt.Sprintf("%d errors, %d warnings, %d HTTP 5xx", lc.Errors, lc.Warnings, lc.HTTP5xx)
	if r := lc.BusiestErrorRoute; r != nil && r.Route != "" {
		line += fmt.Sprintf(" (most errors on %s: %d)", r.Route, r.Count)
	}
	return line, lc.SearchPath
}

func truncateRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}

func cmdIncidentsShow(args []string) int {
	fs := newFlagSet("incidents show", flag.ContinueOnError)
	if err := fs.Parse(args); err != nil || fs.NArg() != 1 {
		PrintUsage(os.Stderr, "usage: gregale incidents show <incident-id>", "incidents")
		return 1
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	d, err := client.GetIncident(context.Background(), fs.Arg(0))
	if err != nil {
		return printErr("Could not read the incident", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(d))
	}
	renderIncident(osStdout, d, time.Now())
	return 0
}

func renderIncident(w io.Writer, d api.IncidentDetail, now time.Time) {
	inc := d.Incident
	_, _ = fmt.Fprintf(w, "%s  (%s, %s)\n", inc.Title, inc.Source, inc.Status)
	if inc.App != "" {
		_, _ = fmt.Fprintf(w, "  app       %s\n", inc.App)
	}
	_, _ = fmt.Fprintf(w, "  opened    %s\n", inc.OpenedAt.UTC().Format("2006-01-02 15:04:05 UTC"))
	if inc.ResolvedAt != nil {
		_, _ = fmt.Fprintf(w, "  resolved  %s  (after %s)\n", inc.ResolvedAt.UTC().Format("2006-01-02 15:04:05 UTC"), incidentDuration(inc, now))
	} else {
		_, _ = fmt.Fprintf(w, "  open for  %s\n", incidentDuration(inc, now))
	}
	if inc.Metric != "" {
		_, _ = fmt.Fprintf(w, "  metric    %s\n", inc.Metric)
	}
	if inc.Impact != "" {
		_, _ = fmt.Fprintf(w, "  impact    %s\n", inc.Impact)
	}
	if len(d.Fires) > 0 {
		_, _ = fmt.Fprintln(w, "\nNotifications")
		for _, f := range d.Fires {
			_, _ = fmt.Fprintf(w, "  %s  observed %g  %s\n", f.At.UTC().Format("15:04:05"), f.Observed, f.Status)
		}
	}
	if line, search := incidentLogLine(d.LogContext); line != "" {
		_, _ = fmt.Fprintf(w, "\nWhat the logs showed\n  %s\n", line)
		if search != "" {
			_, _ = fmt.Fprintf(w, "  search: %s\n", search)
		}
	}
	if len(d.Updates) > 0 {
		_, _ = fmt.Fprintln(w, "\nStatus updates")
		for _, u := range d.Updates {
			_, _ = fmt.Fprintf(w, "  %s  %-13s  %s\n", u.At.UTC().Format("2006-01-02 15:04"), u.State, u.Message)
		}
	}
	if len(d.Changes) > 0 {
		_, _ = fmt.Fprintln(w, "\nWhat changed")
		for _, c := range d.Changes {
			_, _ = fmt.Fprintf(w, "  %s  %s\n", c.At.UTC().Format("2006-01-02 15:04"), c.Summary)
		}
		if d.ChangesTruncated {
			_, _ = fmt.Fprintln(w, "  (older changes omitted)")
		}
	}
	if len(d.UnavailableSources) > 0 {
		_, _ = fmt.Fprintf(w, "\nCould not read: %s.\n", strings.Join(d.UnavailableSources, ", "))
	}
}
