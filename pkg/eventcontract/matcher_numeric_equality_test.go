package eventcontract

import (
	"encoding/json"
	"testing"
	"time"
)

func TestFilterEqualityComparesNestedNumbers(t *testing.T) {
	event := Envelope{SpecVersion: CloudEventsSpecVersion, ID: "event-1", Source: "billing", Type: "paid", Time: time.Now(), DataContentType: JSONDataContentType, AccountID: "00000000-0000-4000-8000-000000000001", Data: json.RawMessage(`{"object":{"amount":1.0,"values":[2e0,{"price":3.00}]},"items":[1.0]}`)}
	for _, tc := range []struct {
		filter string
		want   bool
	}{
		{`{"data":{"object":{"$eq":{"amount":1,"values":[2,{"price":3}]}}}}`, true},
		{`{"data":{"items":{"$eq":[1]}}}`, true},
		{`{"data":{"items":{"$eq":[2]}}}`, false},
		{`{"data":{"object":{"$eq":{"amount":1}}}}`, false},
		{`{"data":{"items":{"$eq":{"0":1}}}}`, false},
	} {
		subscription := Subscription{AccountID: event.AccountID, Source: "billing", Type: "paid", Filter: json.RawMessage(tc.filter)}
		got, err := subscription.Match(event)
		if err != nil || got != tc.want {
			t.Errorf("filter %s: got %v, %v; want %v", tc.filter, got, err, tc.want)
		}
	}
}
