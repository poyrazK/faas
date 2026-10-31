package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/operatorobs"
)

func cmdObsResponseTimes(args []string) int {
	fs := flag.NewFlagSet(subObsResponseTimes, flag.ContinueOnError)
	fs.SetOutput(osStderr)
	since := fs.String("since", "", "activation-cohort lower bound (RFC3339)")
	token := fs.String("admin-token", "", obsAdminTokenUsage)
	timeout := fs.Duration("timeout", 10*time.Second, "HTTP timeout")
	jsonOut := fs.Bool("json", false, "emit structured JSON")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 0 || *timeout <= 0 {
		_, _ = fmt.Fprintln(osStderr, "gregalectl obs response-times: no positional arguments and a positive timeout required")
		return 2
	}
	if *since != "" {
		if _, err := time.Parse(time.RFC3339Nano, *since); err != nil {
			_, _ = fmt.Fprintln(osStderr, "gregalectl obs response-times: since must be RFC3339")
			return 2
		}
	}
	cred, ok := resolveObsCredential("obs response-times", *token)
	if !ok {
		return 2
	}
	path := operatorobs.AlertResponseTimesPath
	if *since != "" {
		path += "?" + url.Values{"since": {*since}}.Encode()
	}
	var out api.ObsAlertResponseTimes
	if err := fetchObsBundleSection(context.Background(), &http.Client{Timeout: *timeout}, apidBase()+path, cred, "response-times", &out); err != nil {
		_, _ = fmt.Fprintf(osStderr, "gregalectl obs %v\n", err)
		return 1
	}
	if out.AsOf.IsZero() || out.Since.IsZero() || out.Scope != "retained_activation_cohort" || out.DurationBasis != "episode_activation" || out.Groups == nil || out.Collection.State == "" || out.PercentileMethod != "nearest_rank_whole_seconds" {
		_, _ = fmt.Fprintln(osStderr, "gregalectl obs response-times: invalid response")
		return 1
	}
	if out.Coverage != nil && (!validCollectionCoverage(*out.Coverage) || !out.Coverage.Since.Equal(out.Since) || !out.Coverage.AsOf.Equal(out.AsOf)) {
		_, _ = fmt.Fprintln(osStderr, "gregalectl obs response-times: invalid coverage")
		return 1
	}
	if *jsonOut || jsonOutput || jsonFlagEnabled(args) {
		return emitOperatorJSON(out)
	}
	_, _ = fmt.Fprintf(osStdout, "response_times scope=%s since=%s as_of=%s collection=%s retention=%s\n", out.Scope, out.Since.UTC().Format(time.RFC3339), out.AsOf.UTC().Format(time.RFC3339), out.Collection.CollectionState, out.Retention.State)
	_, _ = fmt.Fprintf(osStdout, "percentile_method=%s coverage=retained_only\n", out.PercentileMethod)
	writeCollectionCoverage(osStdout, out.Coverage)
	if out.Totals == nil {
		_, _ = fmt.Fprintln(osStdout, "totals=unknown (no successful collection)")
		return 0
	}
	writeResponseAggregate(osStdout, "total", *out.Totals)
	for _, g := range out.Groups {
		writeResponseAggregate(osStdout, "severity="+g.Severity+" component="+g.Component, g.Metrics)
	}
	return 0
}
func writeResponseAggregate(w io.Writer, label string, a api.ObsAlertResponseAggregate) {
	_, _ = fmt.Fprintf(w, "%s episodes=%d unresolved=%d unknown_observation=%d complete_receipts=%d gapped_receipts=%d invalid_receipts=%d missing_revisions=%d\n", label, a.Episodes, a.Unresolved, a.UnknownObservation, a.ReceiptCoverage.CompleteEpisodes, a.ReceiptCoverage.GappedEpisodes, a.ReceiptCoverage.InvalidReceiptEpisodes, a.ReceiptCoverage.MissingRevisions)
	for _, entry := range []struct {
		name   string
		metric api.ObsAlertResponseMetric
	}{{"acknowledgment", a.Acknowledgment}, {"resolution", a.Resolution}} {
		m := entry.metric
		_, _ = fmt.Fprintf(w, "  %s samples=%d not_observed=%d unknown=%d invalid_clock=%d p50_seconds=%s p95_seconds=%s\n", entry.name, m.Samples, m.NotObserved, m.Unknown, m.InvalidClock, responseSeconds(m.P50Seconds), responseSeconds(m.P95Seconds))
	}
}
func responseSeconds(v *int64) string {
	if v == nil {
		return "unavailable"
	}
	return strconv.FormatInt(*v, 10)
}
