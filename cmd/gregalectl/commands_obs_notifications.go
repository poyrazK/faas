package main

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/operatorobs"
)

func cmdObsNotificationHealth(args []string) int {
	fs := flag.NewFlagSet(subObsNotificationHealth, flag.ContinueOnError)
	fs.SetOutput(osStderr)
	token := fs.String("admin-token", "", obsAdminTokenUsage)
	timeout := fs.Duration("timeout", 10*time.Second, "HTTP timeout")
	jsonOut := fs.Bool("json", false, "emit structured JSON")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 0 || *timeout <= 0 {
		_, _ = fmt.Fprintln(osStderr, "gregalectl obs notification-health: no positional arguments and a positive timeout required")
		return 2
	}
	cred, ok := resolveObsCredential("obs notification-health", *token)
	if !ok {
		return 2
	}
	var out api.ObsNotificationHealth
	if err := fetchObsBundleSection(context.Background(), &http.Client{Timeout: *timeout}, apidBase()+operatorobs.NotificationHealthPath, cred, subObsNotificationHealth, &out); err != nil {
		_, _ = fmt.Fprintf(osStderr, "gregalectl obs %v\n", err)
		return 1
	}
	if out.GeneratedAt.IsZero() || out.State == "" || out.AlertmanagerAPI.State == "" || out.Signals == nil || out.Integrations == nil || out.Limitations == nil {
		_, _ = fmt.Fprintln(osStderr, "gregalectl obs notification-health: invalid response")
		return 1
	}
	if out.ReceiverHealth != nil && !validObsNotificationReceivers(*out.ReceiverHealth, out.GeneratedAt) {
		_, _ = fmt.Fprintln(osStderr, "gregalectl obs notification-health: invalid receiver health response")
		return 1
	}
	if *jsonOut || jsonOutput || jsonFlagEnabled(args) {
		return emitOperatorJSON(out)
	}
	writeObsNotificationHealth(out)
	return 0
}

func writeObsNotificationHealth(out api.ObsNotificationHealth) {
	_, _ = fmt.Fprintf(osStdout, "notification_health=%s alertmanager_api=%s window_seconds=%d generated_at=%s\n", out.State, out.AlertmanagerAPI.State, out.WindowSeconds, out.GeneratedAt.UTC().Format(time.RFC3339))
	names := make([]string, 0, len(out.Signals))
	for name := range out.Signals {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		signal := out.Signals[name]
		_, _ = fmt.Fprintf(osStdout, "  %s state=%s value=%s reason=%s\n", name, signal.State, notificationNumber(signal), signal.Reason)
	}
	for _, integration := range out.Integrations {
		_, _ = fmt.Fprintf(osStdout, "  integration=%s attempts=%s attempts_state=%s failures=%s failures_state=%s\n", integration.Integration, notificationNumber(integration.Attempts), integration.Attempts.State, notificationNumber(integration.Failures), integration.Failures.State)
	}
	_, _ = fmt.Fprintln(osStdout, "coverage=aggregate_integration_counters per_episode_delivery=unknown human_receipt=unknown")
	writeObsNotificationReceivers(out.ReceiverHealth)
}

func writeObsNotificationReceivers(out *api.ObsNotificationReceiverHealth) {
	if out == nil {
		_, _ = fmt.Fprintln(osStdout, "receiver_health=unknown reason=server_does_not_report_receivers")
		return
	}
	_, _ = fmt.Fprintf(osStdout, "receiver_health=%s reason=%s collector=%s collector_state=%s expected_groups=%s inventory_state=%s\n", out.State, out.Reason, notificationNumber(out.Collector), out.Collector.State, notificationNumber(out.Inventory), out.Inventory.State)
	for _, group := range out.Groups {
		expected, success := "unknown", "unknown"
		if group.Expected != nil {
			expected = strconv.FormatBool(*group.Expected)
		}
		if group.LastSuccessObservation.State == api.ObsHealthSignalHealthy && group.LastSuccessObservation.Value != nil {
			success = time.UnixMilli(int64(*group.LastSuccessObservation.Value * 1000)).UTC().Format(time.RFC3339Nano)
		}
		_, _ = fmt.Fprintf(osStdout, "  receiver=%s integration=%s expected=%s state=%s reason=%s requests=%s requests_state=%s failures=%s failures_state=%s last_success_observed_at=%s last_success_state=%s\n", group.Receiver, group.Integration, expected, group.State, group.Reason, notificationNumber(group.Requests), group.Requests.State, notificationNumber(group.Failures), group.Failures.State, success, group.LastSuccessObservation.State)
	}
	_, _ = fmt.Fprintln(osStdout, "receiver_coverage=deployment_intent_and_completed_requests success_time=collector_confirmation per_episode_delivery=unknown human_receipt=unknown external_watchdog_receipt=unknown")
}

func notificationNumber(signal api.ObsNotificationSignal) string {
	if signal.State != api.ObsHealthSignalHealthy || signal.Value == nil {
		return "unknown"
	}
	return strconv.FormatFloat(*signal.Value, 'f', -1, 64)
}
