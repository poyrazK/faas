//go:build linux

package main

import (
	"encoding/json"
	"strings"
	"testing"
)

func advisoryEvents(n int, pathLen int) []advisoryEvent {
	out := make([]advisoryEvent, n)
	for i := range out {
		out[i] = advisoryEvent{
			Path:   "/data/" + strings.Repeat("x", pathLen),
			Masks:  []string{"modify"},
			PID:    100 + i,
			TsUnix: int64(1_700_000_000_000 + i),
		}
	}
	return out
}

// TestMarshalAdvisoryBatchWithinLimit_AlwaysParseable is the regression pin.
//
// The previous implementation marshalled the whole batch and byte-sliced the
// result down to advisoryMaxBody. That produced a truncated JSON document, so
// the host receiver could not unmarshal it: the log said "dropping tail" while
// the entire batch was in fact discarded. Whatever this returns must parse.
func TestMarshalAdvisoryBatchWithinLimit_AlwaysParseable(t *testing.T) {
	cases := []struct {
		name    string
		events  []advisoryEvent
		limit   int
		wantAll bool
	}{
		{"empty batch", nil, advisoryMaxBody, true},
		{"comfortably under the limit", advisoryEvents(4, 10), advisoryMaxBody, true},
		{"full batch at the documented sizing", advisoryEvents(advisoryBatchCap, 20), advisoryMaxBody, true},
		{"over the limit by volume", advisoryEvents(advisoryBatchCap, 400), advisoryMaxBody, false},
		{"over the limit by a single huge event", advisoryEvents(1, 20000), advisoryMaxBody, false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			body, sent, err := marshalAdvisoryBatchWithinLimit("app-1", tc.events, tc.limit)
			if err != nil {
				t.Fatalf("marshalAdvisoryBatchWithinLimit: %v", err)
			}
			if len(body) > tc.limit {
				t.Fatalf("body is %d bytes, over the %d limit", len(body), tc.limit)
			}
			if sent > len(tc.events) {
				t.Fatalf("reported %d events sent, batch had %d", sent, len(tc.events))
			}

			if len(body) == 0 {
				// Only legal when the batch was empty or not even one event fits.
				if len(tc.events) > 0 && tc.name != "over the limit by a single huge event" {
					t.Fatalf("returned no body for a batch that should have fit a prefix")
				}
				return
			}

			// The contract: whatever comes back is a parseable document whose
			// event count matches the reported prefix length.
			var decoded advisoryBatch
			if err := json.Unmarshal(body, &decoded); err != nil {
				t.Fatalf("returned body does not parse: %v\nbody: %.120q", err, body)
			}
			if len(decoded.Events) != sent {
				t.Fatalf("decoded %d events, reported %d", len(decoded.Events), sent)
			}
			if decoded.AppID != "app-1" {
				t.Fatalf("decoded app_id = %q", decoded.AppID)
			}
			if tc.wantAll && sent != len(tc.events) {
				t.Fatalf("dropped events that should have fit: sent %d of %d", sent, len(tc.events))
			}
		})
	}
}

// TestMarshalAdvisoryBatchWithinLimit_KeepsLargestPrefix pins that the binary
// search finds the true boundary rather than an arbitrary smaller prefix: the
// returned count must fit and one more event must not.
func TestMarshalAdvisoryBatchWithinLimit_KeepsLargestPrefix(t *testing.T) {
	events := advisoryEvents(advisoryBatchCap, 200)
	body, sent, err := marshalAdvisoryBatchWithinLimit("app-1", events, advisoryMaxBody)
	if err != nil {
		t.Fatalf("marshalAdvisoryBatchWithinLimit: %v", err)
	}
	if sent == 0 || sent == len(events) {
		t.Skipf("fixture did not straddle the limit (sent %d of %d, %d bytes)", sent, len(events), len(body))
	}
	oneMore, err := json.Marshal(advisoryBatch{AppID: "app-1", Events: events[:sent+1]})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if len(oneMore) <= advisoryMaxBody {
		t.Fatalf("prefix of %d fit in %d bytes but was not selected; search is not finding the boundary",
			sent+1, len(oneMore))
	}
}
