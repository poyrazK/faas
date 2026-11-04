package operatorobs

import (
	"context"
	"regexp"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// WakeTimelinePath is the admin read of one wake's events.
const WakeTimelinePath = "/v1/admin/obs/wakes/{wake_id}"

// wake_id is a UUIDv7 minted by schedd (and the OTel trace_id in hex on
// warm paths); accept those shapes and nothing that could widen the jsonb
// lookup.
var wakeIDRE = regexp.MustCompile(`^[A-Za-z0-9-]{8,64}$`)

// ValidWakeID reports whether id has a wake_id's shape.
func ValidWakeID(id string) bool { return wakeIDRE.MatchString(id) }

// WakeEventsReader is the store query the timeline needs.
type WakeEventsReader interface {
	ListEventsByWakeID(ctx context.Context, wakeID string, since time.Time, limit int) ([]state.Event, error)
}

// WakeTimeline returns every event recorded for wakeID, oldest first, up to
// api.ObsWakeTimelineEventsMax. It reads one more row than it returns so a
// longer history is reported as truncated rather than silently cut.
func WakeTimeline(ctx context.Context, reader WakeEventsReader, wakeID string) (api.ObsWakeTimeline, error) {
	out := api.ObsWakeTimeline{WakeID: wakeID, Events: []api.ObsWakeEvent{}}
	if !ValidWakeID(wakeID) {
		return out, state.ErrInvalidArgument
	}
	ctx, cancel := context.WithTimeout(ctx, api.ObsWakeTimelineBudget)
	defer cancel()
	rows, err := reader.ListEventsByWakeID(ctx, wakeID, time.Time{}, api.ObsWakeTimelineEventsMax+1)
	if err != nil {
		return out, err
	}
	if len(rows) > api.ObsWakeTimelineEventsMax {
		rows = rows[:api.ObsWakeTimelineEventsMax]
		out.Truncated = true
	}
	for _, r := range rows {
		ev := api.ObsWakeEvent{At: r.At.UTC(), Kind: r.Kind, Actor: r.Actor, Data: r.Data}
		if r.Subject != nil {
			ev.Subject = r.Subject.String()
			if out.Subject == "" {
				out.Subject = ev.Subject
			}
		}
		if r.TraceID != nil {
			ev.TraceID = *r.TraceID
		}
		out.Events = append(out.Events, ev)
	}
	return out, nil
}
