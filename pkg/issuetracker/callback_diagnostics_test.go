package issuetracker

import (
	"github.com/onebox-faas/faas/pkg/api"
	"testing"
	"time"
)

func TestCallbackSetupRequiresRecentEvidenceAndCurrentConfiguration(t *testing.T) {
	now := time.Now()
	recent, old, future, boundary := now.Add(-time.Minute), now.Add(-api.IssueTrackerCallbackVerificationMaxAge-time.Nanosecond), now.Add(time.Minute), now.Add(-api.IssueTrackerCallbackVerificationMaxAge)
	for _, test := range []struct {
		name       string
		configured bool
		at         *time.Time
		want       string
	}{
		{"disabled", false, &recent, "Not configured"},
		{"unverified", true, nil, "Configured; awaiting verified delivery"},
		{"recent", true, &recent, "Verified delivery received within 24 hours"},
		{"boundary", true, &boundary, "Verified delivery received within 24 hours"},
		{"expired", true, &old, "Configured; delivery evidence is older than 24 hours"},
		{"future", true, &future, "Configured; delivery evidence is ahead of Gregale’s clock"},
	} {
		t.Run(test.name, func(t *testing.T) {
			h := CallbackHealth{Configured: test.configured, LastVerifiedAt: test.at}
			if got := h.SetupStatus(now); got != test.want {
				t.Fatal(got, test.want)
			}
		})
	}
}
