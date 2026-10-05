package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestWorkflowSchedulesCLIJSONPreservesAvailability(t *testing.T) {
	resetJSONOut(t)
	fake := authedFakeAPI(t, `{"runtime_enabled":false,"unavailable_reason":"runtime_disabled","schedules":[{"workflow_name":"nightly","deployment_id":"00000000-0000-4000-8000-000000000001","schedule":"0 7 * * *","timezone":"UTC","overlap":"skip","enabled":true,"last_status":"skipped_quota"}]}`, http.StatusOK)
	var output bytes.Buffer
	previous := osStdout
	osStdout = &output
	t.Cleanup(func() { osStdout = previous })
	jsonOutput = true
	if code := cmdWorkflows([]string{"schedules", "--app", "reports"}); code != 0 {
		t.Fatalf("exit=%d", code)
	}
	if fake.sawMethod != http.MethodGet || fake.sawPath != "/v1/apps/reports/workflows/schedules" {
		t.Fatalf("request=%s %s", fake.sawMethod, fake.sawPath)
	}
	var response api.ListWorkflowSchedulesResponse
	if err := json.Unmarshal(output.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.RuntimeEnabled || response.UnavailableReason != "runtime_disabled" || len(response.Schedules) != 1 || response.Schedules[0].LastStatus != "skipped_quota" {
		t.Fatalf("response=%+v", response)
	}
}
