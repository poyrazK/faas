package issueslack

import (
	"encoding/json"
	"net/url"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/state"
)

// ADR-1493: native reminders share safe labels and authenticated issue links.
func TestApplicationSlackMessagesUseSafeLabelsAndIssueLinks(t *testing.T) {
	for _, kind := range []string{"created", "regressed", "impact_threshold_reached", "frequency_threshold_reached", "compound_threshold_reached", "application_reminder", "application_escalation"} {
		t.Run(kind, func(t *testing.T) {
			c := state.IssueSlackClaim{Application: true, Kind: kind, ID: "delivery", AppSlug: "exports", IssueID: "issue-id", ChannelID: "C0123456789", ThreadTS: "123.456", Details: map[string]string{"environment": "production", "rule_key": "burst", "minimum_events": "50", "observed_events": "51", "minimum_customers": "2", "identified_customers": "3", "window_seconds": "300", "reminder_sequence": "2", "stack": "private-secret", "request_body": "private-secret", "evil": "<!here>"}}
			raw, err := message(c, "https://gregale.dev")
			if err != nil {
				t.Fatal(err)
			}
			text := string(raw)
			for _, want := range []string{"Gregale Issues", "production", "burst", "51", "123.456", "https://gregale.dev/dashboard/apps/exports/issues?issue=issue-id", "https://gregale.dev/dashboard/apps/exports/issues/issue-id/triage"} {
				if !strings.Contains(text, want) {
					t.Fatalf("missing %q: %s", want, text)
				}
			}
			for _, bad := range []string{"private-secret", "<!here>", "Open triage", "Sync health", "new provider check", "actions"} {
				if strings.Contains(text, bad) {
					t.Fatalf("unexpected message content %q", bad)
				}
			}
			var payload map[string]any
			if err := json.Unmarshal(raw, &payload); err != nil {
				t.Fatal(err)
			}
			if payload["client_msg_id"] != "delivery" || payload["parse"] != "none" || payload["unfurl_links"] != false {
				t.Fatalf("unsafe delivery shape: %+v", payload)
			}
			c.Details = map[string]string{"environment": "<!here>", "rule_key": "https://secret.invalid", "observed_events": "xoxb-secret", "window_seconds": "99999999999999999999999"}
			raw, err = message(c, "https://gregale.dev")
			if err != nil {
				t.Fatal(err)
			}
			for _, bad := range []string{"here", "secret.invalid", "xoxb-secret", "999999999999"} {
				if strings.Contains(string(raw), bad) {
					t.Fatalf("untrusted metadata leaked: %s", raw)
				}
			}
		})
	}
	if _, err := message(state.IssueSlackClaim{Application: true, Kind: "resolved"}, "https://gregale.dev"); err == nil {
		t.Fatal("unsupported lifecycle kind accepted")
	}
	if _, err := message(state.IssueSlackClaim{Application: true, Kind: "created", ThreadTS: "bad"}, "https://gregale.dev"); err == nil {
		t.Fatal("invalid application thread timestamp accepted")
	}
}
func TestApplicationSlackPolicyFormRetainsExplicitOptInAndRevision(t *testing.T) {
	form := url.Values{"expected_revision": {"3"}, "enabled": {"1"}, "events": {"created", "frequency_threshold_reached"}, "fallback_team_id": {"team"}}
	out, err := ApplicationPolicyForm(form)
	if err != nil || !out.Enabled || out.Revision != 3 || len(out.Events) != 2 || out.FallbackTeamID != "team" {
		t.Fatalf("form=%+v %v", out, err)
	}
	for _, value := range []string{"", "bad", "-1"} {
		form.Set("expected_revision", value)
		if _, err := ApplicationPolicyForm(form); err == nil {
			t.Fatal("invalid revision accepted", value)
		}
	}
	for _, kind := range []string{"created", "regressed", "impact_threshold_reached", "frequency_threshold_reached", "compound_threshold_reached"} {
		if !state.ApplicationSlackEvent(kind) {
			t.Fatal(kind)
		}
	}
	for _, kind := range []string{"resolved", "reopened", "ignored", "issue.created", "reminder"} {
		if state.ApplicationSlackEvent(kind) {
			t.Fatal("unselected event kind accepted", kind)
		}
	}
}
