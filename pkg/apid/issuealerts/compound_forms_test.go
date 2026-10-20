// adr: 1662
package issuealerts

import (
	"net/url"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestCompoundFormPreservesOtherRulesAndRevision(t *testing.T) {
	current := api.IssueCompoundAlertPolicy{Revision: 7, Rules: []api.IssueCompoundAlertRule{{Key: "other", MinimumEvents: 1, MinimumCustomers: 1, WindowSeconds: 60, CooldownSeconds: 60}}}
	form := url.Values{"action": {"save"}, "expected_revision": {"7"}, "key": {"payments"}, "enabled": {"on"}, "environment": {"production"}, "minimum_events": {"20"}, "minimum_customers": {"3"}, "window_seconds": {"600"}, "cooldown_seconds": {"1800"}}
	in, err := CompoundFormUpdate(current, form)
	if err != nil || len(in.Rules) != 2 || *in.ExpectedRevision != 7 || in.Rules[1].MinimumCustomers != 3 || len(current.Rules) != 1 {
		t.Fatal(in, err)
	}
	form.Set("action", "remove")
	form.Set("key", "other")
	in, err = CompoundFormUpdate(current, form)
	if err != nil || len(in.Rules) != 0 {
		t.Fatal(in, err)
	}
	form.Set("key", "missing")
	if _, err := CompoundFormUpdate(current, form); err == nil {
		t.Fatal("removed unknown rule")
	}
	form.Set("action", "save")
	form.Set("minimum_customers", "bad")
	if _, err := CompoundFormUpdate(current, form); err == nil {
		t.Fatal("invalid customer threshold")
	}
}
