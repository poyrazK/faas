package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/operatorobs"
)

func cmdObsRecurrence(args []string) int {
	fs := flag.NewFlagSet(subObsRecurrence, flag.ContinueOnError)
	fs.SetOutput(osStderr)
	since := fs.String("since", "", "activation-cohort lower bound (RFC3339)")
	token := fs.String("admin-token", "", obsAdminTokenUsage)
	timeout := fs.Duration("timeout", 10*time.Second, "HTTP timeout")
	jsonOut := fs.Bool("json", false, "emit structured JSON")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 0 || *timeout <= 0 {
		_, _ = fmt.Fprintln(osStderr, "gregalectl obs recurrence: no positional arguments and a positive timeout required")
		return 2
	}
	if *since != "" {
		if _, err := time.Parse(time.RFC3339Nano, *since); err != nil {
			_, _ = fmt.Fprintln(osStderr, "gregalectl obs recurrence: since must be RFC3339")
			return 2
		}
	}
	cred, ok := resolveObsCredential("obs recurrence", *token)
	if !ok {
		return 2
	}
	path := operatorobs.AlertRecurrencePath
	if *since != "" {
		path += "?" + url.Values{"since": {*since}}.Encode()
	}
	var out api.ObsAlertRecurrence
	if err := fetchObsBundleSection(context.Background(), &http.Client{Timeout: *timeout}, apidBase()+path, cred, "recurrence", &out); err != nil {
		_, _ = fmt.Fprintf(osStderr, "gregalectl obs %v\n", err)
		return 1
	}
	if !validObsRecurrence(out) {
		_, _ = fmt.Fprintln(osStderr, "gregalectl obs recurrence: invalid response")
		return 1
	}
	if *jsonOut || jsonOutput || jsonFlagEnabled(args) {
		return emitOperatorJSON(out)
	}

	_, _ = fmt.Fprintf(osStdout, "recurrence scope=%s since=%s as_of=%s collection=%s retention=%s truncated=%t\n", out.Scope, out.Since.UTC().Format(time.RFC3339), out.AsOf.UTC().Format(time.RFC3339), out.Collection.CollectionState, out.Retention.State, out.Truncated)
	writeCollectionCoverage(osStdout, out.Coverage)
	if out.Totals == nil {
		_, _ = fmt.Fprintln(osStdout, "totals=unknown (no successful collection)")
		return 0
	}
	_, _ = fmt.Fprintf(osStdout, "series=%d repeated_series=%d\n", out.Totals.Series, out.Totals.RepeatedSeries)
	writeRecurrenceMetrics(osStdout, "total", out.Totals.Metrics)
	for _, item := range out.Items {
		label := "rule=" + item.RuleKey + " series=" + item.SeriesID + " name=" + item.Name + " component=" + item.Component + " severity=" + item.Severity
		writeRecurrenceMetrics(osStdout, label, item.Metrics)
		_, _ = fmt.Fprintf(osStdout, "  recent_episodes_truncated=%t\n", item.RecentEpisodesTruncated)
		for _, e := range item.RecentEpisodes {
			_, _ = fmt.Fprintf(osStdout, "  episode=%s status=%s evidence=%s\n", e.EpisodeID, e.Status, e.EvidencePath)
		}
	}
	return 0
}

func writeRecurrenceMetrics(w io.Writer, label string, m api.ObsAlertRecurrenceMetrics) {
	_, _ = fmt.Fprintf(w, "%s episodes=%d repeat_activations=%d unresolved=%d unknown_observation=%d\n", label, m.Episodes, m.RepeatActivations, m.Unresolved, m.UnknownObservation)
	d, g := m.Duration, m.RecoveryGap
	_, _ = fmt.Fprintf(w, "  duration samples=%d not_observed=%d invalid_clock=%d min_seconds=%s p50_seconds=%s\n", d.Samples, d.NotObserved, d.InvalidClock, responseSeconds(d.MinSeconds), responseSeconds(d.P50Seconds))
	_, _ = fmt.Fprintf(w, "  recovery_gap samples=%d unconfirmed_recovery=%d resolution_overlap=%d invalid_clock=%d min_seconds=%s p50_seconds=%s\n", g.Samples, g.UnconfirmedRecovery, g.ResolutionOverlap, g.InvalidClock, responseSeconds(g.MinSeconds), responseSeconds(g.P50Seconds))
}
