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
		writeServingProbeDiagnostics(w, probe.Diagnostics)
	}
	writeServingProbeNotifications(w, out.ProbeNotifications)
	_, _ = fmt.Fprintln(w, "coverage=expected_serving_probes url_identity=sha256_exact_url direct_probe=not_executed notification_delivery=unassessed")
}

func writeServingProbeNotifications(w io.Writer, d *api.ObsServingProbeNotificationHealth) {
	if d == nil {
		_, _ = fmt.Fprintln(w, "probe_notifications=unknown reason=server_does_not_report_probe_notifications")
		return
	}
	expected := "unknown"
	if d.ExpectedChannels != nil {
		expected = strconv.Itoa(*d.ExpectedChannels)
	}
	_, _ = fmt.Fprintf(w, "probe_notifications=%s reason=%s expected_channels=%s retained=%d omitted=%d truncated=%t\n", d.State, d.Reason, expected, len(d.Channels), d.Omitted, d.Truncated)
	if c := d.Counts; c != nil {
		_, _ = fmt.Fprintf(w, "  disabled=%d unknown=%d last_submission_succeeded=%d last_submission_failed=%d\n", c.Disabled, c.Unknown, c.LastSubmissionSucceeded, c.LastSubmissionFailed)
	}
	writeServingProbeNotificationItems(w, d.Channels)
	_, _ = fmt.Fprintln(w, "  evidence=last_completed_submission provider_receipt=unassessed human_receipt=unassessed")
}

func writeServingProbeNotificationItems(w io.Writer, items []api.ObsServingProbeNotification) {
	for _, item := range items {
		evidence := ""
		if item.EvidenceReason != "" {
			evidence = " evidence_reason=" + item.EvidenceReason
		}
		_, _ = fmt.Fprintf(w, "  node=%s channel=%s state=%s reason=%s%s failure_code=%s evaluated_at=%s last_run_at=%s last_attempt_at=%s last_success_at=%s\n", item.Node, item.Channel, item.State, item.Reason, evidence, item.FailureCode, servingTime(item.EvaluatedAt), servingTime(item.LastRunAt), servingTime(item.LastAttemptAt), servingTime(item.LastSuccessAt))
	}
}

func writeServingProbeDiagnostics(w io.Writer, d *api.ObsServingProbeDiagnostics) {
	if d == nil {
		_, _ = fmt.Fprintln(w, "    diagnostics=unknown reason=server_does_not_report_diagnostics")
	} else if d.State == "unknown" {
		_, _ = fmt.Fprintf(w, "    diagnostics=unknown reason=%s\n", d.Reason)
	} else {
		_, _ = fmt.Fprintf(w, "    diagnostics=available failure_code=%s first_failure_code=%s attempts=%d http_status=%d recovered_on_retry=%t evaluated_at=%s\n", d.FailureCode, d.FirstFailureCode, *d.Attempts, *d.HTTPStatus, *d.RecoveredOnRetry, servingTime(d.EvaluatedAt))
	}
}
