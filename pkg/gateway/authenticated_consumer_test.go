// adr: 104
package gateway

import "testing"

func TestResolveConsumerKey_StableConsumerID(t *testing.T) {
	t.Parallel()

	consumerID, ok := resolveConsumerKey("consumer_id", "", Authenticated{
		ConsumerID:    "consumer-42",
		ConsumerKeyID: "key-rotated",
	})
	if !ok {
		t.Fatal("resolveConsumerKey(consumer_id) returned ok=false")
	}
	if consumerID != "consumer-42" {
		t.Fatalf("resolveConsumerKey(consumer_id) = %q, want consumer-42", consumerID)
	}
}

func TestResolveConsumerKey_ConsumerIDAnonymous(t *testing.T) {
	t.Parallel()

	if consumerID, ok := resolveConsumerKey("consumer_id", "", Authenticated{}); ok || consumerID != "" {
		t.Fatalf("resolveConsumerKey(consumer_id) anonymous = (%q, %v), want (\"\", false)", consumerID, ok)
	}
}
