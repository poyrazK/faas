package state

import (
	"context"
	"sort"
	"time"

	"github.com/onebox-faas/faas/pkg/publicstatus"
)

// StatusUptimeBuckets returns one daily platform-availability bucket for the
// requested window. Each total is a complete five-minute observation of every
// public platform component; customer invocation outcomes never contribute.
// It is an optional Store capability because the legacy status endpoint must
// remain source-compatible with narrow test doubles and older integrations.
type StatusHistoryStore interface {
	StatusUptimeBuckets(ctx context.Context, since time.Time) ([]StatusUptimeBucket, error)
	ListStatusIncidentsSince(ctx context.Context, since time.Time, limit int) ([]StatusIncident, error)
}

// statusUptimeBuckets is the shared in-memory rollup used by MemStore. A
// five-minute interval is eligible only when all public components have fresh
// telemetry. This keeps a partial write or a monitoring gap distinct from an
// outage. Operational and declared-maintenance intervals are available, which
// matches publicstatus.SummarizeDay.
func statusUptimeBuckets(buckets map[string]StatusBucket, since time.Time) []StatusUptimeBucket {
	type interval struct {
		components map[publicstatus.Component]struct{}
		available  bool
	}
	byInterval := make(map[time.Time]interval)
	for _, bucket := range buckets {
		if bucket.BucketAt.Before(since) || !bucket.HasTelemetry || !publicstatus.ValidComponent(bucket.Component) {
			continue
		}
		at := bucket.BucketAt.UTC().Truncate(5 * time.Minute)
		current, ok := byInterval[at]
		if !ok {
			current = interval{components: make(map[publicstatus.Component]struct{}), available: true}
		}
		current.components[bucket.Component] = struct{}{}
		if bucket.State != publicstatus.StateOperational && bucket.State != publicstatus.StateMaintenance {
			current.available = false
		}
		byInterval[at] = current
	}

	type counts struct{ successful, total int64 }
	byDay := make(map[time.Time]counts)
	componentCount := len(publicstatus.AllComponents())
	for at, interval := range byInterval {
		if len(interval.components) != componentCount {
			continue
		}
		day := time.Date(at.Year(), at.Month(), at.Day(), 0, 0, 0, 0, time.UTC)
		c := byDay[day]
		c.total++
		if interval.available {
			c.successful++
		}
		byDay[day] = c
	}
	out := make([]StatusUptimeBucket, 0, len(byDay))
	for day, c := range byDay {
		out = append(out, StatusUptimeBucket{Day: day, Successful: c.successful, Total: c.total})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Day.Before(out[j].Day) })
	return out
}
