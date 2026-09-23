package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestAppTaskClientLifecycle(t *testing.T) {
	var methods []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		methods = append(methods, r.Method+" "+r.URL.Path)
		if r.Method == http.MethodPost {
			var request CreateAppTaskRequest
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil || len(request.Command) != 1 || request.Command[0] != "bin/task" {
				t.Fatalf("request decode = %#v, err=%v", request, err)
			}
		}
		if r.Method == http.MethodGet && r.URL.Path == "/v1/apps/my-app/tasks" {
			if r.URL.Query().Get("limit") != "10" || r.URL.Query().Get("offset") != "20" {
				t.Fatalf("list query = %v", r.URL.Query())
			}
			_, _ = w.Write([]byte(`{"tasks":[],"limit":10,"offset":20,"next_offset":-1}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"2bdd4251-f567-4a48-9f66-a155bbfa7751","app_id":"0123456789abcdef0123456789abcdef","deployment_id":"abcdef0123456789abcdef0123456789","deployment_scope":"default","kind":"manual","command":["bin/task"],"command_shell":false,"status":"queued","timeout_seconds":600,"max_output_bytes":1048576,"output_truncated":false,"created_at":"2026-09-23T00:00:00Z","updated_at":"2026-09-23T00:00:00Z"}`))
	}))
	defer server.Close()

	client := NewClient(server.URL, "token")
	ctx := context.Background()
	if _, err := client.CreateAppTask(ctx, "my-app", CreateAppTaskRequest{Command: []string{"bin/task"}}); err != nil {
		t.Fatalf("CreateAppTask: %v", err)
	}
	if _, err := client.ListAppTasks(ctx, "my-app", 10, 20); err != nil {
		t.Fatalf("ListAppTasks: %v", err)
	}
	if _, err := client.GetAppTask(ctx, "my-app", "task-1"); err != nil {
		t.Fatalf("GetAppTask: %v", err)
	}
	if _, err := client.CancelAppTask(ctx, "my-app", "task-1"); err != nil {
		t.Fatalf("CancelAppTask: %v", err)
	}
	want := []string{
		"POST /v1/apps/my-app/tasks",
		"GET /v1/apps/my-app/tasks",
		"GET /v1/apps/my-app/tasks/task-1",
		"DELETE /v1/apps/my-app/tasks/task-1",
	}
	if len(methods) != len(want) {
		t.Fatalf("methods = %v, want %v", methods, want)
	}
	for i := range want {
		if methods[i] != want[i] {
			t.Errorf("methods[%d] = %q, want %q", i, methods[i], want[i])
		}
	}
}
