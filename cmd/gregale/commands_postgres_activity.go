package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

const postgresActivityUsage = "usage: gregale postgres activity DATABASE [--since 24h|7d|RFC3339]"

// cmdPostgresActivity shows why a database was awake (ADR-20261125231222).
func cmdPostgresActivity(args []string) int {
	fs := newFlagSet("postgres activity", flag.ContinueOnError)
	since := fs.String("since", "", "period to show: a duration such as 24h or 7d, or an RFC 3339 time (default 24h)")
	if err := parseInterspersed(fs, args); err != nil || fs.NArg() != 1 {
		PrintUsage(os.Stderr, postgresActivityUsage, "postgres")
		return 1
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	database, err := resolveManagedPostgresDatabase(context.Background(), client, fs.Arg(0))
	if err != nil {
		return printErr("Could not find managed PostgreSQL database", err)
	}
	result, err := client.GetManagedPostgresActivity(context.Background(), database.ID, *since)
	if err != nil {
		return printErr("Could not load the database's activity", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(result))
	}
	_, _ = fmt.Fprint(osStdout, renderPostgresActivity(result))
	return 0
}

// postgresActivityListed caps the starts the table prints; --json has all
// the API returned.
const postgresActivityListed = 20

func renderPostgresActivity(a api.ManagedPostgresActivity) string {
	var b strings.Builder
	period := a.Until.Sub(a.Since)
	fmt.Fprintf(&b, "%s: awake %s of the last %s, %d starts", a.DatabaseName, formatAwake(a.AwakeSeconds), formatAwake(int64(period.Seconds())), a.Starts)
	if a.SuspendTimeoutSeconds > 0 {
		fmt.Fprintf(&b, " (suspend timeout %ds)", a.SuspendTimeoutSeconds)
	}
	b.WriteString("\n")
	if a.Starts == 0 {
		b.WriteString("\nNo recorded starts. Starts are recorded from the usage sweep and can lag by up to an hour.\n")
		return b.String()
	}
	b.WriteString("\nCauses\n")
	tw := tabwriter.NewWriter(&b, 0, 0, 2, ' ', 0)
	_, _ = fmt.Fprintln(tw, "  CAUSE\tAPP\tDETAIL\tSTARTS\tAWAKE")
	for _, c := range a.Causes {
		_, _ = fmt.Fprintf(tw, "  %s\t%s\t%s\t%d\t%s\n", c.Cause, dashIfEmpty(c.AppSlug), dashIfEmpty(c.Detail), c.Starts, formatAwake(c.AwakeSeconds))
	}
	_ = tw.Flush()
	b.WriteString("\nRecent starts\n")
	tw = tabwriter.NewWriter(&b, 0, 0, 2, ' ', 0)
	_, _ = fmt.Fprintln(tw, "  STARTED\tSUSPENDED\tAWAKE\tCAUSE\tAPP\tDETAIL")
	for i, s := range a.ComputeStarts {
		if i == postgresActivityListed {
			break
		}
		suspended, awake := "awake", formatAwake(int64(a.Until.Sub(s.StartedAt).Seconds()))
		if s.SuspendedAt != nil {
			suspended, awake = s.SuspendedAt.UTC().Format("15:04:05"), formatAwake(int64(s.SuspendedAt.Sub(s.StartedAt).Seconds()))
		}
		_, _ = fmt.Fprintf(tw, "  %s\t%s\t%s\t%s\t%s\t%s\n", s.StartedAt.UTC().Format("Jan 02 15:04:05"), suspended, awake, s.Cause, dashIfEmpty(s.AppSlug), dashIfEmpty(s.Detail))
	}
	_ = tw.Flush()
	if hidden := len(a.ComputeStarts) - postgresActivityListed; hidden > 0 || a.Truncated {
		b.WriteString("  … more starts in --json")
		if a.Truncated {
			b.WriteString(" (the API lists the latest 200)")
		}
		b.WriteString("\n")
	}
	if len(a.Hints) > 0 {
		b.WriteString("\nWhat to do\n")
		for _, h := range a.Hints {
			fmt.Fprintf(&b, "  • %s\n", h.Message)
			if h.Command != "" {
				fmt.Fprintf(&b, "    look: $ %s\n", h.Command)
			}
			for _, fix := range h.FixCommands {
				fmt.Fprintf(&b, "    fix:  $ %s\n", fix)
			}
		}
	}
	fmt.Fprintf(&b, "\nCauses are best guesses from timing: %s\n", a.DocsURL)
	return b.String()
}

// formatAwake renders seconds as 1h05m, 4m30s or 45s.
func formatAwake(seconds int64) string {
	d := time.Duration(seconds) * time.Second
	switch {
	case d >= 24*time.Hour:
		return fmt.Sprintf("%dd%02dh", int(d.Hours())/24, int(d.Hours())%24)
	case d >= time.Hour:
		return fmt.Sprintf("%dh%02dm", int(d.Hours()), int(d.Minutes())%60)
	case d >= time.Minute:
		return fmt.Sprintf("%dm%02ds", int(d.Minutes()), int(d.Seconds())%60)
	default:
		return fmt.Sprintf("%ds", seconds)
	}
}
