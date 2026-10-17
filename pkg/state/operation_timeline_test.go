// adr: 1195
package state

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func timelineTestSource(kind string, more bool, at ...time.Time) operationTimelineSource {
	source := operationTimelineSource{more: more}
	for i, t := range at {
		label := fmt.Sprintf("%s%d", kind, i+1)
		source.items = append(source.items, operationTimelineItem{
			entry:   api.OperationTimelineEntry{At: t, Kind: label},
			advance: func(c *OperationTimelineCursor) { c.Event = c.Event*10 + int64(len(kind)); c.Step.Step = label },
		})
	}
	return source
}

func timelineTestKinds(entries []api.OperationTimelineEntry) string {
	kinds := make([]string, 0, len(entries))
	for _, e := range entries {
		kinds = append(kinds, e.Kind)
	}
	return strings.Join(kinds, " ")
}

func TestMergeOperationTimeline(t *testing.T) {
	t0 := time.Date(2026, 10, 14, 0, 0, 0, 0, time.UTC)
	at := func(s int) time.Time { return t0.Add(time.Duration(s) * time.Second) }
	for _, tc := range []struct {
		name    string
		sources []operationTimelineSource
		limit   int
		want    string
		next    bool
	}{
		{"interleaves by time", []operationTimelineSource{timelineTestSource("e", false, at(0), at(3)), timelineTestSource("a", false, at(1), at(2))}, 10, "e1 a1 a2 e2", false},
		{"equal times keep kind order", []operationTimelineSource{timelineTestSource("e", false, at(1)), timelineTestSource("a", false, at(1)), timelineTestSource("d", false, at(1))}, 10, "e1 a1 d1", false},
		{"full page leaves the rest", []operationTimelineSource{timelineTestSource("e", false, at(0), at(3)), timelineTestSource("a", false, at(1), at(2))}, 2, "e1 a1", true},
		{"unread rows beyond the fetch continue", []operationTimelineSource{timelineTestSource("e", true, at(0)), timelineTestSource("a", false)}, 5, "e1", true},
		{"empty", []operationTimelineSource{timelineTestSource("e", false), timelineTestSource("a", false)}, 5, "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			entries, next := mergeOperationTimeline(tc.sources, OperationTimelineCursor{}, tc.limit)
			if got := timelineTestKinds(entries); got != tc.want || (next != nil) != tc.next {
				t.Fatalf("merge = %q next=%v, want %q next=%v", got, next != nil, tc.want, tc.next)
			}
		})
	}
}

func TestMergeOperationTimelineAdvancesOnlyConsumedSources(t *testing.T) {
	t0 := time.Date(2026, 10, 14, 0, 0, 0, 0, time.UTC)
	events := timelineTestSource("e", false, t0, t0.Add(3*time.Second))
	attempts := timelineTestSource("a", false, t0.Add(time.Second))
	entries, next := mergeOperationTimeline([]operationTimelineSource{events, attempts}, OperationTimelineCursor{Event: 7}, 2)
	if timelineTestKinds(entries) != "e1 a1" || next == nil {
		t.Fatalf("entries=%q next=%v", timelineTestKinds(entries), next)
	}
	// e1 then a1 advanced the shared fake position in consumption order; e2 did not.
	if next.Event != 7*100+1*10+1 || next.Step.Step != "a1" {
		t.Fatalf("cursor = %+v", next)
	}
}

func TestOperationTimelineEvents(t *testing.T) {
	t0 := time.Date(2026, 10, 14, 0, 0, 0, 0, time.UTC)
	page := api.OperationEventsResponse{LatestSequence: 4, Events: []api.OperationEvent{{Sequence: 3, Type: "running", CreatedAt: t0}, {Sequence: 4, Type: "succeeded", CreatedAt: t0}}}
	source, expired, err := operationTimelineEvents(page, 2, 2)
	if err != nil || expired || !source.more || len(source.items) != 2 {
		t.Fatalf("full page: %+v %v %v", source, expired, err)
	}
	var c OperationTimelineCursor
	source.items[1].advance(&c)
	if c.Event != 4 || source.items[0].entry.Event.Type != "running" {
		t.Fatalf("event entry or position: %+v %+v", c, source.items[0].entry)
	}
	if _, expired, err := operationTimelineEvents(api.OperationEventsResponse{LatestSequence: 4, ResyncRequired: true}, 2, 2); err != nil || !expired {
		t.Fatalf("pruned events: %v %v", expired, err)
	}
	if _, _, err := operationTimelineEvents(api.OperationEventsResponse{LatestSequence: 4, ResyncRequired: true}, 9, 2); err == nil {
		t.Fatal("a position beyond the latest event was accepted")
	}
}

func TestOperationAttemptLogs(t *testing.T) {
	day := func(d, h int) time.Time { return time.Date(2026, 10, d, h, 0, 0, 0, time.UTC) }
	ptr := func(t time.Time) *time.Time { return &t }
	for _, tc := range []struct {
		name          string
		instance      string
		since         time.Time
		until         *time.Time
		now           time.Time
		dates, until2 string
	}{
		{"one day", "i", day(14, 1), ptr(day(14, 2)), day(20, 0), "2026-10-14", "set"},
		{"crosses midnight", "i", day(13, 23), ptr(day(14, 1)), day(20, 0), "2026-10-13 2026-10-14", "set"},
		{"running uses now", "i", day(13, 23), nil, day(14, 0), "2026-10-13 2026-10-14", ""},
		{"clock skew collapses to the start", "i", day(14, 5), ptr(day(14, 4)), day(20, 0), "2026-10-14", "set"},
		{"long attempt keeps the newest days", "i", day(1, 0), ptr(day(14, 0)), day(20, 0), "2026-10-08 2026-10-09 2026-10-10 2026-10-11 2026-10-12 2026-10-13 2026-10-14", "set"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			logs := operationAttemptLogs(tc.instance, tc.since, tc.until, tc.now)
			if logs == nil || strings.Join(logs.ArchiveDates, " ") != tc.dates || (logs.Until != nil) != (tc.until2 != "") || logs.Correlation != api.OperationAttemptLogCorrelation || len(logs.ArchiveDates) > api.OperationTimelineLogDatesMax {
				t.Fatalf("logs = %+v, want dates %q", logs, tc.dates)
			}
			if tc.until != nil && logs.Until == tc.until {
				t.Fatal("log window aliases the attempt's finish time")
			}
		})
	}
	if operationAttemptLogs("", day(14, 1), nil, day(14, 2)) != nil {
		t.Fatal("an attempt without a placement got a log window")
	}
}

func TestOperationWorkflowStepOutcome(t *testing.T) {
	for status, want := range map[string]string{"running": "running", "retrying": "retry", "succeeded": "succeeded", "failed": "failed", "awaiting_event": "unknown"} {
		if got := operationWorkflowStepOutcome(status); got != want {
			t.Fatalf("%s -> %s, want %s", status, got, want)
		}
	}
}

func TestValidOperationTimelineRequest(t *testing.T) {
	for _, tc := range []struct {
		cursor OperationTimelineCursor
		limit  int
		ok     bool
	}{
		{OperationTimelineCursor{}, 1, true},
		{OperationTimelineCursor{Event: 9, Step: OperationTimelineStepKey{Step: "charge", Attempt: 2}}, api.OperationHistoryPageMax, true},
		{OperationTimelineCursor{}, 0, false},
		{OperationTimelineCursor{}, api.OperationHistoryPageMax + 1, false},
		{OperationTimelineCursor{Event: -1}, 1, false},
		{OperationTimelineCursor{Attempt: OperationTimelineAttemptKey{Generation: 1 << 31}}, 1, false},
		{OperationTimelineCursor{Delivery: OperationTimelineAttemptKey{Attempt: -1}}, 1, false},
		{OperationTimelineCursor{Step: OperationTimelineStepKey{Step: strings.Repeat("s", api.OperationWorkflowNameMaxBytes+1)}}, 1, false},
	} {
		if got := validOperationTimelineRequest(tc.cursor, tc.limit); got != tc.ok {
			t.Fatalf("%+v limit %d: %v", tc.cursor, tc.limit, got)
		}
	}
}
