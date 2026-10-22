package issues

import (
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestImpactAlertCrossesThreshold(t *testing.T) {
	now := time.Now().UTC()
	future, past := now.Add(time.Minute), now.Add(-time.Minute)
	for _, tc := range []struct {
		name, state            string
		until                  *time.Time
		minimum, before, after int64
		want                   bool
	}{
		{"open crossing", "open", nil, 2, 1, 2, true},
		{"below threshold", "open", nil, 2, 0, 1, false},
		{"already above", "open", nil, 2, 2, 3, false},
		{"disabled", "open", nil, 0, 0, 1, false},
		{"indefinite ignore", "ignored", nil, 2, 1, 2, false},
		{"active ignore", "ignored", &future, 2, 1, 2, false},
		{"expired before maintenance", "ignored", &past, 2, 1, 2, true},
		{"expiry boundary", "ignored", &now, 2, 1, 2, true},
		{"expiry does not replay", "ignored", &past, 2, 2, 3, false},
		{"reopened does not replay", "open", nil, 2, 2, 3, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := ImpactAlertCrossesThreshold(api.Issue{State: tc.state, IgnoredUntil: tc.until}, tc.minimum, tc.before, tc.after, now); got != tc.want {
				t.Fatalf("got %v want %v", got, tc.want)
			}
		})
	}
}
