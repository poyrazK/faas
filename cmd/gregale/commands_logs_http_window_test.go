package main_test

import (
	"bytes"
	"strings"
	"testing"

	main "github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
)

// TestHTTPLogRowMarksMinuteWindow pins Hunt 32 H32-64: stored request rows
// carry the minute they were counted in, which printed as an exact time.
func TestHTTPLogRowMarksMinuteWindow(t *testing.T) {
	for _, tc := range []struct {
		at   string
		want bool
	}{
		{"2026-11-10T07:43:00Z", true},
		{"2026-11-10T07:35:30.624Z", false},
		{"not-a-time", false},
	} {
		var out bytes.Buffer
		main.RenderHTTPLogQueryEvent(&out, api.LogQueryEvent{Timestamp: tc.at, Status: 202, Method: "POST", Route: "POST /log", ID: "r1"})
		if got := strings.Contains(out.String(), " window=1m"); got != tc.want {
			t.Errorf("%s: window marker = %t, want %t (%q)", tc.at, got, tc.want, out.String())
		}
	}
}

// Hunt 33 H33-6: latency is a pointer on the wire, and the row printed its
// address (latency=44358373218936ms) instead of the value.
func TestHTTPLogRowPrintsLatencyValue(t *testing.T) {
	latency := 42
	for _, tc := range []struct {
		event api.LogQueryEvent
		want  string
	}{
		{api.LogQueryEvent{Timestamp: "2026-11-11T10:00:01Z", Status: 200, Method: "GET", Route: "GET /", ID: "r1", LatencyMS: &latency}, " latency=42ms "},
		{api.LogQueryEvent{Timestamp: "2026-11-11T10:00:01Z", Status: 200, Method: "GET", Route: "GET /", ID: "r2"}, " latency=0ms "},
	} {
		var out bytes.Buffer
		main.RenderHTTPLogQueryEvent(&out, tc.event)
		if !strings.Contains(out.String(), tc.want) {
			t.Errorf("row %q does not contain %q", out.String(), tc.want)
		}
	}
}
