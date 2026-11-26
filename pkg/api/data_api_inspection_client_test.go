package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestDataAPIInspectionClient(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Header.Get("Authorization") != "Bearer token" {
			t.Error("missing authorization")
		}
		switch calls {
		case 1:
			if r.Method != "POST" || r.URL.Path != "/v1/apps/notes/data-api-inspections" || r.Header.Get("Idempotency-Key") == "" {
				t.Errorf("unexpected admission request %s %s", r.Method, r.URL.Path)
			}
			var request CreateDataAPIInspectionRequest
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil || request.DatabaseID != "db" || request.BindingID != "binding" || request.Table != "notes" {
				t.Errorf("wrong request: %+v %v", request, err)
			}
		case 2:
			if r.Method != "GET" || r.URL.Path != "/v1/apps/notes/data-api-inspections/task" {
				t.Errorf("unexpected status request %s %s", r.Method, r.URL.Path)
			}
		default:
			t.Errorf("unexpected request %d", calls)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"task_id":"task","app_id":"app","deployment_id":"dep","database_id":"db","binding_id":"binding","credential_generation":1,"task_status":"succeeded","binding_identity":{"status":"verified","reason":"provider_target_and_trust_matched"}}`))
	}))
	defer srv.Close()
	client := NewClient(srv.URL, "token")
	created, err := client.CreateDataAPIInspection(t.Context(), "notes", CreateDataAPIInspectionRequest{DatabaseID: "db", BindingID: "binding", Table: "notes"})
	if err != nil || created.TaskID != "task" {
		t.Fatalf("create=%+v %v", created, err)
	}
	result, err := client.GetDataAPIInspection(t.Context(), "notes", "task")
	if err != nil || result.BindingIdentity.Status != "verified" {
		t.Fatalf("get=%+v %v", result, err)
	}
	if calls != 2 {
		t.Fatalf("calls=%d", calls)
	}
}
