package receivers

import "testing"

// The jsonb column is nullable and rejects "". See
// TestPGAppErrorsReceiverStoresRepeatOccurrenceWithoutHeaders for the
// Postgres round trip.
func TestHeadersSampleJSONB(t *testing.T) {
	if got := headersSampleJSONB(""); got != nil {
		t.Fatalf("empty sample = %q, want nil (NULL)", got)
	}
	if got := string(headersSampleJSONB(`{"a":"b"}`)); got != `{"a":"b"}` {
		t.Fatalf("sample = %q", got)
	}
}
