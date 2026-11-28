package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// parkedWindowInstanceLimit bounds how many parked instances one
// `gregale logs --since` reads from the retained archive.
const parkedWindowInstanceLimit = 5

// parkedWindowLogsClient is the API surface the parked-window pass needs.
type parkedWindowLogsClient interface {
	ListInstancesWithHistory(ctx context.Context, slug string, history bool) ([]api.InstanceResponse, error)
	StreamAppArchivedLogs(ctx context.Context, slug string, s api.ArchiveLogSelector) (io.ReadCloser, error)
}

type parkedWindowInstance struct {
	ID    string
	Start time.Time
	Days  []string
}

// liveLogStates are the instance states whose output the live log stream
// still serves from the instance's ring buffer.
var liveLogStates = map[string]bool{"running": true, "waking": true, "cold_booting": true, "restoring": true, "snapshotting": true, "parking": true}

// parkedWindowInstances picks the instances that are no longer running but
// were active inside [since, now]. The live stream only reads running
// instances, so their output exists only in the retained archive (H10-100).
// It returns the newest parkedWindowInstanceLimit of them, oldest first, each
// with the UTC days its activity in the window spans.
func parkedWindowInstances(instances []api.InstanceResponse, since, now time.Time) []parkedWindowInstance {
	out := allParkedWindowInstances(instances, since, now)
	if len(out) > parkedWindowInstanceLimit {
		out = out[len(out)-parkedWindowInstanceLimit:]
	}
	return out
}

// allParkedWindowInstances is parkedWindowInstances without the limit.
func allParkedWindowInstances(instances []api.InstanceResponse, since, now time.Time) []parkedWindowInstance {
	var out []parkedWindowInstance
	for _, ins := range instances {
		if liveLogStates[ins.State] {
			continue
		}
		start, err := time.Parse(time.RFC3339, ins.StartedAt)
		if err != nil {
			continue
		}
		end, known := start, false
		for _, raw := range []string{ins.LastRequestAt, ins.ParkedAt} {
			if at, err := time.Parse(time.RFC3339, raw); err == nil && at.After(end) {
				end, known = at, true
			}
		}
		if !known {
			// A stopped or failed instance (replaced by a deploy, crashed)
			// or one that served no tracked request records no end here, so
			// it may have run up to now. Its lines are filtered by write time
			// below, so reading its window days only costs a fetch; dropping it
			// hid a long-running instance's logs from --since (prod findings
			// 2026-11-15 #11).
			end = now
		}
		if end.Before(since) || start.After(now) {
			continue
		}
		from := start
		if from.Before(since) {
			from = since
		}
		var days []string
		for d := from.UTC().Truncate(24 * time.Hour); !d.After(end.UTC()); d = d.Add(24 * time.Hour) {
			days = append(days, d.Format("2006-01-02"))
		}
		out = append(out, parkedWindowInstance{ID: ins.ID, Start: start, Days: days})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Start.Before(out[j].Start) })
	return out
}

// printParkedWindowLogs prints the retained lines written at or after since by
// instances that parked inside the window, each under a stderr header, and
// returns how many lines it printed. A missing or lagging archive day is
// reported on stderr, not fatal: archives trail the live ring.
func printParkedWindowLogs(ctx context.Context, client parkedWindowLogsClient, slug string, filter api.LogFilter, since time.Time, out, errOut io.Writer) int {
	instances, err := client.ListInstancesWithHistory(ctx, slug, true)
	if err != nil {
		return 0
	}
	printed := 0
	now := time.Now().UTC()
	all := allParkedWindowInstances(instances, since, now)
	if skipped := len(all) - parkedWindowInstanceLimit; skipped > 0 {
		// Say so instead of silently reading only the newest few.
		oldest := all[0]
		_, _ = fmt.Fprintf(errOut, "(%d earlier instances also ran in this window; read one with `gregale logs %s --archive --instance %s --date %s`, or narrow --since)\n",
			skipped, slug, oldest.ID, oldest.Days[0])
	}
	for _, ins := range parkedWindowInstances(instances, since, now) {
		header := false
		for _, day := range ins.Days {
			body, err := client.StreamAppArchivedLogs(ctx, slug, api.ArchiveLogSelector{InstanceID: ins.ID, Date: day})
			if err != nil {
				_, _ = fmt.Fprintf(errOut, "(retained logs of instance %s for %s unavailable: %v)\n", ins.ID, day, err)
				continue
			}
			dec := api.NewDecoder(body)
			dec.SetCloseFn(body.Close)
			_, _ = consumeLogStream(ctx, dec.Events(), dec.Errors(), func(e api.Event) (bool, int) {
				if e.Event == "end" {
					return true, 0
				}
				if e.Data == "" || !retainedLineMatches(e.Data, filter.Grep) || !retainedLineLevelMatches(e.Data, filter.Level) || !writtenAtOrAfter(e.Data, since) {
					return false, 0
				}
				if !header {
					// started_at is the moment the instance became ready, after
					// its boot lines were written; "started" put the boot output
					// before the start (prod hunt r71).
					_, _ = fmt.Fprintf(errOut, "Retained logs of parked instance %s (ready %s).\n", ins.ID, ins.Start.UTC().Format(time.RFC3339))
					header = true
				}
				_, _ = fmt.Fprintln(out, formatRuntimeLogLine(e.Data))
				printed++
				return false, 0
			})
			_ = dec.Close()
		}
	}
	return printed
}

func writtenAtOrAfter(data string, since time.Time) bool {
	var event api.LogEvent
	if json.Unmarshal([]byte(data), &event) != nil {
		return false
	}
	at, err := time.Parse(time.RFC3339Nano, event.WrittenAt)
	return err == nil && !at.Before(since)
}
