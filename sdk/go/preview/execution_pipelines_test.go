package preview_test

import (
	"context"
	"encoding/json"
	faas "github.com/gregale-dev/gregale-go"
	"github.com/gregale-dev/gregale-go/preview"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestExecutionPipelineV2AndAttemptLineage(t *testing.T) {
	var paths []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.EscapedPath())
		w.Header().Set("Content-Type", "application/json")
		if len(paths) == 1 {
			var body map[string]any
			if json.NewDecoder(r.Body).Decode(&body) != nil || body["batch"] == nil {
				t.Error("batch body missing")
			}
			w.WriteHeader(202)
			_, _ = w.Write([]byte(`{"workflow_id":"batch:one","plan_id":"aaaaaaaaaaaaaaaaaaaaaaaa","status":"queued","step_count":1,"pipeline":{"deadline_at":"2026-11-16T00:00:00Z","completion":{"version":"v1","completion_id":"completion-1","workflow_id":"batch:one","outcome":"succeeded","finished_at":"2026-11-16T00:00:00Z","receipt":"/v1/execution-workflows/batch:one","usage":{"wall_time_ms":2,"cpu_time_ms":1,"peak_memory_mb":8},"steps":[]},"steps":[{"label":"one","max_attempts":2,"attempts":[{"run_id":"run-2","attempt":2,"previous_run_id":"run-1","status":"queued"}]}]}}`))
		} else {
			_, _ = w.Write([]byte(`{"workflow_id":"batch:one"}`))
		}
	}))
	defer server.Close()
	client, err := faas.NewClient(server.URL, "token")
	if err != nil {
		t.Fatal(err)
	}
	request := preview.CreateManagedExecutionWorkflowRequest{WorkflowID: "batch:one", Version: "v1", Batch: &preview.ExecutionTaskBatch{Name: "report", Version: 2, Items: []preview.ExecutionTaskBatchItem{{Label: "one"}}}}
	out, err := client.CreateManagedExecutionWorkflow(context.Background(), request)
	if err != nil || out.Pipeline == nil || out.Pipeline.Completion == nil || out.Pipeline.Completion.CompletionID != "completion-1" || out.Pipeline.Steps[0].Attempts[0].PreviousRunID != "run-1" {
		t.Fatal("lineage", out, err)
	}
	if _, err = client.CancelExecutionPipeline(context.Background(), "batch:one"); err != nil {
		t.Fatal(err)
	}
	if paths[0] != "/v2/execution-workflows" || paths[1] != "/v1/execution-workflows/batch:one/cancel" {
		t.Fatal(paths)
	}
}

func TestPipelineProgressCursorAndRerunContract(t *testing.T) {
	var paths []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.RequestURI())
		w.Header().Set("Content-Type", "application/json")
		if r.Method == "POST" {
			var body preview.RerunExecutionPipelineRequest
			if json.NewDecoder(r.Body).Decode(&body) != nil || body.WorkflowID != "recovery" || len(body.RepeatSteps) != 1 {
				t.Error("rerun intent missing")
			}
			w.WriteHeader(202)
			_, _ = w.Write([]byte(`{"id":"new-pipeline","workflow_id":"recovery","status":"queued"}`))
			return
		}
		_, _ = w.Write([]byte(`{"pipeline_id":"pipeline","events":[{"sequence":7,"type":"pipeline","status":"failed","created_at":"2026-11-17T00:00:00Z"}],"next_sequence":7,"gap":true,"terminal":true}`))
	}))
	defer server.Close()
	client, err := faas.NewClient(server.URL, "token")
	if err != nil {
		t.Fatal(err)
	}
	seen := 0
	err = client.WatchExecutionPipeline(context.Background(), "pipeline", 2, func(page preview.ExecutionPipelineEventsResponse) error {
		seen++
		if !page.Gap || !page.Terminal || page.NextSequence != 7 {
			t.Fatal(page)
		}
		return nil
	})
	if err != nil || seen != 1 {
		t.Fatal(seen, err)
	}
	out, err := client.RerunExecutionPipeline(context.Background(), "pipeline", preview.RerunExecutionPipelineRequest{WorkflowID: "recovery", RepeatSteps: []string{"failed"}})
	if err != nil || out.ID != "new-pipeline" || paths[0] != "/v1/execution-pipelines/pipeline/events?after=2" || paths[1] != "/v2/execution-pipelines/pipeline/reruns" {
		t.Fatal(paths, out, err)
	}
}
