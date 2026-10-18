package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
)

// cmdSLOsStatus implements `gregale slos status --app <slug> <slo-id>`
// (ADR-747): the SLO's error-budget position.
func cmdSLOsStatus(args []string) int {
	fs := newFlagSet("slos status", flag.ContinueOnError)
	slug := fs.String("app", "", "app slug (required)")
	if err := parseInterspersed(fs, args); err != nil {
		return 1
	}
	if *slug == "" || fs.NArg() != 1 {
		PrintUsage(os.Stderr, "usage: gregale slos status --app <slug> <slo-id>", "slos")
		return 1
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	slo, err := client.GetSLO(context.Background(), *slug, fs.Arg(0))
	if err != nil {
		return printErr("Could not fetch SLO", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(slo))
	}
	renderSLOStatus(osStdout, slo)
	return 0
}

func renderSLOStatus(w io.Writer, slo api.SLOResponse) {
	_, _ = fmt.Fprintf(w, "%s — %s over %d days\n", slo.Name, describeSLOObjective(slo), slo.WindowDays)
	st := slo.Status
	if st == nil {
		_, _ = fmt.Fprintln(w, "  (no status returned)")
		return
	}
	_, _ = fmt.Fprintf(w, "  Attained:          %s (%d of %d requests)\n", fmtPct(st.AttainmentPct), st.Good, st.Total)
	_, _ = fmt.Fprintf(w, "  Budget remaining:  %s\n", fmtPct(st.BudgetRemainingPct))
	_, _ = fmt.Fprintf(w, "  Burn rate:         %s (1h) · %s (6h)\n", fmtBurn(st.BurnRate1h), fmtBurn(st.BurnRate6h))
	_, _ = fmt.Fprintf(w, "  History:           %d of %d hours since %s\n", st.HoursRecorded, st.HoursExpected, st.WindowStart)
	if strings.HasPrefix(st.Source, "degraded") {
		_, _ = fmt.Fprintf(w, "  Note: %s\n", st.Source)
	}
}

func fmtPct(v *float64) string {
	if v == nil {
		return "no traffic"
	}
	return fmt.Sprintf("%.3f%%", *v)
}

func fmtBurn(v *float64) string {
	if v == nil {
		return "—"
	}
	return fmt.Sprintf("%.2f×", *v)
}
