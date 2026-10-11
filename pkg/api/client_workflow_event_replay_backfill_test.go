package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestCreateWorkflowEventReplayBackfillPostsPinnedTargetAndRange(t *testing.T) {
	from := time.Date(2026, 10, 1, 0, 0, 0, 123456000, time.UTC)
	var body WorkflowEventReplayBackfillRequest
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.EscapedPath() != "/v1/apps/app%2F%3F+/workflow-event-replays" {
			t.Errorf("request=%s %s", r.Method, r.URL)
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode request: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"job","app_slug":"worker","consumer_kind":"workflow","workflow_name":"paid","workflow_revision":"revision","state":"running"}`))
	}))
	defer s.Close()

	r, err := NewClient(s.URL, "").CreateWorkflowEventReplayBackfill(context.Background(), "app/?+", WorkflowEventReplayBackfillRequest{
		WorkflowName: "paid", From: from, Until: from.Add(time.Hour),
	})
	if err != nil || r.ID != "job" || r.ConsumerKind != "workflow" || r.WorkflowName != "paid" || r.WorkflowRevision != "revision" {
		t.Fatalf("result=%+v err=%v", r, err)
	}
	if body.WorkflowName != "paid" || !body.From.Equal(from) || !body.Until.Equal(from.Add(time.Hour)) {
		t.Fatalf("request body=%+v", body)
	}
}
