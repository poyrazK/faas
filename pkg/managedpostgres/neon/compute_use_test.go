// adr: 20261025094915 — amendment 2026-11-05: the cap sweep reads use from
// the consumption report, which is current, not the quota counter.
package neon

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/managedpostgres"
)

// consumptionFixture serves hourly compute frames: hours maps each hour to
// its compute_unit_seconds; an hour outside it is missing from the report.
type consumptionFixture struct {
	mu      sync.Mutex
	hours   map[time.Time]int64
	queries [][2]time.Time
}

func (f *consumptionFixture) provider(t *testing.T, now time.Time) *Provider {
	t.Helper()
	provider := testProvider(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		query := r.URL.Query()
		if r.URL.Path != "/api/v2/consumption_history/v2/projects" || query.Get("project_ids") != "quiet-river-12345678" ||
			query.Get("granularity") != "hourly" || query.Get("metrics") != "compute_unit_seconds" || query.Get("org_id") != "org-gregale-12345678" {
			t.Errorf("unexpected request %s", r.URL)
			writeResponse(t, w, http.StatusNotFound, nil)
			return
		}
		from, errFrom := time.Parse(time.RFC3339, query.Get("from"))
		to, errTo := time.Parse(time.RFC3339, query.Get("to"))
		if errFrom != nil || errTo != nil {
			t.Errorf("window %q..%q", query.Get("from"), query.Get("to"))
		}
		f.queries = append(f.queries, [2]time.Time{from, to})
		var frames []map[string]any
		for hour := from; hour.Before(to); hour = hour.Add(time.Hour) {
			used, ok := f.hours[hour]
			if !ok {
				continue
			}
			frames = append(frames, map[string]any{"timeframe_start": hour, "timeframe_end": hour.Add(time.Hour),
				"metrics": []map[string]any{{"metric_name": "compute_unit_seconds", "value": used}}})
		}
		writeResponse(t, w, http.StatusOK, map[string]any{"projects": []map[string]any{{"project_id": "quiet-river-12345678",
			"periods": []map[string]any{{"period_id": "period-1", "period_start": "2026-11-01T00:00:00Z", "consumption": frames}}}}})
	}))
	provider.now = func() time.Time { return now }
	return provider
}

func TestComputeUnitSecondsSinceReadsTheOpenHour(t *testing.T) {
	open := time.Date(2026, 11, 5, 11, 0, 0, 0, time.UTC)
	now := open.Add(22 * time.Minute)
	full := map[time.Time]int64{open.Add(-2 * time.Hour): 196, open.Add(-time.Hour): 900, open: 324}
	for _, tc := range []struct {
		name  string
		hours map[time.Time]int64
		from  time.Time
		want  int64
		err   error
	}{
		{name: "closed hours and the open hour", hours: full, from: open.Add(-2 * time.Hour), want: 1420},
		{name: "only the open hour", hours: full, from: open, want: 324},
		{name: "the open hour not reported yet", hours: map[time.Time]int64{open.Add(-time.Hour): 900}, from: open.Add(-time.Hour), want: 900},
		{name: "nothing reported in the open hour yet", hours: map[time.Time]int64{}, from: open, want: 0},
		{name: "a missing closed hour is not zero", hours: map[time.Time]int64{open.Add(-2 * time.Hour): 196, open: 324}, from: open.Add(-2 * time.Hour), err: managedpostgres.ErrUnavailable},
		{name: "a start inside an hour", hours: full, from: open.Add(-30 * time.Minute), err: managedpostgres.ErrInvalid},
		{name: "a start after the open hour", hours: full, from: open.Add(time.Hour), err: managedpostgres.ErrInvalid},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := &consumptionFixture{hours: tc.hours}
			got, err := f.provider(t, now).ComputeUnitSecondsSince(context.Background(), "quiet-river-12345678/br-restore-123", tc.from)
			if tc.err != nil {
				if !errors.Is(err, tc.err) {
					t.Fatalf("err %v; want %v", err, tc.err)
				}
				return
			}
			if err != nil || got != tc.want {
				t.Fatalf("used %d, %v; want %d", got, err, tc.want)
			}
		})
	}
}

func TestComputeUnitSecondsSinceSplitsLongWindows(t *testing.T) {
	month := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	now := month.Add(10*24*time.Hour + 5*time.Minute)
	f := &consumptionFixture{hours: map[time.Time]int64{}}
	for hour := month; !hour.After(now); hour = hour.Add(time.Hour) {
		f.hours[hour] = 10
	}
	got, err := f.provider(t, now).ComputeUnitSecondsSince(context.Background(), "quiet-river-12345678", month)
	if err != nil || got != 10*(10*24+1) {
		t.Fatalf("used %d, %v; want %d", got, err, 10*(10*24+1))
	}
	if len(f.queries) != 2 || !f.queries[0][1].Equal(month.Add(computeUseReadSpan)) || !f.queries[1][1].Equal(now.Truncate(time.Hour).Add(time.Hour)) {
		t.Fatalf("queries %v; want two reads of at most %s, the last through the open hour", f.queries, computeUseReadSpan)
	}
}
