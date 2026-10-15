// adr: 1195
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

const timelineTestOperation = "11111111-1111-1111-1111-111111111111"
const timelineTestInstance = "44444444-4444-4444-4444-444444444444"

type timelineTestClient struct {
	pages   map[string]api.OperationTimelineResponse
	cursors []string
	logs    map[string]string
	missing map[string]bool
}

func (f *timelineTestClient) GetOperationTimeline(_ context.Context, app, id string, limit int, cursor string) (api.OperationTimelineResponse, error) {
	if app != "exports" || id != timelineTestOperation || limit != 2 {
		return api.OperationTimelineResponse{}, fmt.Errorf("unexpected request %s %s %d", app, id, limit)
	}
	f.cursors = append(f.cursors, cursor)
	return f.pages[cursor], nil
}

func (f *timelineTestClient) StreamAppArchivedLogs(_ context.Context, app string, s api.ArchiveLogSelector) (io.ReadCloser, error) {
	if app != "exports" || s.InstanceID != timelineTestInstance {
		return nil, fmt.Errorf("unexpected archive %s %+v", app, s)
	}
	if f.missing[s.Date] {
		return io.NopCloser(strings.NewReader("event: end\ndata: {\"reason\":\"archive_missing\"}\n\n")), nil
	}
	return io.NopCloser(strings.NewReader(f.logs[s.Date] + "event: end\ndata: {\"reason\":\"archive_complete\"}\n\n")), nil
}

func timelineTestLogLine(at time.Time, line string) string {
	raw, _ := json.Marshal(api.LogEvent{Seq: 1, InstanceID: timelineTestInstance, Stream: "stdout", Line: line, WrittenAt: at.Format(time.RFC3339Nano)})
	return "data: " + string(raw) + "\n\n"
}

func timelineTestPages(t0 time.Time) map[string]api.OperationTimelineResponse {
	finished, status := t0.Add(1500*time.Millisecond), 502
	logs := &api.OperationAttemptLogs{InstanceID: timelineTestInstance, Since: t0.Add(time.Second), Until: &finished, ArchiveDates: []string{"2026-10-13", "2026-10-14"}, Correlation: api.OperationAttemptLogCorrelation}
	return map[string]api.OperationTimelineResponse{
		"": {OperationID: timelineTestOperation, State: api.OperationSucceeded, Generation: 1, NextCursor: "page-2", Entries: []api.OperationTimelineEntry{
			{At: t0, Kind: api.OperationTimelineKindEvent, Event: &api.OperationEvent{Sequence: 1, Type: "accepted", Data: json.RawMessage(`{}`)}},
			{At: t0.Add(time.Second), Kind: api.OperationTimelineKindAttempt, Attempt: &api.OperationTimelineAttempt{Generation: 1, ExecutionID: timelineTestOperation, Attempt: 1, Outcome: api.OperationAttemptRetry, HTTPStatus: &status, Error: "upstream\nreset", StartedAt: t0.Add(time.Second), FinishedAt: &finished, Logs: logs}},
		}},
		"page-2": {OperationID: timelineTestOperation, State: api.OperationSucceeded, Generation: 1, EventsExpired: true, Entries: []api.OperationTimelineEntry{
			{At: t0.Add(3 * time.Second), Kind: api.OperationTimelineKindDelivery, Delivery: &api.OperationDeliveryAttempt{ReplayGeneration: 0, AttemptNumber: 1, Outcome: "succeeded", ResponseCode: 200, StartedAt: t0.Add(3 * time.Second), FinishedAt: t0.Add(3 * time.Second)}},
		}},
	}
}

func TestCustomerOperationTimelineFollowsCursorsAndFiltersLogWindows(t *testing.T) {
	t0 := time.Date(2026, 10, 14, 0, 0, 0, 0, time.UTC)
	client := &timelineTestClient{pages: timelineTestPages(t0),
		logs: map[string]string{"2026-10-14": timelineTestLogLine(t0.Add(500*time.Millisecond), "before window") +
			timelineTestLogLine(t0.Add(1200*time.Millisecond), "charging card") + timelineTestLogLine(t0.Add(2*time.Second), "after window")},
		missing: map[string]bool{"2026-10-13": true}}
	var out bytes.Buffer
	c := customerOperationTimelineCommand{app: "exports", id: timelineTestOperation, limit: 2, all: true, logs: true}
	if err := runCustomerOperationTimeline(context.Background(), client, c, &out, false); err != nil {
		t.Fatal(err)
	}
	text := out.String()
	for _, want := range []string{"event     #1 accepted", "attempt   g1 #1 retry HTTP 502 500ms error: upstream reset", "charging card", "No archived logs were found", "Event history expired", "delivery  0/1 succeeded HTTP 200"} {
		if !strings.Contains(text, want) {
			t.Fatalf("timeline output lacks %q:\n%s", want, text)
		}
	}
	for _, unwanted := range []string{"before window", "after window", "Next cursor"} {
		if strings.Contains(text, unwanted) {
			t.Fatalf("timeline output has %q:\n%s", unwanted, text)
		}
	}
	if strings.Join(client.cursors, ",") != ",page-2" {
		t.Fatalf("cursors = %q", client.cursors)
	}
}

func TestCustomerOperationTimelineSinglePageAndJSON(t *testing.T) {
	t0 := time.Date(2026, 10, 14, 0, 0, 0, 0, time.UTC)
	for _, asJSON := range []bool{false, true} {
		client := &timelineTestClient{pages: timelineTestPages(t0)}
		var out bytes.Buffer
		c := customerOperationTimelineCommand{app: "exports", id: timelineTestOperation, limit: 2}
		if err := runCustomerOperationTimeline(context.Background(), client, c, &out, asJSON); err != nil {
			t.Fatal(err)
		}
		if len(client.cursors) != 1 {
			t.Fatalf("read %d pages without --all", len(client.cursors))
		}
		if asJSON {
			var page api.OperationTimelineResponse
			if err := json.Unmarshal(out.Bytes(), &page); err != nil || page.NextCursor != "page-2" || len(page.Entries) != 2 {
				t.Fatalf("json page = %+v %v", page, err)
			}
		} else if !strings.Contains(out.String(), "Next cursor: page-2") || strings.Contains(out.String(), "(logs") {
			t.Fatalf("human page:\n%s", out.String())
		}
	}
}

func TestCustomerOperationTimelineRejectsMismatchedPages(t *testing.T) {
	t0 := time.Date(2026, 10, 14, 0, 0, 0, 0, time.UTC)
	for name, mutate := range map[string]func(map[string]api.OperationTimelineResponse){
		"operation": func(p map[string]api.OperationTimelineResponse) {
			r := p[""]
			r.OperationID = timelineTestInstance
			p[""] = r
		},
		"oversized page": func(p map[string]api.OperationTimelineResponse) {
			r := p[""]
			r.Entries = append(r.Entries, r.Entries[0])
			p[""] = r
		},
		"repeat cursor": func(p map[string]api.OperationTimelineResponse) {
			r := p["page-2"]
			r.NextCursor = "page-2"
			p["page-2"] = r
		},
	} {
		t.Run(name, func(t *testing.T) {
			pages := timelineTestPages(t0)
			mutate(pages)
			c := customerOperationTimelineCommand{app: "exports", id: timelineTestOperation, limit: 2, all: true}
			if err := runCustomerOperationTimeline(context.Background(), &timelineTestClient{pages: pages}, c, io.Discard, true); err == nil || !strings.Contains(err.Error(), "mismatched timeline page") {
				t.Fatalf("err = %v", err)
			}
		})
	}
}

func TestParseCustomerOperationTimeline(t *testing.T) {
	for _, tc := range []struct {
		args   []string
		asJSON bool
		ok     bool
	}{
		{[]string{timelineTestOperation, "--app", "exports", "--all", "--logs"}, false, true},
		{[]string{timelineTestOperation, "--app", "exports", "--limit", "100", "--cursor", "c"}, true, true},
		{[]string{timelineTestOperation}, false, false},
		{[]string{"not-a-uuid", "--app", "exports"}, false, false},
		{[]string{timelineTestOperation, "--app", "exports", "--limit", "101"}, false, false},
		{[]string{timelineTestOperation, "--app", "exports", "--cursor", strings.Repeat("c", api.OperationHistoryCursorMaxBytes+1)}, false, false},
		{[]string{timelineTestOperation, "--app", "exports", "--logs"}, true, false},
	} {
		if _, err := parseCustomerOperationTimeline(tc.args, tc.asJSON); (err == nil) != tc.ok {
			t.Fatalf("%v json=%v: err = %v", tc.args, tc.asJSON, err)
		}
	}
}
