package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"slices"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

type inspectWatchOptions struct {
	interval, timeout time.Duration
}

// Watch events have their own schema; summary preserves inspect's snapshot schema.
// An unavailable event intentionally has no summary: prior data may be stale.
type inspectWatchEvent struct {
	SchemaVersion int             `json:"schema_version"`
	Type          string          `json:"type"`
	ObservedAt    time.Time       `json:"observed_at"`
	AppSlug       string          `json:"app_slug"`
	Changed       []string        `json:"changed,omitempty"`
	Summary       *inspectSummary `json:"summary,omitempty"`
	Message       string          `json:"message,omitempty"`
}

func validateInspectWatchOptions(fs *flag.FlagSet, watch, leaf bool, options inspectWatchOptions) error {
	explicit := false
	fs.Visit(func(f *flag.Flag) {
		if f.Name == "interval" || f.Name == "timeout" {
			explicit = true
		}
	})
	if !watch && explicit {
		return errors.New("--interval and --timeout require --watch")
	}
	if watch && leaf {
		return errors.New("--watch cannot be combined with --upstreams or --errors")
	}
	if options.interval < api.InspectWatchIntervalMin || options.interval > api.InspectWatchIntervalMax || options.timeout < 0 {
		return errors.New("--interval must be between 1s and 1h; --timeout must be non-negative")
	}
	return nil
}

func cmdInspectWatch(slug string, options inspectWatchOptions) int {
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	signalCtx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	ctx := signalCtx
	if options.timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, options.timeout)
		defer cancel()
	}
	if !jsonOutput {
		_, _ = fmt.Fprintf(osStdout, "Watching %s every %s. Ctrl-C to exit.\n", slug, options.interval)
	}
	err = runInspectWatch(ctx, slug, options.interval, func(readCtx context.Context) (inspectSummary, error) {
		return collectInspectSummary(readCtx, client, slug)
	}, emitInspectWatchEvent)
	return inspectWatchExitCode(err)
}

func inspectWatchExitCode(err error) int {
	if errors.Is(err, context.Canceled) {
		return 130
	}
	if errors.Is(err, context.DeadlineExceeded) {
		_, _ = fmt.Fprintln(osStderr, "Inspect watch duration elapsed; previous observations may be stale.")
		return 124
	}
	if err != nil {
		return printErr("Inspect watch stopped", err)
	}
	return 0
}

func runInspectWatch(ctx context.Context, slug string, interval time.Duration, collect func(context.Context) (inspectSummary, error), emit func(inspectWatchEvent) error) error {
	var previous *inspectSummary
	unavailable := false
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		readCtx, cancel := context.WithTimeout(ctx, api.InspectWatchReadTimeout)
		summary, err := collect(readCtx)
		cancel()
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err != nil {
			if inspectWatchPermanentError(err) {
				return err
			}
			if !unavailable {
				if err := emit(inspectWatchEvent{SchemaVersion: 1, Type: "unavailable", ObservedAt: time.Now().UTC(), AppSlug: slug,
					Message: "Current app state could not be read; previous observations may be stale. Retrying."}); err != nil {
					return err
				}
			}
			unavailable, previous = true, nil
		} else {
			changed, err := inspectWatchChanges(previous, summary)
			if err != nil {
				return err
			}
			if previous == nil || len(changed) > 0 {
				kind := "change"
				if previous == nil {
					kind, changed = "snapshot", nil
				}
				if err := emit(inspectWatchEvent{SchemaVersion: 1, Type: kind, ObservedAt: time.Now().UTC(), AppSlug: slug, Changed: changed, Summary: &summary}); err != nil {
					return err
				}
			}
			previous, unavailable = &summary, false
		}
		timer := time.NewTimer(interval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}

func inspectWatchPermanentError(err error) bool {
	var apiErr *api.APIError
	return errors.As(err, &apiErr) && apiErr.Problem.Status >= 400 && apiErr.Problem.Status < 500 && apiErr.Problem.Status != 408 && apiErr.Problem.Status != 429
}

// Ignore observation clock churn and metadata ordering, while retaining status,
// coverage, missing sources, operation identity, attempts and safe failure codes.
func inspectWatchComparable(summary inspectSummary) inspectSummary {
	summary.Unavailable = sortedUniqueStrings(summary.Unavailable)
	if summary.Operational != nil {
		operation := *summary.Operational
		operation.CheckedAt = time.Time{}
		operation.Monitoring.CheckedAt = nil
		operation.Monitoring.WindowStart, operation.Monitoring.WindowEnd = nil, nil
		operation.Recovery.Rollbacks = slices.Clone(operation.Recovery.Rollbacks)
		operation.Recovery.Restarts = slices.Clone(operation.Recovery.Restarts)
		for i := range operation.Recovery.Rollbacks {
			operation.Recovery.Rollbacks[i].UpdatedAt = time.Time{}
		}
		slices.SortFunc(operation.Recovery.Rollbacks, func(a, b api.AppOperationalRollback) int { return strings.Compare(a.ID, b.ID) })
		slices.SortFunc(operation.Recovery.Restarts, func(a, b api.RuntimeConfigRestartStatusResponse) int { return strings.Compare(a.WakeID, b.WakeID) })
		summary.Operational = &operation
	}
	return summary
}

func inspectWatchChanges(previous *inspectSummary, current inspectSummary) ([]string, error) {
	if previous == nil {
		return nil, nil
	}
	before, after := inspectWatchComparable(*previous), inspectWatchComparable(current)
	sections := []struct {
		name        string
		before, now any
	}{
		{"app", before.App, after.App},
		{"runtime", before.Runtime, after.Runtime},
		{"resources", before.Resources, after.Resources},
		{"signals", []any{before.API, before.Data}, []any{after.API, after.Data}},
		{"release", before.Release, after.Release},
		{"current_operations", before.Operational, after.Operational},
		{"recommendations", before.Recommendations, after.Recommendations},
		{"unavailable", before.Unavailable, after.Unavailable},
	}
	var changed []string
	for _, section := range sections {
		old, err := json.Marshal(section.before)
		if err != nil {
			return nil, err
		}
		now, err := json.Marshal(section.now)
		if err != nil {
			return nil, err
		}
		if !bytes.Equal(old, now) {
			changed = append(changed, section.name)
		}
	}
	return changed, nil
}

func emitInspectWatchEvent(event inspectWatchEvent) error {
	if jsonOutput {
		return json.NewEncoder(osStdout).Encode(event)
	}
	var output bytes.Buffer
	switch event.Type {
	case "unavailable":
		_, _ = fmt.Fprintf(&output, "[%s] %s\n", event.ObservedAt.Format(time.RFC3339), event.Message)
	case "snapshot":
		_, _ = fmt.Fprintf(&output, "\n[%s] Current observation\n", event.ObservedAt.Format(time.RFC3339))
		renderInspectSummaryHuman(&output, *event.Summary)
	default:
		_, _ = fmt.Fprintf(&output, "\n[%s] Changed: %s\n", event.ObservedAt.Format(time.RFC3339), strings.Join(event.Changed, ", "))
		renderInspectWatchChanges(&output, *event.Summary, event.Changed)
	}
	_, err := osStdout.Write(output.Bytes())
	return err
}

func renderInspectWatchChanges(w io.Writer, summary inspectSummary, changed []string) {
	for _, section := range changed {
		switch section {
		case "app":
			renderInspectApp(w, summary.App)
		case "runtime":
			renderInspectRuntime(w, summary.Runtime)
		case "resources":
			renderInspectResources(w, summary.Resources)
		case "signals":
			renderInspectSignals(w, summary)
		case "release":
			renderInspectRelease(w, summary.Release, containsString(summary.Unavailable, "deployment"))
		case "current_operations":
			renderInspectOperational(w, summary.Operational)
		case "recommendations":
			renderInspectRecommendations(w, summary.Recommendations)
		case "unavailable":
			if len(summary.Unavailable) == 0 {
				_, _ = fmt.Fprintln(w, "  unavailable sources: none")
			} else {
				_, _ = fmt.Fprintf(w, "  unavailable: %s\n", strings.Join(summary.Unavailable, ", "))
			}
		}
	}
}
