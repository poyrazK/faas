package mcphosting

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

const taskTimestamp = "2026-10-06T12:00:00Z"

func writeTaskResponse(w http.ResponseWriter, id int, result string) {
	w.Header().Set("Content-Type", "application/json")
	_, _ = fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%d,"result":%s}`, id, result)
}

func writeTaskSSEMessage(w http.ResponseWriter, message any) {
	encoded, _ := json.Marshal(message)
	_, _ = fmt.Fprintf(w, "data: %s\n\n", encoded)
}

func advertiseTasks(client *Client) {
	client.Capabilities = ServerCapabilities{
		Tools: &ToolsCapability{},
		Extensions: map[string]json.RawMessage{
			TasksExtensionID: json.RawMessage(`{}`),
		},
	}
}

func checkTaskRequest(t *testing.T, r *http.Request, method, id string, params map[string]any) {
	t.Helper()
	if r.Header.Get("Mcp-Method") != method {
		t.Fatalf("header=%q, want %q", r.Header.Get("Mcp-Method"), method)
	}
	if id != "" && r.Header.Get("Mcp-Name") != id {
		t.Fatalf("Mcp-Name=%q, want task ID %q", r.Header.Get("Mcp-Name"), id)
	}
	meta, _ := params["_meta"].(map[string]any)
	caps, _ := meta["io.modelcontextprotocol/clientCapabilities"].(map[string]any)
	extensions, _ := caps["extensions"].(map[string]any)
	if _, ok := extensions[TasksExtensionID]; !ok {
		t.Fatalf("Tasks extension capability missing: %v", caps)
	}
}

func TestCallWithTasksPollsToCompletion(t *testing.T) {
	var polls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			ID     int            `json:"id"`
			Method string         `json:"method"`
			Params map[string]any `json:"params"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatal(err)
		}
		if request.Method == "tools/call" {
			w.Header().Set("Content-Type", "application/json")
			meta, _ := request.Params["_meta"].(map[string]any)
			caps, _ := meta["io.modelcontextprotocol/clientCapabilities"].(map[string]any)
			extensions, _ := caps["extensions"].(map[string]any)
			if _, ok := extensions[TasksExtensionID]; !ok || r.Header.Get("Mcp-Name") != "report" {
				t.Errorf("task opt-in or tool routing header missing: caps=%v header=%q", caps, r.Header.Get("Mcp-Name"))
			}
			writeTaskResponse(w, request.ID, fmt.Sprintf(`{"resultType":"task","taskId":"task-123","status":"working","createdAt":%q,"lastUpdatedAt":%q,"ttlMs":60000,"pollIntervalMs":0}`, taskTimestamp, taskTimestamp))
			return
		}
		if request.Method == "subscriptions/listen" {
			w.Header().Set("Content-Type", "application/json")
			_, _ = fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%d,"error":{"code":-32601,"message":"method not found"}}`, request.ID)
			return
		}
		if request.Method != "tasks/get" {
			t.Errorf("unexpected method %q", request.Method)
			return
		}
		checkTaskRequest(t, r, "tasks/get", "task-123", request.Params)
		if r.Header.Get("Mcp-Name") != "task-123" || request.Params["taskId"] != "task-123" {
			t.Errorf("task routing mismatch: header=%q params=%v", r.Header.Get("Mcp-Name"), request.Params)
		}
		polls.Add(1)
		if polls.Load() == 1 {
			writeTaskResponse(w, request.ID, fmt.Sprintf(`{"resultType":"complete","taskId":"task-123","status":"working","createdAt":%q,"lastUpdatedAt":%q,"ttlMs":60000,"pollIntervalMs":0}`, taskTimestamp, taskTimestamp))
			return
		}
		writeTaskResponse(w, request.ID, fmt.Sprintf(`{"resultType":"complete","taskId":"task-123","status":"completed","createdAt":%q,"lastUpdatedAt":"2026-10-06T12:00:01Z","ttlMs":60000,"pollIntervalMs":0,"result":{"resultType":"complete","content":[{"type":"text","text":"finished"}]}}`, taskTimestamp))
	}))
	defer server.Close()
	client, err := NewClient(server.URL+"/mcp", "", ProtocolVersion)
	if err != nil {
		t.Fatal(err)
	}
	advertiseTasks(client)
	var statuses []string
	exchange, err := client.CallWithOptions(context.Background(), Tool{Name: "report", InputSchema: map[string]any{"type": "object"}}, nil, CallOptions{
		EnableTasks: true, WaitForTask: true,
		OnTask: func(task Task) { statuses = append(statuses, task.Status) },
	})
	if err != nil || polls.Load() != 2 || !strings.Contains(string(exchange.Result), "finished") || strings.Join(statuses, ",") != "working,completed" {
		t.Fatalf("exchange=%+v err=%v polls=%d statuses=%v", exchange, err, polls.Load(), statuses)
	}
}

func TestCallWithTasksUpdatesInputAndCanResumeLater(t *testing.T) {
	var polls atomic.Int32
	var updates atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			ID     int            `json:"id"`
			Method string         `json:"method"`
			Params map[string]any `json:"params"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatal(err)
		}
		switch request.Method {
		case "tools/call":
			writeTaskResponse(w, request.ID, fmt.Sprintf(`{"resultType":"task","taskId":"task-input","status":"working","createdAt":%q,"lastUpdatedAt":%q,"ttlMs":60000,"pollIntervalMs":0}`, taskTimestamp, taskTimestamp))
		case "tasks/get":
			if r.Header.Get("Mcp-Name") != "task-input" || request.Params["taskId"] != "task-input" {
				t.Errorf("task routing mismatch: header=%q params=%v", r.Header.Get("Mcp-Name"), request.Params)
			}
			polls.Add(1)
			if polls.Load() == 1 {
				writeTaskResponse(w, request.ID, fmt.Sprintf(`{"resultType":"complete","taskId":"task-input","status":"input_required","createdAt":%q,"lastUpdatedAt":%q,"ttlMs":60000,"pollIntervalMs":0,"inputRequests":{"approval":{"method":"elicitation/create","params":{"mode":"form","message":"Approve the export?","requestedSchema":{"type":"object","properties":{"approved":{"type":"boolean"}},"required":["approved"]}}}}}`, taskTimestamp, taskTimestamp))
				return
			}
			writeTaskResponse(w, request.ID, fmt.Sprintf(`{"resultType":"complete","taskId":"task-input","status":"completed","createdAt":%q,"lastUpdatedAt":"2026-10-06T12:00:02Z","ttlMs":60000,"result":{"resultType":"complete","content":[{"type":"text","text":"exported"}]}}`, taskTimestamp))
		case "tasks/update":
			checkTaskRequest(t, r, "tasks/update", "task-input", request.Params)
			responses, _ := request.Params["inputResponses"].(map[string]any)
			approval, _ := responses["approval"].(map[string]any)
			content, _ := approval["content"].(map[string]any)
			if approval["action"] != "accept" || content["approved"] != true {
				t.Errorf("unexpected input response: %v", responses)
			}
			updates.Add(1)
			writeTaskResponse(w, request.ID, `{"resultType":"complete"}`)
		case "subscriptions/listen":
			w.Header().Set("Content-Type", "application/json")
			_, _ = fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%d,"error":{"code":-32601,"message":"method not found"}}`, request.ID)
		default:
			t.Errorf("unexpected method %q", request.Method)
		}
	}))
	defer server.Close()
	client, err := NewClient(server.URL+"/mcp", "", ProtocolVersion)
	if err != nil {
		t.Fatal(err)
	}
	advertiseTasks(client)
	var prompted atomic.Int32
	exchange, err := client.CallWithOptions(context.Background(), Tool{Name: "export", InputSchema: map[string]any{"type": "object"}}, nil, CallOptions{
		WaitForTask: true,
		Responder: func(_ context.Context, request InputRequest) (InputResponse, error) {
			prompted.Add(1)
			if request.Tool != "export" || request.ID != "approval" || request.Message != "Approve the export?" {
				t.Errorf("unexpected input request %+v", request)
			}
			return InputResponse{Action: "accept", Content: map[string]any{"approved": true}}, nil
		},
	})
	if err != nil || updates.Load() != 1 || polls.Load() != 2 || prompted.Load() != 1 || !strings.Contains(string(exchange.Result), "exported") {
		t.Fatalf("exchange=%+v err=%v updates=%d polls=%d prompted=%d", exchange, err, updates.Load(), polls.Load(), prompted.Load())
	}
}

func TestGetAndCancelTaskUseTaskRoutingHeader(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			ID     int            `json:"id"`
			Method string         `json:"method"`
			Params map[string]any `json:"params"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatal(err)
		}
		id, _ := request.Params["taskId"].(string)
		checkTaskRequest(t, r, request.Method, id, request.Params)
		if id != "task-456" {
			t.Errorf("taskId=%q", id)
		}
		if request.Method == "tasks/cancel" {
			writeTaskResponse(w, request.ID, `{"resultType":"complete"}`)
			return
		}
		writeTaskResponse(w, request.ID, fmt.Sprintf(`{"resultType":"complete","taskId":"task-456","status":"working","createdAt":%q,"lastUpdatedAt":%q,"ttlMs":null,"pollIntervalMs":250}`, taskTimestamp, taskTimestamp))
	}))
	defer server.Close()
	client, err := NewClient(server.URL+"/mcp", "", ProtocolVersion)
	if err != nil {
		t.Fatal(err)
	}
	advertiseTasks(client)
	task, _, err := client.GetTask(context.Background(), "task-456")
	if err != nil || task.Status != "working" {
		t.Fatalf("task=%+v err=%v", task, err)
	}
	if _, err := client.CancelTask(context.Background(), "task-456"); err != nil {
		t.Fatal(err)
	}
}

func TestWaitTaskResumesSavedHandleWithoutRepeatingToolCall(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			ID     int            `json:"id"`
			Method string         `json:"method"`
			Params map[string]any `json:"params"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatal(err)
		}
		calls.Add(1)
		switch request.Method {
		case "server/discover":
			writeTaskResponse(w, request.ID, fmt.Sprintf(`{"supportedVersions":[%q],"capabilities":{"tools":{},"extensions":{"%s":{}}}}`, ProtocolVersion, TasksExtensionID))
		case "tasks/get":
			if request.Params["taskId"] != "saved-task" || r.Header.Get("Mcp-Name") != "saved-task" {
				t.Errorf("task routing mismatch: params=%v header=%q", request.Params, r.Header.Get("Mcp-Name"))
			}
			writeTaskResponse(w, request.ID, fmt.Sprintf(`{"resultType":"complete","taskId":"saved-task","status":"completed","createdAt":%q,"lastUpdatedAt":%q,"ttlMs":null,"result":{"content":[{"type":"text","text":"resumed"}]}}`, taskTimestamp, taskTimestamp))
		default:
			t.Errorf("resuming a task unexpectedly sent %q", request.Method)
		}
	}))
	defer server.Close()
	client, err := NewClient(server.URL+"/mcp", "", ProtocolVersion)
	if err != nil {
		t.Fatal(err)
	}
	task, err := client.WaitTask(context.Background(), "saved-task", CallOptions{})
	if err != nil || task.Status != "completed" || calls.Load() != 2 || !strings.Contains(string(task.Result), "resumed") {
		t.Fatalf("task=%+v err=%v requests=%d", task, err, calls.Load())
	}
}

func TestWaitTaskPrefersTaskStatusSubscription(t *testing.T) {
	var gets, subscriptions atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			ID     int            `json:"id"`
			Method string         `json:"method"`
			Params map[string]any `json:"params"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
			return
		}
		switch request.Method {
		case "tasks/get":
			gets.Add(1)
			writeTaskResponse(w, request.ID, fmt.Sprintf(`{"resultType":"complete","taskId":"subscribed-task","status":"working","createdAt":%q,"lastUpdatedAt":%q,"ttlMs":60000,"pollIntervalMs":0}`, taskTimestamp, taskTimestamp))
		case "subscriptions/listen":
			subscriptions.Add(1)
			if r.Header.Get("Mcp-Method") != "subscriptions/listen" || r.Header.Get("MCP-Protocol-Version") != ProtocolVersion || !strings.Contains(r.Header.Get("Accept"), "text/event-stream") {
				t.Errorf("subscription headers are incomplete: %v", r.Header)
			}
			checkTaskRequest(t, r, "subscriptions/listen", "", request.Params)
			notifications, _ := request.Params["notifications"].(map[string]any)
			taskIDs, _ := notifications["taskIds"].([]any)
			if len(taskIDs) != 1 || taskIDs[0] != "subscribed-task" {
				t.Errorf("subscription task IDs=%v", taskIDs)
			}
			w.Header().Set("Content-Type", "text/event-stream")
			writeTaskSSEMessage(w, map[string]any{"jsonrpc": "2.0", "method": "notifications/subscriptions/acknowledged", "params": map[string]any{"notifications": map[string]any{"taskIds": []string{"subscribed-task"}}}})
			writeTaskSSEMessage(w, map[string]any{"jsonrpc": "2.0", "method": "notifications/tasks", "params": map[string]any{"taskId": "subscribed-task", "status": "working", "createdAt": taskTimestamp, "lastUpdatedAt": taskTimestamp, "ttlMs": 60000, "pollIntervalMs": 0}})
			writeTaskSSEMessage(w, map[string]any{"jsonrpc": "2.0", "method": "notifications/tasks", "params": map[string]any{"taskId": "subscribed-task", "status": "completed", "createdAt": taskTimestamp, "lastUpdatedAt": "2026-10-06T12:00:01Z", "ttlMs": 60000, "result": map[string]any{"resultType": "complete", "content": []any{map[string]any{"type": "text", "text": "subscribed"}}}}})
		default:
			t.Errorf("unexpected method %q", request.Method)
		}
	}))
	defer server.Close()
	client, err := NewClient(server.URL+"/mcp", "", ProtocolVersion)
	if err != nil {
		t.Fatal(err)
	}
	advertiseTasks(client)
	var statuses []string
	task, err := client.WaitTask(context.Background(), "subscribed-task", CallOptions{OnTask: func(task Task) { statuses = append(statuses, task.Status) }})
	if err != nil || task.Status != "completed" || gets.Load() != 1 || subscriptions.Load() != 1 || strings.Join(statuses, ",") != "working,completed" {
		t.Fatalf("task=%+v err=%v gets=%d subscriptions=%d statuses=%v", task, err, gets.Load(), subscriptions.Load(), statuses)
	}
}

func TestWaitTaskReconnectsAfterSubscriptionCloses(t *testing.T) {
	var gets, subscriptions atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			ID     int    `json:"id"`
			Method string `json:"method"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
			return
		}
		switch request.Method {
		case "tasks/get":
			gets.Add(1)
			writeTaskResponse(w, request.ID, fmt.Sprintf(`{"resultType":"complete","taskId":"reconnect-task","status":"working","createdAt":%q,"lastUpdatedAt":%q,"ttlMs":60000,"pollIntervalMs":0}`, taskTimestamp, taskTimestamp))
		case "subscriptions/listen":
			attempt := subscriptions.Add(1)
			w.Header().Set("Content-Type", "text/event-stream")
			writeTaskSSEMessage(w, map[string]any{"jsonrpc": "2.0", "method": "notifications/subscriptions/acknowledged", "params": map[string]any{"notifications": map[string]any{"taskIds": []string{"reconnect-task"}}}})
			if attempt == 1 {
				writeTaskSSEMessage(w, map[string]any{"jsonrpc": "2.0", "method": "notifications/tasks", "params": map[string]any{"taskId": "reconnect-task", "status": "working", "createdAt": taskTimestamp, "lastUpdatedAt": taskTimestamp, "ttlMs": 60000}})
				writeTaskSSEMessage(w, map[string]any{"jsonrpc": "2.0", "id": request.ID, "result": map[string]any{"resultType": "complete"}})
				return
			}
			writeTaskSSEMessage(w, map[string]any{"jsonrpc": "2.0", "method": "notifications/tasks", "params": map[string]any{"taskId": "reconnect-task", "status": "completed", "createdAt": taskTimestamp, "lastUpdatedAt": "2026-10-06T12:00:02Z", "ttlMs": 60000, "result": map[string]any{"resultType": "complete", "content": []any{map[string]any{"type": "text", "text": "reconnected"}}}}})
		default:
			t.Errorf("unexpected method %q", request.Method)
		}
	}))
	defer server.Close()
	client, err := NewClient(server.URL+"/mcp", "", ProtocolVersion)
	if err != nil {
		t.Fatal(err)
	}
	advertiseTasks(client)
	task, err := client.WaitTask(context.Background(), "reconnect-task", CallOptions{})
	if err != nil || task.Status != "completed" || gets.Load() != 1 || subscriptions.Load() != 2 {
		t.Fatalf("task=%+v err=%v gets=%d subscriptions=%d", task, err, gets.Load(), subscriptions.Load())
	}
}

func TestWaitTaskFallsBackToPollingAfterRepeatedDisconnects(t *testing.T) {
	var gets, subscriptions atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			ID     int    `json:"id"`
			Method string `json:"method"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
			return
		}
		switch request.Method {
		case "tasks/get":
			if gets.Add(1) == 1 {
				writeTaskResponse(w, request.ID, fmt.Sprintf(`{"resultType":"complete","taskId":"unstable-task","status":"working","createdAt":%q,"lastUpdatedAt":%q,"ttlMs":60000,"pollIntervalMs":0}`, taskTimestamp, taskTimestamp))
				return
			}
			writeTaskResponse(w, request.ID, fmt.Sprintf(`{"resultType":"complete","taskId":"unstable-task","status":"completed","createdAt":%q,"lastUpdatedAt":"2026-10-06T12:00:03Z","ttlMs":60000,"result":{"content":[{"type":"text","text":"polled after disconnects"}]}}`, taskTimestamp))
		case "subscriptions/listen":
			subscriptions.Add(1)
			w.Header().Set("Content-Type", "text/event-stream")
			writeTaskSSEMessage(w, map[string]any{"jsonrpc": "2.0", "method": "notifications/subscriptions/acknowledged", "params": map[string]any{"notifications": map[string]any{"taskIds": []string{"unstable-task"}}}})
			writeTaskSSEMessage(w, map[string]any{"jsonrpc": "2.0", "method": "notifications/tasks", "params": map[string]any{"taskId": "unstable-task", "status": "working", "createdAt": taskTimestamp, "lastUpdatedAt": taskTimestamp, "ttlMs": 60000}})
		default:
			t.Errorf("unexpected method %q", request.Method)
		}
	}))
	defer server.Close()
	client, err := NewClient(server.URL+"/mcp", "", ProtocolVersion)
	if err != nil {
		t.Fatal(err)
	}
	advertiseTasks(client)
	task, err := client.WaitTask(context.Background(), "unstable-task", CallOptions{})
	if err != nil || task.Status != "completed" || gets.Load() != 2 || subscriptions.Load() != maxTaskStreamReconnects {
		t.Fatalf("task=%+v err=%v gets=%d subscriptions=%d", task, err, gets.Load(), subscriptions.Load())
	}
}

func TestWaitTaskFallsBackToPollingWhenSubscriptionsUnsupported(t *testing.T) {
	var gets, subscriptions atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			ID     int    `json:"id"`
			Method string `json:"method"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
			return
		}
		switch request.Method {
		case "tasks/get":
			if gets.Add(1) == 1 {
				writeTaskResponse(w, request.ID, fmt.Sprintf(`{"resultType":"complete","taskId":"poll-fallback-task","status":"working","createdAt":%q,"lastUpdatedAt":%q,"ttlMs":60000,"pollIntervalMs":0}`, taskTimestamp, taskTimestamp))
				return
			}
			writeTaskResponse(w, request.ID, fmt.Sprintf(`{"resultType":"complete","taskId":"poll-fallback-task","status":"completed","createdAt":%q,"lastUpdatedAt":"2026-10-06T12:00:03Z","ttlMs":60000,"result":{"content":[{"type":"text","text":"polled"}]}}`, taskTimestamp))
		case "subscriptions/listen":
			subscriptions.Add(1)
			w.Header().Set("Content-Type", "application/json")
			_, _ = fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%d,"error":{"code":-32601,"message":"method not found"}}`, request.ID)
		default:
			t.Errorf("unexpected method %q", request.Method)
		}
	}))
	defer server.Close()
	client, err := NewClient(server.URL+"/mcp", "", ProtocolVersion)
	if err != nil {
		t.Fatal(err)
	}
	advertiseTasks(client)
	task, err := client.WaitTask(context.Background(), "poll-fallback-task", CallOptions{})
	if err != nil || task.Status != "completed" || gets.Load() != 2 || subscriptions.Load() != 1 {
		t.Fatalf("task=%+v err=%v gets=%d subscriptions=%d", task, err, gets.Load(), subscriptions.Load())
	}
}

func TestCallWithTasksRequiresServerExtensionAdvertisement(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			ID     int    `json:"id"`
			Method string `json:"method"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatal(err)
		}
		calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		if request.Method == "server/discover" {
			_, _ = fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%d,"result":{"supportedVersions":[%q],"capabilities":{"tools":{}}}}`, request.ID, ProtocolVersion)
			return
		}
		t.Errorf("unexpected request %q", request.Method)
	}))
	defer server.Close()
	client, err := NewClient(server.URL+"/mcp", "", ProtocolVersion)
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.CallWithOptions(context.Background(), Tool{Name: "run", InputSchema: map[string]any{"type": "object"}}, nil, CallOptions{EnableTasks: true})
	if err == nil || !strings.Contains(err.Error(), "does not advertise") || calls.Load() != 1 {
		t.Fatalf("err=%v requests=%d", err, calls.Load())
	}
}

func TestWaitForTaskRequestsCancellationWhenContextEnds(t *testing.T) {
	cancelled := make(chan struct{}, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			ID     int            `json:"id"`
			Method string         `json:"method"`
			Params map[string]any `json:"params"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatal(err)
		}
		w.Header().Set("Content-Type", "application/json")
		switch request.Method {
		case "tools/call":
			writeTaskResponse(w, request.ID, fmt.Sprintf(`{"resultType":"task","taskId":"task-cancel","status":"working","createdAt":%q,"lastUpdatedAt":%q,"ttlMs":60000,"pollIntervalMs":5000}`, taskTimestamp, taskTimestamp))
		case "tasks/cancel":
			if r.Header.Get("Mcp-Name") != "task-cancel" {
				t.Errorf("cancel route header=%q", r.Header.Get("Mcp-Name"))
			}
			cancelled <- struct{}{}
			writeTaskResponse(w, request.ID, `{"resultType":"complete"}`)
		case "subscriptions/listen":
			_, _ = fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%d,"error":{"code":-32601,"message":"method not found"}}`, request.ID)
		default:
			t.Errorf("unexpected method %q", request.Method)
		}
	}))
	defer server.Close()
	client, err := NewClient(server.URL+"/mcp", "", ProtocolVersion)
	if err != nil {
		t.Fatal(err)
	}
	advertiseTasks(client)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	_, err = client.CallWithOptions(ctx, Tool{Name: "long", InputSchema: map[string]any{"type": "object"}}, nil, CallOptions{WaitForTask: true})
	if err == nil || !strings.Contains(err.Error(), "deadline exceeded") {
		t.Fatalf("expected task wait timeout, got %v", err)
	}
	select {
	case <-cancelled:
	case <-time.After(time.Second):
		t.Fatal("CLI timeout did not request task cancellation")
	}
}

func TestDecodeTaskRejectsMalformedTaskEnvelope(t *testing.T) {
	valid := fmt.Sprintf(`{"resultType":"complete","taskId":"task-1","status":"working","createdAt":%q,"lastUpdatedAt":%q,"ttlMs":null}`, taskTimestamp, taskTimestamp)
	for _, tc := range []struct {
		name, body, want string
	}{
		{name: "wrong discriminator", body: strings.Replace(valid, `"complete"`, `"task"`, 1), want: "resultType"},
		{name: "wrong ID", body: valid, want: "does not match"},
		{name: "missing TTL", body: strings.Replace(valid, `,"ttlMs":null`, "", 1), want: "ttlMs"},
		{name: "invalid poll interval", body: strings.TrimSuffix(valid, "}") + `,"pollIntervalMs":-1}`, want: "outside"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			wantType, expectedID := "complete", ""
			if tc.name == "wrong discriminator" {
				wantType = "complete"
			}
			if tc.name == "wrong ID" {
				expectedID = "different"
			}
			_, err := decodeTask(json.RawMessage(tc.body), wantType, expectedID)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err=%v, want substring %q", err, tc.want)
			}
		})
	}
}
