package issuetriage_test

import (
	"github.com/onebox-faas/faas/pkg/apid/issuetriage"
	"net/url"
	"testing"
)

func TestParseResponseAction(t *testing.T) {
	for _, tc := range []struct {
		name, rev, action, until string
		valid                    bool
	}{
		{"UTC", "1", "snooze", "2026-10-20T13:00", true},
		{"offset", "2", "snooze", "2026-10-20T16:00:00+03:00", true},
		{"ack", "0", "acknowledge", "", true},
		{"clear", "0", "unsnooze", "", true},
		{"negative revision", "-1", "acknowledge", "", false},
		{"missing revision", "", "acknowledge", "", false},
		{"unknown action", "0", "claim", "", false},
		{"bad time", "0", "snooze", "tomorrow", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, req, err := issuetriage.ParseAction(url.Values{"expected_revision": {tc.rev}, "action": {tc.action}, "snoozed_until": {tc.until}})
			if (err == nil) != tc.valid {
				t.Fatal(err)
			}
			if tc.valid && tc.until != "" && req.SnoozedUntil.UTC().Hour() != 13 {
				t.Fatal("time conversion", req)
			}
		})
	}
}
