package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/operatorobs"
)

func cmdObsServingHealth(args []string) int {
	fs := flag.NewFlagSet(subObsServingHealth, flag.ContinueOnError)
	fs.SetOutput(osStderr)
	token := fs.String("admin-token", "", obsAdminTokenUsage)
	timeout := fs.Duration("timeout", 10*time.Second, "HTTP timeout")
	jsonOut := fs.Bool("json", false, "emit structured JSON")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 0 || *timeout <= 0 {
		_, _ = fmt.Fprintln(osStderr, "gregalectl obs serving-health: no positional arguments and a positive timeout required")
		return 2
	}
	cred, ok := resolveObsCredential("obs serving-health", *token)
	if !ok {
		return 2
	}
	client := &http.Client{Timeout: *timeout, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	var out api.ObsServingHealth
	if err := fetchObsBundleSection(context.Background(), client, apidBase()+operatorobs.ServingHealthPath, cred, subObsServingHealth, &out); err != nil {
		_, _ = fmt.Fprintf(osStderr, "gregalectl obs %v\n", err)
		return 1
	}
	if !validObsServingHealth(out) {
		_, _ = fmt.Fprintln(osStderr, "gregalectl obs serving-health: invalid response")
		return 1
	}
	if *jsonOut || jsonOutput || jsonFlagEnabled(args) {
		return emitOperatorJSON(out)
	}
	writeObsServingHealth(osStdout, out)
	return 0
}

func servingTime(value *time.Time) string {
	if value == nil {
		return "unknown"
	}
	return value.UTC().Format(time.RFC3339Nano)
}

func writeObsServingHealth(w io.Writer, out api.ObsServingHealth) {
	configured, expected := "unknown", "unknown"
	if out.Inventory.Configured != nil {
		configured = strconv.FormatBool(*out.Inventory.Configured)
	}
	if out.Inventory.ExpectedPairs != nil {
		expected = strconv.Itoa(*out.Inventory.ExpectedPairs)
	}
	_, _ = fmt.Fprintf(w, "serving_health=%s reason=%s inventory=%s configured=%s expected_pairs=%s generated_at=%s\n", out.State, out.Reason, out.Inventory.State, configured, expected, out.GeneratedAt.UTC().Format(time.RFC3339Nano))
	if c := out.Counts; c != nil {
		_, _ = fmt.Fprintf(w, "reporting=%d healthy=%d missing=%d stale=%d invalid=%d content_disabled=%d budget_mismatch=%d failing=%d unknown=%d\n", c.Reporting, c.Healthy, c.Missing, c.Stale, c.Invalid, c.ContentDisabled, c.BudgetMismatch, c.Failing, c.Unknown)
	}
	for _, probe := range out.Probes {
		reporting := "unknown"
		if probe.Reporting != nil {
			reporting = strconv.FormatBool(*probe.Reporting)
		}
		_, _ = fmt.Fprintf(w, "  node=%s url_id=%s state=%s reason=%s reporting=%s evaluated_at=%s last_run_at=%s expected_first_byte_seconds=%s max_result_age_seconds=%s\n", probe.Node, probe.URLID, probe.State, probe.Reason, reporting, servingTime(probe.EvaluatedAt), servingTime(probe.LastRunAt), strconv.FormatFloat(probe.ExpectedFirstByteSeconds, 'g', -1, 64), strconv.FormatFloat(probe.MaxResultAgeSeconds, 'g', -1, 64))
	}
	_, _ = fmt.Fprintln(w, "coverage=expected_serving_probes url_identity=sha256_exact_url direct_probe=not_executed notification_delivery=unassessed")
}
