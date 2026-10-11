package api

// adr: 624

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestPreviewEventReplayClientEncodesRangeTargetAndContinuation(t *testing.T) {
	from := time.Date(2026, 10, 1, 0, 0, 0, 123456000, time.UTC)
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || !strings.Contains(r.URL.EscapedPath(), "app%2F%3F+/event-subscriptions/sub%2F%3F+/replay-preview") {
			t.Errorf("request=%s %s", r.Method, r.URL)
		}
		for k, v := range map[string]string{"from": from.Format(time.RFC3339Nano), "until": from.Add(time.Hour).Format(time.RFC3339Nano), "after": "erp1.a+/b?", "limit": "17"} {
			if got := r.URL.Query().Get(k); got != v {
				t.Errorf("%s=%q want %q", k, got, v)
			}
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"scanned_count":17,"matched_count":1,"matches":[{"event_id":"old","original_recipient":"not_captured"}],"retention":{"history_complete":false},"next_after":"next"}`))
	}))
	defer s.Close()
	r, err := NewClient(s.URL, "").PreviewEventReplay(context.Background(), "app/?+", "sub/?+", EventReplayPreviewOptions{From: from, Until: from.Add(time.Hour), After: "erp1.a+/b?", Limit: 17})
	if err != nil || r.ScannedCount != 17 || r.MatchedCount != 1 || len(r.Matches) != 1 || r.Matches[0].OriginalRecipient != "not_captured" || r.NextAfter != "next" || r.Retention.HistoryComplete {
		t.Fatalf("result=%+v %v", r, err)
	}
}
