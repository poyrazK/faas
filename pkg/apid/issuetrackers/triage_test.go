package issuetrackers

import (
	"net/url"
	"testing"
	"time"
)

func TestParseTriageSnoozeUTCAndRFC3339(t *testing.T) {
	for _, tc := range []struct {
		input, want string
		fail        bool
	}{
		{"2026-10-16T09:30", "2026-10-16T09:30:00Z", false},
		{"2026-10-16T09:30:00+02:00", "2026-10-16T07:30:00Z", false},
		{"not a date", "", true},
	} {
		t.Run(tc.input, func(t *testing.T) {
			req, err := ParseTriageAction(url.Values{"action": {"snooze"}, "snoozed_until": {tc.input}, "reason": {"waiting"}})
			if tc.fail {
				if err == nil {
					t.Fatal("invalid date accepted")
				}
				return
			}
			if err != nil || req.SnoozedUntil == nil || req.SnoozedUntil.UTC().Format(time.RFC3339) != tc.want || req.Reason != "waiting" {
				t.Fatal(req, err)
			}
		})
	}
}
