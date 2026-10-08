package main

// gregale usage savings [--app SLUG] [--since RFC3339] [--until RFC3339]
//
// Prints the scale-to-zero savings estimate (GET /v1/apps/{slug}/savings):
// billed RAM-time against keeping the app's minimum instance count
// running. The slug falls back to the linked project context.

import (
	"context"
	"flag"
	"fmt"
	"io"

	"github.com/onebox-faas/faas/pkg/api"
)

const usageSavingsUsage = "usage: gregale usage savings [--app SLUG] [--since RFC3339] [--until RFC3339]"

func cmdUsageSavings(args []string) int {
	fs := newFlagSet("usage-savings", flag.ContinueOnError)
	app := fs.String("app", "", "app slug; default: the linked project")
	since := fs.String("since", "", "window start (RFC3339); default and floor: 30 days before --until")
	until := fs.String("until", "", "window end (RFC3339); default: today's UTC midnight")
	if err := fs.Parse(args); err != nil {
		PrintUsage(osStderr, usageSavingsUsage, "usage")
		return 1
	}
	if rejectUnexpectedFlagArgs(fs) {
		return 1
	}
	slug, err := resolveRequiredAppSlug(*app)
	if err != nil {
		PrintUsage(osStderr, usageSavingsUsage, "usage")
		return printErr("No app", err)
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	out, err := client.GetAppSavings(context.Background(), slug, api.AppUsageSummaryOptions{Since: *since, Until: *until})
	if err != nil {
		return printErr("Request failed", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(out))
	}
	renderUsageSavings(osStdout, out)
	return 0
}

// renderUsageSavings prints the estimate. Money arrives in integer
// millicents and is shown in euros with integer arithmetic only.
func renderUsageSavings(w io.Writer, s api.AppSavingsResponse) {
	const labelWidth = 15
	line := func(label, format string, args ...any) {
		_, _ = fmt.Fprintf(w, "  %-*s %s\n", labelWidth, label, fmt.Sprintf(format, args...))
	}
	line("App:", "%s", s.Slug)
	line("Window:", "%s → %s", s.PeriodStart.Format("2006-01-02"), s.PeriodEnd.Format("2006-01-02"))
	line("Always-on:", "%.3f GB-hours (%s)", s.AlwaysOnGBHours, euros(s.AlwaysOnMillicents))
	line("Actual:", "%.3f GB-hours (%s)", s.ActualGBHours, euros(s.ActualMillicents))
	line("Saved:", "%.3f GB-hours (%s), parked %d%% of the time", s.SavedGBHours, euros(s.SavedMillicents), parkedPercent(s.ParkedRatio))
	_, _ = fmt.Fprintf(w, "\n  %s\n", s.Methodology)
}

// euros formats integer millicents (1 € = 100,000 millicents) to the cent,
// rounding half up.
func euros(millicents int64) string {
	sign := ""
	if millicents < 0 {
		sign, millicents = "-", -millicents
	}
	cents := (millicents + 500) / 1000
	return fmt.Sprintf("%s€%d.%02d", sign, cents/100, cents%100)
}

func parkedPercent(ratio float64) int {
	return int(ratio*100 + 0.5)
}
