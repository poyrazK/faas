package main

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	"net/url"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/operatorobs"
)

func cmdObsCollectionHistory(args []string) int {
	fs := flag.NewFlagSet(subObsCollectionHistory, flag.ContinueOnError)
	fs.SetOutput(osStderr)
	since := fs.String("since", "", "collection-history lower bound (RFC3339)")
	details := fs.Bool("details", false, "include retained diagnostics for recent failed attempts")
	token := fs.String("admin-token", "", obsAdminTokenUsage)
	timeout := fs.Duration("timeout", 10*time.Second, "HTTP timeout")
	jsonOut := fs.Bool("json", false, "emit structured JSON")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 0 || *timeout <= 0 {
		_, _ = fmt.Fprintln(osStderr, "gregalectl obs collection-history: no positional arguments and a positive timeout required")
		return 2
	}
	if *since != "" {
		if _, err := time.Parse(time.RFC3339Nano, *since); err != nil {
			_, _ = fmt.Fprintln(osStderr, "gregalectl obs collection-history: since must be RFC3339")
			return 2
		}
	}
	cred, ok := resolveObsCredential("obs collection-history", *token)
	if !ok {
		return 2
	}
	path := operatorobs.AlertCollectionHistoryPath
	query := url.Values{}
	if *since != "" {
		query.Set("since", *since)
	}
	if *details {
		query.Set("details", "true")
	}
	if len(query) > 0 {
		path += "?" + query.Encode()
	}
	var out api.ObsAlertCollectionHistory
	if err := fetchObsBundleSection(context.Background(), &http.Client{Timeout: *timeout}, apidBase()+path, cred, "collection-history", &out); err != nil {
		_, _ = fmt.Fprintf(osStderr, "gregalectl obs %v\n", err)
		return 1
	}
	if !validObsCollectionHistory(out) {
		_, _ = fmt.Fprintln(osStderr, "gregalectl obs collection-history: invalid response")
		return 1
	}
	if *details && out.FailureDetails == nil {
		_, _ = fmt.Fprintln(osStderr, "gregalectl obs collection-history: historical failure details unknown (not supplied by server)")
		return 1
	}
	if *jsonOut || jsonOutput || jsonFlagEnabled(args) {
		return emitOperatorJSON(out)
	}

	writeCollectionCoverage(osStdout, &out.Coverage)
	_, _ = fmt.Fprintf(osStdout, "collection=%s intervals=%d limit=%d truncated=%t\n", out.Collection.CollectionState, out.TotalIntervals, out.Limit, out.Truncated)
	for _, item := range out.Items {
		_, _ = fmt.Fprintf(osStdout, "from=%s until=%s state=%s reason=%s\n", item.From.UTC().Format(time.RFC3339Nano), item.Until.UTC().Format(time.RFC3339Nano), item.State, item.Reason)
	}
	if out.FailureDetails != nil {
		writeCollectionFailureDetails(osStdout, *out.FailureDetails)
	}
	return 0
}
