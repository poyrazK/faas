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

// Found on prod 2026-11-11: the HTTP row printed the latency pointer's
// address ("latency=88979542362600ms") instead of its value.
func TestHTTPLogRowPrintsLatencyValue(t *testing.T) {
	latency := 1194
	var out bytes.Buffer
	main.RenderHTTPLogQueryEvent(&out, api.LogQueryEvent{Timestamp: "2026-11-11T11:50:09.061Z", Status: 503, Method: "GET", Route: "GET /", ID: "r1", LatencyMS: &latency})
	if !strings.Contains(out.String(), " latency=1194ms ") {
		t.Fatalf("row = %q, want latency=1194ms", out.String())
	}
	out.Reset()
	main.RenderHTTPLogQueryEvent(&out, api.LogQueryEvent{Timestamp: "2026-11-11T11:50:09.061Z", Status: 503, Method: "GET", Route: "GET /", ID: "r1"})
	if !strings.Contains(out.String(), " latency=- ") {
		t.Fatalf("row without latency = %q, want latency=-", out.String())
	}
}
