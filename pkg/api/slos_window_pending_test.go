package api

import "testing"

// A new SLO showed "Attained: no traffic (0 of 0 requests)" beside a live
// 9.30× burn rate, because no hourly rollup had closed yet (Hunt 12 H12-22).
func TestSLOWindowPending(t *testing.T) {
	tests := []struct {
		name   string
		status SLOStatus
		want   string
		ok     bool
	}{
		{"creation hour still open", SLOStatus{WindowStart: "2026-10-22T12:00:00Z"}, "pending: first hour closes 13:00 UTC", true},
		{"first hour closed with no requests", SLOStatus{WindowStart: "2026-10-22T12:00:00Z", HoursExpected: 1, HoursRecorded: 1}, "", false},
		// Production-us rc.294: "no traffic (0 of 0 requests)" for an hour
		// that served 192 requests, until the rollup recorded it (Hunt 14 H14-16).
		{"first hour closed, rollup lagging", SLOStatus{WindowStart: "2026-10-22T12:00:00Z", HoursExpected: 1}, "pending: first hour closed 13:00 UTC, recording it", true},
		{"unparseable start", SLOStatus{WindowStart: ""}, "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := SLOWindowPending(tt.status)
			if got != tt.want || ok != tt.ok {
				t.Fatalf("SLOWindowPending = %q, %v; want %q, %v", got, ok, tt.want, tt.ok)
			}
		})
	}
}
