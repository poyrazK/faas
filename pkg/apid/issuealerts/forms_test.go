package issuealerts

import (
	"net/url"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestFrequencyFormsPreserveOtherRulesAndRevision(t *testing.T) {
	current := api.IssueFrequencyAlertPolicy{Revision: 4, Rules: []api.IssueFrequencyAlertRule{{Key: "existing", Enabled: true, MinimumEvents: 2, WindowSeconds: 60, CooldownSeconds: 60}}}
	form := url.Values{"action": {"save"}, "expected_revision": {"4"}, "key": {"new"}, "enabled": {"on"}, "environment": {"production"}, "minimum_events": {"50"}, "window_seconds": {"300"}, "cooldown_seconds": {"1800"}}
	out, err := FormUpdate(current, form)
	if err != nil || len(out.Rules) != 2 || *out.ExpectedRevision != 4 || out.Rules[0] != current.Rules[0] || len(current.Rules) != 1 {
		t.Fatalf("update=%+v err=%v", out, err)
	}
	form.Set("key", "existing")
	form.Del("enabled")
	out, err = FormUpdate(current, form)
	if err != nil || out.Rules[0].Enabled || out.Rules[0].MinimumEvents != 50 || current.Rules[0].MinimumEvents != 2 {
		t.Fatalf("edit=%+v err=%v", out, err)
	}
	form.Set("action", "remove")
	out, err = FormUpdate(current, form)
	if err != nil || len(out.Rules) != 0 || len(current.Rules) != 1 {
		t.Fatalf("remove=%+v err=%v", out, err)
	}
	// Never silently upgrade a stale form to the freshly-read revision.
	form.Set("expected_revision", "3")
	out, err = FormUpdate(current, form)
	if err != nil || *out.ExpectedRevision != 3 {
		t.Fatal("stale revision lost")
	}
	for _, field := range []string{"expected_revision", "minimum_events", "window_seconds", "cooldown_seconds"} {
		bad := url.Values{"action": {"save"}, "expected_revision": {"4"}, "key": {"x"}, "minimum_events": {"2"}, "window_seconds": {"60"}, "cooldown_seconds": {"60"}}
		bad.Set(field, "bad")
		if _, err := FormUpdate(current, bad); err == nil {
			t.Fatalf("accepted bad %s", field)
		}
	}
	form.Set("key", "missing")
	if _, err := FormUpdate(current, form); err == nil {
		t.Fatal("removed missing rule")
	}
}
