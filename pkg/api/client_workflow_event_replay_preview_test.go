package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestPreviewWorkflowEventReplayEncodesTargetRangeAndCursor(t *testing.T) {
	from := time.Date(2026, 10, 1, 0, 0, 0, 123456000, time.UTC)
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.EscapedPath() != "/v1/apps/app%2F%3F+/workflow-event-replay-preview" {
			t.Errorf("request=%s %s", r.Method, r.URL)
		}
		for key, want := range map[string]string{
			"workflow_name": "paid/?+", "from": from.Format(time.RFC3339Nano), "until": from.Add(time.Hour).Format(time.RFC3339Nano),
			"after": "werp1.a+/b?", "limit": "17",
		} {
			if got := r.URL.Query().Get(key); got != want {
				t.Errorf("%s=%q want %q", key, got, want)
			}
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"workflow_name":"paid","scanned_count":17,"matched_count":1,"already_admitted_count":1,"matches":[{"event_id":"old","original_recipient":"captured","admission_recorded":true}],"retention":{"history_complete":false},"next_after":"next"}`))
	}))
	defer s.Close()
	r, err := NewClient(s.URL, "").PreviewWorkflowEventReplay(context.Background(), "app/?+", "paid/?+", WorkflowEventReplayPreviewOptions{
		From: from, Until: from.Add(time.Hour), After: "werp1.a+/b?", Limit: 17,
	})
	if err != nil || r.WorkflowName != "paid" || r.ScannedCount != 17 || r.MatchedCount != 1 || len(r.Matches) != 1 ||
		!r.Matches[0].AdmissionRecorded || r.NextAfter != "next" || r.Retention.HistoryComplete {
		t.Fatalf("result=%+v %v", r, err)
	}
}
