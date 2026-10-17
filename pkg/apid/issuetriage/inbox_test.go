package issuetriage_test

import (
	"net/url"
	"testing"

	"github.com/onebox-faas/faas/pkg/apid/issuetriage"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestInboxFilterAndSafeLinks(t *testing.T) {
	for _, status := range []string{"", "all", "unacknowledged", "snoozed", "resurfaced", "escalated", "delivery_failed"} {
		f, err := issuetriage.ParseInboxFilter(url.Values{"status": {status}, "team": {"mine"}})
		if err != nil || !f.MyTeams || f.Status == "" {
			t.Fatal(f, err)
		}
	}
	for _, v := range []url.Values{{"status": {"unknown"}}, {"team": {"foreign"}}, {"after": {"bad"}}, {"after": {"00000000-0000-0000-0000-000000000000"}}} {
		if _, err := issuetriage.ParseInboxFilter(v); err == nil {
			t.Fatal(v)
		}
	}
	link := issuetriage.InboxURL("slug", state.IssueApplicationResponseFilter{Status: "snoozed", MyTeams: true, AfterID: "123"})
	if link != "/dashboard/apps/slug/issues/responses?after=123&status=snoozed&team=mine" {
		t.Fatal(link)
	}
}
