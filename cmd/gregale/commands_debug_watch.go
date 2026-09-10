package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

const (
	debugWatchDefaultInterval = 5 * time.Second
	debugWatchMinInterval     = 250 * time.Millisecond
	debugWatchMaxInterval     = time.Hour
	debugWatchSeenCap         = 10_000
)

// debugWatchEvent is the line-oriented machine format emitted by the watch
// commands. Keeping the poll timestamp and event kind in every line lets a
// pipe consumer distinguish a changed row from a quiet heartbeat without
// having to maintain client-side polling state.
type debugWatchEvent struct {
	Type         string                         `json:"type"`
	ObservedAt   string                         `json:"observed_at"`
	Since        string                         `json:"since,omitempty"`
	Request      *api.DebugTelemetryRequestItem `json:"request,omitempty"`
	Regression   *api.DebugRegressionItem       `json:"regression,omitempty"`
	DeploymentID string                         `json:"deployment_id,omitempty"`
	Route        string                         `json:"route,omitempty"`
}

// cmdDebugRequestsWatch implements a bounded-polling watch over retained
// request telemetry. The API remains the source of truth; this command only
// tracks row fingerprints so collapsed rows that change count/latency are
// emitted again while unchanged rows stay quiet.
func cmdDebugRequestsWatch(args []string) int {
	fs := flag.NewFlagSet("debug requests watch", flag.ContinueOnError)
	since := fs.String("since", "", "lookback window (e.g. 30m, 24h, 3d)")
	route := fs.String("route", "", "route filter (exact match)")
	limit := fs.Int("limit", 20, "max rows per poll (1..200)")
	interval := fs.Duration("interval", debugWatchDefaultInterval, "poll interval (250ms..1h)")
	once := fs.Bool("once", false, "poll once and exit (useful for scripts and tests)")
	flagArgs, positional := normalizeDebugFlagArgs(args, map[string]bool{
		"since": true, "route": true, "limit": true, "interval": true,
	})
	if err := fs.Parse(flagArgs); err != nil {
		return 1
	}
	if len(positional) != 1 {
		PrintUsage(os.Stderr, "usage: gregale debug requests watch [--since D] [--route P] [--limit N] [--interval D] [--once] <slug>", debugCmdDocsTopic)
		return 1
	}
	if *limit < 1 || *limit > 200 {
		fmt.Fprintln(os.Stderr, "--limit must be between 1 and 200")
		return 1
	}
	if err := validateDebugWatchInterval(*interval); err != nil {
		return printErr("Invalid watch interval", err)
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	return runDebugRequestsWatch(ctx, client, positional[0], api.DebugTelemetryListOptions{
		Since: *since, Route: *route, Limit: *limit,
	}, *interval, *once)
}

func runDebugRequestsWatch(ctx context.Context, client *api.Client, slug string, opts api.DebugTelemetryListOptions, interval time.Duration, once bool) int {
	seen := make(map[string]string)
	seenOrder := make([]string, 0, debugWatchSeenCap)
	poll := 0
	if !jsonOutput {
		_, _ = fmt.Fprintf(osStdout, "Watching debugger requests for %s (every %s). Ctrl-C to exit.\n", slug, interval)
	}
	for {
		if err := ctx.Err(); err != nil {
			return 130
		}
		resp, err := client.ListAppDebugRequestsWithOptions(ctx, slug, opts)
		if err != nil {
			if ctx.Err() != nil {
				return 130
			}
			if poll == 0 {
				return printErr("Could not list debug requests", err)
			}
			_, _ = fmt.Fprintf(os.Stderr, "debug requests watch: poll %d: %v (continuing)\n", poll+1, err)
		} else {
			changed := make([]api.DebugTelemetryRequestItem, 0, len(resp.Requests))
			for _, request := range resp.Requests {
				fingerprint := debugWatchFingerprint(request)
				if previous, ok := seen[request.ID]; !ok || previous != fingerprint {
					if _, ok := seen[request.ID]; !ok {
						seenOrder = append(seenOrder, request.ID)
					}
					seen[request.ID] = fingerprint
					changed = append(changed, request)
				}
			}
			for len(seenOrder) > debugWatchSeenCap {
				delete(seen, seenOrder[0])
				seenOrder = seenOrder[1:]
			}
			observedAt := time.Now().UTC().Format(time.RFC3339Nano)
			if jsonOutput {
				if len(changed) == 0 {
					_ = writeDebugWatchEvent(debugWatchEvent{Type: "heartbeat", ObservedAt: observedAt, Since: resp.Since})
				} else {
					for i := range changed {
						request := changed[i]
						_ = writeDebugWatchEvent(debugWatchEvent{Type: "request", ObservedAt: observedAt, Since: resp.Since, Request: &request})
					}
				}
			} else if len(changed) == 0 {
				_, _ = fmt.Fprintf(osStdout, "[%s] no new or changed request telemetry\n", observedAt)
			} else {
				_, _ = fmt.Fprintf(osStdout, "[%s] %d new or changed request(s)\n", observedAt, len(changed))
				renderDebugRequestsTable(osStdout, api.DebugTelemetryListResponse{Since: resp.Since, Requests: changed})
			}
		}
		poll++
		if once {
			return 0
		}
		if !waitDebugWatch(ctx, interval) {
			return 130
		}
	}
}

// cmdDebugRegressionsWatch watches active regression observations and emits
// updates plus explicit clear events when an observation disappears.
func cmdDebugRegressionsWatch(args []string) int {
	fs := flag.NewFlagSet("debug regressions watch", flag.ContinueOnError)
	since := fs.String("since", "", "lookback window (e.g. 30m, 24h, 3d)")
	interval := fs.Duration("interval", debugWatchDefaultInterval, "poll interval (250ms..1h)")
	once := fs.Bool("once", false, "poll once and exit (useful for scripts and tests)")
	flagArgs, positional := normalizeDebugFlagArgs(args, map[string]bool{
		"since": true, "interval": true,
	})
	if err := fs.Parse(flagArgs); err != nil {
		return 1
	}
	if len(positional) != 1 {
		PrintUsage(os.Stderr, "usage: gregale debug regressions watch [--since D] [--interval D] [--once] <slug>", debugCmdDocsTopic)
		return 1
	}
	if err := validateDebugWatchInterval(*interval); err != nil {
		return printErr("Invalid watch interval", err)
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	return runDebugRegressionsWatch(ctx, client, positional[0], *since, *interval, *once)
}

func runDebugRegressionsWatch(ctx context.Context, client *api.Client, slug, since string, interval time.Duration, once bool) int {
	seen := make(map[string]string)
	poll := 0
	if !jsonOutput {
		_, _ = fmt.Fprintf(osStdout, "Watching debugger regressions for %s (every %s). Ctrl-C to exit.\n", slug, interval)
	}
	for {
		if ctx.Err() != nil {
			return 130
		}
		resp, err := client.ListAppDebugRegressions(ctx, slug, since)
		if err != nil {
			if ctx.Err() != nil {
				return 130
			}
			if poll == 0 {
				return printErr("Could not list regressions", err)
			}
			_, _ = fmt.Fprintf(os.Stderr, "debug regressions watch: poll %d: %v (continuing)\n", poll+1, err)
		} else {
			observedAt := time.Now().UTC().Format(time.RFC3339Nano)
			current := make(map[string]string, len(resp.Regressions))
			changed := make([]api.DebugRegressionItem, 0, len(resp.Regressions))
			for _, regression := range resp.Regressions {
				key := debugRegressionWatchKey(regression)
				fingerprint := debugWatchFingerprint(regression)
				current[key] = fingerprint
				if previous, ok := seen[key]; !ok || previous != fingerprint {
					changed = append(changed, regression)
				}
			}
			cleared := make([]string, 0)
			for key := range seen {
				if _, ok := current[key]; !ok {
					cleared = append(cleared, key)
				}
			}
			seen = current
			if jsonOutput {
				if len(changed) == 0 && len(cleared) == 0 {
					_ = writeDebugWatchEvent(debugWatchEvent{Type: "heartbeat", ObservedAt: observedAt, Since: resp.Since})
				} else {
					for i := range changed {
						regression := changed[i]
						_ = writeDebugWatchEvent(debugWatchEvent{Type: "regression", ObservedAt: observedAt, Since: resp.Since, Regression: &regression})
					}
					for _, key := range cleared {
						deploymentID, route := splitDebugRegressionWatchKey(key)
						_ = writeDebugWatchEvent(debugWatchEvent{Type: "regression_cleared", ObservedAt: observedAt, Since: resp.Since, DeploymentID: deploymentID, Route: route})
					}
				}
			} else if len(changed) == 0 && len(cleared) == 0 {
				_, _ = fmt.Fprintf(osStdout, "[%s] no regression changes\n", observedAt)
			} else {
				_, _ = fmt.Fprintf(osStdout, "[%s] %d changed regression(s), %d cleared\n", observedAt, len(changed), len(cleared))
				if len(changed) > 0 {
					renderDebugRegressionsTable(osStdout, api.DebugRegressionsResponse{Since: resp.Since, Regressions: changed})
				}
				for _, key := range cleared {
					deploymentID, route := splitDebugRegressionWatchKey(key)
					_, _ = fmt.Fprintf(osStdout, "cleared\t%s\t%s\n", deploymentID, route)
				}
			}
		}
		poll++
		if once {
			return 0
		}
		if !waitDebugWatch(ctx, interval) {
			return 130
		}
	}
}

func validateDebugWatchInterval(interval time.Duration) error {
	if interval < debugWatchMinInterval || interval > debugWatchMaxInterval {
		return fmt.Errorf("--interval must be between %s and %s (got %s)", debugWatchMinInterval, debugWatchMaxInterval, interval)
	}
	return nil
}

func waitDebugWatch(ctx context.Context, interval time.Duration) bool {
	timer := time.NewTimer(interval)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

func writeDebugWatchEvent(event debugWatchEvent) error {
	return json.NewEncoder(osStdout).Encode(event)
}

func debugWatchFingerprint(value any) string {
	b, err := json.Marshal(value)
	if err != nil {
		return fmt.Sprintf("%#v", value)
	}
	return string(b)
}

func debugRegressionWatchKey(regression api.DebugRegressionItem) string {
	return regression.DeploymentID + "\x00" + regression.Route
}

func splitDebugRegressionWatchKey(key string) (string, string) {
	for i := 0; i < len(key); i++ {
		if key[i] == 0 {
			return key[:i], key[i+1:]
		}
	}
	return key, ""
}
