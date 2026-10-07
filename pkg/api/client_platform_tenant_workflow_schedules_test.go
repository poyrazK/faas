package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestPlatformTenantWorkflowScheduleClientRoutes(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.Header.Get("Authorization") != "Bearer tenant-token" {
			t.Errorf("authorization = %q", r.Header.Get("Authorization"))
		}
		switch requests {
		case 1:
			if r.Method != http.MethodGet || r.URL.Path != "/v1/platform-tenant-self/apps/my-app/workflows/schedules" {
				t.Errorf("list route = %s %s", r.Method, r.URL.String())
			}
			_, _ = w.Write([]byte(`{"schedules":[{"workflow_name":"nightly","deployment_id":"00000000-0000-0000-0000-000000000001","schedule":"0 7 * * *","timezone":"UTC","overlap":"skip","enabled":true,"tenant_configurable":true,"customized":false,"version":0}]}`))
		case 2:
			if r.Method != http.MethodPut || r.URL.Path != "/v1/platform-tenant-self/apps/my-app/workflows/schedules/nightly" {
				t.Errorf("update route = %s %s", r.Method, r.URL.String())
			}
			var request UpdateTenantWorkflowScheduleRequest
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil || request.ExpectedVersion == nil || *request.ExpectedVersion != 0 || request.Schedule != "0 9 * * *" {
				t.Errorf("update body = %+v, err=%v", request, err)
			}
			_, _ = w.Write([]byte(`{"workflow_name":"nightly","deployment_id":"00000000-0000-0000-0000-000000000001","schedule":"0 9 * * *","timezone":"Europe/Istanbul","overlap":"skip","enabled":true,"tenant_configurable":true,"customized":true,"version":1}`))
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.String())
		}
	}))
	defer server.Close()
	client := NewClient(server.URL, "tenant-token")
	listed, err := client.ListTenantWorkflowSchedules(context.Background(), "my-app")
	if err != nil || len(listed.Schedules) != 1 || listed.Schedules[0].WorkflowName != "nightly" {
		t.Fatalf("list schedules = %+v, err=%v", listed, err)
	}
	version := int64(0)
	updated, err := client.UpdateTenantWorkflowSchedule(context.Background(), "my-app", "nightly", UpdateTenantWorkflowScheduleRequest{
		ExpectedVersion: &version, Schedule: "0 9 * * *", Timezone: "Europe/Istanbul",
	})
	if err != nil || updated.Version != 1 || !updated.Customized || requests != 2 {
		t.Fatalf("update schedule = %+v, err=%v requests=%d", updated, err, requests)
	}
}
