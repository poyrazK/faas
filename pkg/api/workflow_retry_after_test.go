package api

import (
	"net/http"
	"testing"
	"time"
)

func TestWorkflowRetryAfter(t *testing.T) {
	now := time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		raw  string
		want time.Time
	}{
		{" 7 ", now.Add(7 * time.Second)},
		{now.Add(90 * time.Second).Format(http.TimeFormat), now.Add(90 * time.Second)},
		{"18446744073709551615", now.Add(WorkflowRetryAfterMaxDelay)},
		{now.Add(24 * time.Hour).Format(http.TimeFormat), now.Add(WorkflowRetryAfterMaxDelay)},
		{"-1", time.Time{}}, {"bogus", time.Time{}}, {now.Add(-time.Second).Format(http.TimeFormat), time.Time{}},
	} {
		t.Run(tc.raw, func(t *testing.T) {
			if got := WorkflowRetryAfter(tc.raw, now); !got.Equal(tc.want) {
				t.Fatalf("deadline=%v want %v", got, tc.want)
			}
		})
	}
}
