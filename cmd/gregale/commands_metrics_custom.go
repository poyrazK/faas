package main

import (
	"context"
	"fmt"
	"io"
	"math"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/appmetrics"
)

// cmdMetricsCustom implements `gregale metrics <slug> --custom NAME` (ADR-745).
func cmdMetricsCustom(client *api.Client, slug, name, rng string) int {
	series, err := client.GetCustomMetricSeries(context.Background(), slug, name, rng)
	if err != nil {
		return printErr("Could not fetch custom metric history", err)
	}
	if jsonOutput {
		return jsonOut(writeJSON(series))
	}
	renderCustomMetricSeries(osStdout, slug, series)
	return 0
}

// sparkRunes draws a value as one of eight bar heights.
var sparkRunes = []rune("▁▂▃▄▅▆▇█")

func renderCustomMetricSeries(w io.Writer, slug string, s api.CustomMetricSeriesResponse) {
	_, _ = fmt.Fprintf(w, "%s / %s — last %s (step %s)\n", slug, s.Name, s.Range, s.Step)
	if s.Source != appmetrics.SourcePrometheus {
		_, _ = fmt.Fprintf(w, "Note: source=%s (no history available right now)\n", s.Source)
		return
	}
	if len(s.Points) == 0 {
		_, _ = fmt.Fprintln(w, "(no values pushed in this window)")
		return
	}
	lo, hi := math.Inf(1), math.Inf(-1)
	for _, p := range s.Points {
		lo, hi = math.Min(lo, p.Value), math.Max(hi, p.Value)
	}
	var spark strings.Builder
	for _, p := range s.Points {
		i := 0
		if hi > lo {
			i = int((p.Value - lo) / (hi - lo) * float64(len(sparkRunes)-1))
		}
		spark.WriteRune(sparkRunes[i])
	}
	last := s.Points[len(s.Points)-1]
	_, _ = fmt.Fprintf(w, "  %s\n", spark.String())
	_, _ = fmt.Fprintf(w, "  Latest: %g at %s\n", last.Value, last.At.Format(time.RFC3339))
	_, _ = fmt.Fprintf(w, "  Range:  min %g · max %g · %d samples\n", lo, hi, len(s.Points))
}
