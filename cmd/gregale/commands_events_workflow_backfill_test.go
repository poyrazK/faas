package main

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

func TestCmdEventsWorkflowBackfillCreatesConfirmedJob(t *testing.T) {
	resetJSONOut(t)
	fake := authedFakeAPI(t, `{"id":"job","app_slug":"worker","consumer_kind":"workflow","workflow_name":"paid","workflow_revision":"abc","state":"running"}`, http.StatusAccepted)
	stdout, restore := swapStdout(t)
	defer restore()
	if code := cmdEventsWorkflowBackfill([]string{
		"worker", "--workflow-name", "paid", "--from", "2026-10-01T00:00:00Z", "--until", "2026-10-02T00:00:00Z", "--yes",
	}); code != 0 {
		t.Fatalf("exit=%d", code)
	}
	var body map[string]any
	if err := json.Unmarshal(fake.sawBody, &body); err != nil {
		t.Fatalf("body=%s: %v", fake.sawBody, err)
	}
	if fake.sawMethod != http.MethodPost || fake.sawPath != "/v1/apps/worker/workflow-event-replays" || body["workflow_name"] != "paid" ||
		!strings.Contains(stdout.String(), "workflow/paid") || !strings.Contains(stdout.String(), "Captured workflow revision: abc") {
		t.Fatalf("request=%s %s body=%v output=%q", fake.sawMethod, fake.sawPath, body, stdout)
	}
}

func TestCmdEventsWorkflowBackfillRequiresConfirmationAndValidRange(t *testing.T) {
	resetJSONOut(t)
	for _, args := range [][]string{
		{"worker", "--workflow-name", "paid", "--from", "bad", "--until", "2026-10-02T00:00:00Z", "--yes"},
		{"worker", "--workflow-name", "paid", "--from", "2026-10-02T00:00:00Z", "--until", "2026-10-01T00:00:00Z", "--yes"},
		{"worker", "--workflow-name", "paid", "--from", "2026-10-01T00:00:00Z", "--until", "2026-10-02T00:00:00Z"},
	} {
		code, _ := runWithStderr(t, func() int { return cmdEventsWorkflowBackfill(args) })
		if code != 1 {
			t.Fatalf("args=%v exit=%d", args, code)
		}
	}
}
