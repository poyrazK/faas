package main_test

import (
	"encoding/json"
	"github.com/onebox-faas/faas/cmd/gregale"
	"net/http"
	"strings"
	"testing"
)

func TestCmdEventsWorkflowBackfillCreatesConfirmedJob(t *testing.T) {
	main.ResetJSONOut(t)
	fake := main.AuthedFakeAPI(t, `{"id":"job","app_slug":"worker","consumer_kind":"workflow","workflow_name":"paid","workflow_revision":"abc","state":"running"}`, http.StatusAccepted)
	stdout, restore := main.SwapStdout(t)
	defer restore()
	if code := main.CmdEventsWorkflowBackfill([]string{
		"worker", "--workflow-name", "paid", "--from", "2026-10-01T00:00:00Z", "--until", "2026-10-02T00:00:00Z", "--yes",
	}); code != 0 {
		t.Fatalf("exit=%d", code)
	}
	var body map[string]any
	if err := json.Unmarshal((*fake.SawBody()), &body); err != nil {
		t.Fatalf("body=%s: %v", (*fake.SawBody()), err)
	}
	if (*fake.SawMethod()) != http.MethodPost || (*fake.SawPath()) != "/v1/apps/worker/workflow-event-replays" || body["workflow_name"] != "paid" ||
		!strings.Contains(stdout.String(), "workflow/paid") || !strings.Contains(stdout.String(), "Captured workflow revision: abc") {
		t.Fatalf("request=%s %s body=%v output=%q", (*fake.SawMethod()), (*fake.SawPath()), body, stdout)
	}
}

func TestCmdEventsWorkflowBackfillRequiresConfirmationAndValidRange(t *testing.T) {
	main.ResetJSONOut(t)
	for _, args := range [][]string{
		{"worker", "--workflow-name", "paid", "--from", "bad", "--until", "2026-10-02T00:00:00Z", "--yes"},
		{"worker", "--workflow-name", "paid", "--from", "2026-10-02T00:00:00Z", "--until", "2026-10-01T00:00:00Z", "--yes"},
		{"worker", "--workflow-name", "paid", "--from", "2026-10-01T00:00:00Z", "--until", "2026-10-02T00:00:00Z"},
	} {
		code, _ := main.RunWithStderr(t, func() int { return main.CmdEventsWorkflowBackfill(args) })
		if code != 1 {
			t.Fatalf("args=%v exit=%d", args, code)
		}
	}
}
