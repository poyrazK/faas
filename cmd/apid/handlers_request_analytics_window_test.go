package main

import (
	"net/http/httptest"
	"testing"
	"time"
)

func TestParseRequestAnalyticsWindow(t *testing.T) {
	now := time.Date(2026, 9, 5, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name      string
		query     string
		retention time.Duration
		wantFrom  time.Time
		wantUntil time.Time
		wantSince string
		wantClamp bool
		wantErr   bool
	}{
		{
			name:      "default duration",
			retention: 7 * 24 * time.Hour,
			wantFrom:  now.Add(-24 * time.Hour),
			wantUntil: now,
			wantSince: "24h",
		},
		{
			name:      "duration is retention clamped",
			query:     "?since=7d",
			retention: 3 * 24 * time.Hour,
			wantFrom:  now.Add(-3 * 24 * time.Hour),
			wantUntil: now,
			wantSince: "3d",
			wantClamp: true,
		},
		{
			name:      "absolute bounded range",
			query:     "?since=2026-09-01T00:00:00Z&until=2026-09-03T00:00:00Z",
			retention: 7 * 24 * time.Hour,
			wantFrom:  time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC),
			wantUntil: time.Date(2026, 9, 3, 0, 0, 0, 0, time.UTC),
			wantSince: "2d",
		},
		{
			name:      "invalid since",
			query:     "?since=not-a-window",
			retention: 7 * 24 * time.Hour,
			wantErr:   true,
		},
		{
			name:      "future until",
			query:     "?until=2026-09-06T00:00:00Z",
			retention: 7 * 24 * time.Hour,
			wantErr:   true,
		},
		{
			name:      "since after until",
			query:     "?since=2026-09-04T00:00:00Z&until=2026-09-03T00:00:00Z",
			retention: 7 * 24 * time.Hour,
			wantErr:   true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := httptest.NewRequest("GET", "/v1/apps/demo/analytics"+tt.query, nil)
			got, err := parseRequestAnalyticsWindow(r, now, tt.retention)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error")
				}
				return
			}
			if err != nil {
				t.Fatalf("parse window: %v", err)
			}
			if !got.From.Equal(tt.wantFrom) || !got.Until.Equal(tt.wantUntil) {
				t.Fatalf("window = [%s, %s), want [%s, %s)", got.From, got.Until, tt.wantFrom, tt.wantUntil)
			}
			if got.Since != tt.wantUntil.Sub(tt.wantFrom) {
				t.Fatalf("since duration = %s, want %s", got.Since, tt.wantUntil.Sub(tt.wantFrom))
			}
			if gotSince := echoDebugSince(got.RequestedSince, got.Since); gotSince != tt.wantSince {
				t.Fatalf("since echo = %q, want %q", gotSince, tt.wantSince)
			}
			if got.WindowClamped != tt.wantClamp {
				t.Fatalf("window_clamped = %t, want %t", got.WindowClamped, tt.wantClamp)
			}
		})
	}
}
