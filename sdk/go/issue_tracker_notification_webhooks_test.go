package faas

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestTrackerSyncWebhookSubscriptionEvents(t *testing.T) {
	events := []string{"issue.tracker_sync.attention", "issue.tracker_sync.recovered"}
	raw, err := json.Marshal(CreateAppWebhookRequest{TargetURL: "https://example.com/sync", WebhookSecret: "secret", EventFilter: events})
	if err != nil {
		t.Fatal(err)
	}
	var got CreateAppWebhookRequest
	if err = json.Unmarshal(raw, &got); err != nil || !reflect.DeepEqual(got.EventFilter, events) {
		t.Fatal(got, err)
	}
}
