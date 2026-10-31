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
	if *jsonOut || jsonOutput || jsonFlagEnabled(args) {
		return emitOperatorJSON(out)
	}
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
	return 0
}

func notificationNumber(signal api.ObsNotificationSignal) string {
	if signal.State != api.ObsHealthSignalHealthy || signal.Value == nil {
		return "unknown"
	}
	return strconv.FormatFloat(*signal.Value, 'f', -1, 64)
}
