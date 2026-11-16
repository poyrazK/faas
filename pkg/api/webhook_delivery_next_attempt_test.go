package api_test

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// Hunt 34 H34-9: a dead delivery's next attempt rendered as the Unix epoch.
func TestWebhookDeliveryOmitsUnscheduledNextAttempt(t *testing.T) {
	now := time.Date(2026, 11, 12, 16, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		next time.Time
		want string
	}{
		{time.Unix(0, 0).UTC(), ""},
		{time.Time{}, ""},
		{now, "2026-11-12T16:00:00Z"},
	} {
		resp := api.AppWebhookDeliveryResponseFromRow(api.AppWebhookDeliveryRow{ID: "d", Status: "dead", NextAttemptAt: tc.next, CreatedAt: now, UpdatedAt: now})
		if resp.NextAttemptAt != tc.want {
			t.Fatalf("next attempt %v = %q, want %q", tc.next, resp.NextAttemptAt, tc.want)
		}
		raw, err := json.Marshal(resp)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(raw), "1970-01-01") || (tc.want == "") != !strings.Contains(string(raw), "next_attempt_at") {
			t.Fatalf("JSON = %s", raw)
		}
	}
}
