package mcpmanagement

import (
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/loglevel"
)

// parkedArchiveUploadWindow covers vmmd's archive flush interval (5 minutes)
// with margin, as the CLI does.
const parkedArchiveUploadWindow = 10 * time.Minute

// parkedLogs answers get_logs for an app with no running instance from its
// newest instance's retained (archived) logs. The live stream used to reach
// the agent as "Log backend is degraded; retry later", which no retry fixed,
// for every parked app: the usual state of a scale-to-zero app.
func (m *management) parkedLogs(ctx context.Context, args logsArgs, limit int) (any, error) {
	var since time.Time
	if args.Since != "" {
		since, _ = time.Parse(time.RFC3339Nano, args.Since)
	}
	instances, err := m.client.ListInstancesWithHistory(ctx, args.App, true)
	if err != nil {
		return nil, err
	}
	if args.DeploymentID != "" {
		kept := instances[:0]
		for _, ins := range instances {
			if ins.DeploymentID == args.DeploymentID {
				kept = append(kept, ins)
			}
		}
		instances = kept
	}
	instance, selector, ok := api.ParkedLogsSelection(instances, since)
	if !ok {
		return nil, m.noParkedLogs(ctx, args.App)
	}
	body, err := m.logsClient().StreamAppArchivedLogs(ctx, args.App, selector)
	if err != nil {
		return nil, err
	}
	defer func() { _ = body.Close() }()
	source := &countingReader{r: io.LimitReader(body, maxLogStreamBytes)}
	result, err := decodeLogStream(ctx, source, limit, m.clean, retainedLineFilter(args, since))
	if err != nil {
		return nil, err
	}
	if source.n >= maxLogStreamBytes {
		result.Truncated = true
	}
	switch result.endReason {
	case "", "archive_complete", "archive_missing":
	default:
		return nil, invalid("logs_unavailable", "The log archive is temporarily unavailable; retry shortly")
	}
	result.Source, result.InstanceID = "archive", instance.ID
	result.Note = fmt.Sprintf("%s is parked; these are the retained logs of its most recent instance %s (started %s, UTC day %s).", args.App, instance.ID, instance.StartedAt, selector.Date)
	if len(result.Items) == 0 {
		result.Note += " " + parkedEmptyNote(instance, result.endReason, time.Now())
	}
	return result, nil
}

// noParkedLogs explains why there are no logs to read: no deployment serves
// the app, or no instance ran in the requested window.
func (m *management) noParkedLogs(ctx context.Context, slug string) error {
	if app, err := m.client.GetApp(ctx, slug); err == nil && app.DeploymentAvailability == api.AppDeploymentAvailabilityMissing {
		return invalid("no_live_deployment", fmt.Sprintf("%s has no deployment receiving traffic, so it has no runtime logs; check its deployments with gregale_list_app_deployments", slug))
	}
	return invalid("app_logs_unavailable", fmt.Sprintf("%s has no running instance and none ran in the requested window; a request to the app wakes it, then read its logs again", slug))
}

// parkedEmptyNote says why a parked instance shows no lines.
func parkedEmptyNote(ins api.InstanceResponse, reason string, now time.Time) string {
	stopped, err := time.Parse(time.RFC3339, ins.ParkedAt)
	if err != nil {
		stopped, err = time.Parse(time.RFC3339, ins.LastRequestAt)
	}
	if err == nil && !stopped.After(now) && now.Sub(stopped) <= parkedArchiveUploadWindow {
		return "It stopped at " + stopped.UTC().Format("15:04:05 UTC") + "; retained logs reach the archive up to 5 minutes after a park, so retry shortly."
	}
	if reason == "archive_missing" {
		return "No archive exists for that instance and day; the instance may have printed nothing, or the archive aged out of the plan's retention."
	}
	return "No retained line matches: the instance printed nothing matching, and an instance restored from a snapshot does not repeat startup output printed before the snapshot was taken."
}

// retainedLineFilter applies grep, level and since to archived lines the way
// the live stream applies them server-side: grep is a case-insensitive
// literal, level a floor on the parsed or detected severity.
func retainedLineFilter(args logsArgs, since time.Time) func(api.LogEvent) bool {
	grep := strings.ToLower(args.Grep)
	floor := loglevel.Rank(strings.ToLower(args.Level))
	return func(e api.LogEvent) bool {
		if grep != "" && !strings.Contains(strings.ToLower(e.Line), grep) {
			return false
		}
		if args.Level != "" && floor >= 0 {
			level := e.Level
			if level == "" {
				level = loglevel.Detect(e.Line)
			}
			if level == "" || loglevel.Rank(level) < floor {
				return false
			}
		}
		if !since.IsZero() {
			if at, err := time.Parse(time.RFC3339Nano, e.WrittenAt); err == nil && at.Before(since) {
				return false
			}
		}
		return true
	}
}
