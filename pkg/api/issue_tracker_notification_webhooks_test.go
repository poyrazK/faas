package api_test

import (
	"github.com/onebox-faas/faas/pkg/api"
	"slices"
	"testing"
)

func TestTrackerNotificationWebhookVocabulary(t *testing.T) {
	for _, event := range []string{"issue.tracker_sync.attention", "issue.tracker_sync.recovered"} {
		if !slices.Contains(api.AllowedAppWebhookEvents, event) {
			t.Fatal("missing event", event)
		}
	}
}
