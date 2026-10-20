package main_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/url"
	"testing"

	"github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
)

func TestWorkflowSchedulesCLIJSONPreservesAvailability(t *testing.T) {
	main.ResetJSONOut(t)
	fake := main.AuthedFakeAPI(t, `{"runtime_enabled":false,"unavailable_reason":"runtime_disabled","schedules":[{"workflow_name":"nightly","deployment_id":"00000000-0000-4000-8000-000000000001","schedule":"0 7 * * *","timezone":"UTC","overlap":"skip","catch_up":"latest","catch_up_window":"2h0m0s","enabled":true,"last_status":"skipped_quota"}]}`, http.StatusOK)
	var output bytes.Buffer
	previous := (*main.OsStdout)
	(*main.OsStdout) = &output
	t.Cleanup(func() { (*main.OsStdout) = previous })
	(*main.JsonOutput) = true
	if code := main.CmdWorkflows([]string{"schedules", "--app", "reports"}); code != 0 {
		t.Fatalf("exit=%d", code)
	}
	if (*fake.SawMethod()) != http.MethodGet || (*fake.SawPath()) != "/v1/apps/reports/workflows/schedules" {
		t.Fatalf("request=%s %s", (*fake.SawMethod()), (*fake.SawPath()))
	}
	var response api.ListWorkflowSchedulesResponse
	if err := json.Unmarshal(output.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.RuntimeEnabled || response.UnavailableReason != "runtime_disabled" || len(response.Schedules) != 1 || response.Schedules[0].LastStatus != "skipped_quota" || response.Schedules[0].CatchUp != "latest" || response.Schedules[0].CatchUpWindow != "2h0m0s" {
		t.Fatalf("response=%+v", response)
	}
}

func TestWorkflowScheduleHistoryCLIForwardsTenantAndPagination(t *testing.T) {
	main.ResetJSONOut(t)
	tenantID := "00000000-0000-4000-8000-000000000002"
	cursor := "00000000-0000-4000-8000-000000000003"
	fake := main.AuthedFakeAPI(t, `{"occurrences":[],"next_cursor":"00000000-0000-4000-8000-000000000004"}`, http.StatusOK)
	var output bytes.Buffer
	previous := (*main.OsStdout)
	(*main.OsStdout) = &output
	t.Cleanup(func() { (*main.OsStdout) = previous })
	(*main.JsonOutput) = true
	if code := main.CmdWorkflows([]string{"schedule-history", "--app", "reports", "--platform-tenant-id", tenantID, "--cursor", cursor, "--limit", "5"}); code != 0 {
		t.Fatalf("exit=%d", code)
	}
	query, err := url.ParseQuery((*fake.SawQuery()))
	if err != nil || (*fake.SawMethod()) != http.MethodGet || (*fake.SawPath()) != "/v1/apps/reports/workflows/schedules/occurrences" || query.Get("platform_tenant_id") != tenantID || query.Get("cursor") != cursor || query.Get("limit") != "5" {
		t.Fatalf("request=%s %s?%s", (*fake.SawMethod()), (*fake.SawPath()), (*fake.SawQuery()))
	}
	var response api.ListWorkflowScheduleOccurrencesResponse
	if err := json.Unmarshal(output.Bytes(), &response); err != nil || response.NextCursor == "" || response.Occurrences == nil {
		t.Fatalf("response=%s err=%v", output.Bytes(), err)
	}
}
