package mcphosting

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sync/atomic"
	"testing"
	"time"
)

func TestWaitTaskAcceptsDefaultFormMode(t *testing.T) {
	for _, tc := range []struct {
		name       string
		properties string
		content    map[string]any
	}{
		{"field", `{"approved":{"type":"boolean"}}`, map[string]any{"approved": true}},
		{"empty consent", `{}`, map[string]any{}},
	} {
		t.Run(tc.name, func(t *testing.T) { testWaitTaskForm(t, tc.properties, tc.content) })
	}
}

func testWaitTaskForm(t *testing.T, properties string, content map[string]any) {
	t.Helper()
	var updates, prompts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			ID     int    `json:"id"`
			Method string `json:"method"`
			Params struct {
				InputResponses map[string]InputResponse `json:"inputResponses"`
			} `json:"params"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
			return
		}
		switch request.Method {
		case "tasks/get":
			if updates.Load() == 0 {
				writeTaskResponse(w, request.ID, fmt.Sprintf(`{"resultType":"complete","taskId":"approval-task","status":"input_required","createdAt":%q,"lastUpdatedAt":%q,"ttlMs":60000,"pollIntervalMs":0,"inputRequests":{"approval":{"method":"elicitation/create","params":{"message":"Approve?","requestedSchema":{"type":"object","properties":%s}}}}}`, taskTimestamp, taskTimestamp, properties))
			} else {
				writeTaskResponse(w, request.ID, fmt.Sprintf(`{"resultType":"complete","taskId":"approval-task","status":"completed","createdAt":%q,"lastUpdatedAt":%q,"ttlMs":60000,"result":{"content":[]}}`, taskTimestamp, taskTimestamp))
			}
		case "tasks/update":
			if got := request.Params.InputResponses["approval"]; got.Action != "accept" || !reflect.DeepEqual(got.Content, content) {
				t.Error("task update lost the explicit form response")
			}
			updates.Add(1)
			writeTaskResponse(w, request.ID, `{"resultType":"complete"}`)
		case "subscriptions/listen":
			w.Header().Set("Content-Type", "application/json")
			_, _ = fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%d,"error":{"code":-32601,"message":"unsupported"}}`, request.ID)
		default:
			t.Errorf("unexpected method %s", request.Method)
		}
	}))
	defer server.Close()
	client, err := NewClient(server.URL+"/mcp", "", ProtocolVersion)
	if err != nil {
		t.Fatal(err)
	}
	advertiseTasks(client)
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	task, err := client.WaitTask(ctx, "approval-task", CallOptions{Responder: func(context.Context, InputRequest) (InputResponse, error) {
		prompts.Add(1)
		return InputResponse{Action: "accept", Content: content}, nil
	}})
	if err != nil || task.Status != "completed" || updates.Load() != 1 || prompts.Load() != 1 {
		t.Fatalf("task=%+v err=%v prompts=%d updates=%d", task, err, prompts.Load(), updates.Load())
	}
}

func TestInteractiveCallEmptyConsentFormRequiresExplicitResponse(t *testing.T) {
	for _, action := range []string{"accept", "decline", "cancel"} {
		t.Run(action, func(t *testing.T) {
			want := InputResponse{Action: action}
			if action == "accept" {
				want.Content = map[string]any{}
			}
			var prompts, continuations atomic.Int32
			client := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				var request struct {
					ID     int    `json:"id"`
					Method string `json:"method"`
					Params struct {
						RequestState   string                   `json:"requestState"`
						InputResponses map[string]InputResponse `json:"inputResponses"`
					} `json:"params"`
				}
				if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
					t.Error(err)
					return
				}
				if request.Method != "tools/call" {
					t.Errorf("unexpected method %s", request.Method)
					return
				}
				if request.Params.InputResponses == nil {
					writeTaskResponse(w, request.ID, `{"resultType":"input_required","requestState":"consent","inputRequests":{"approval":{"method":"elicitation/create","params":{"message":"Approve?","requestedSchema":{"type":"object","properties":{}}}}}}`)
					return
				}
				if request.Params.RequestState != "consent" || !reflect.DeepEqual(request.Params.InputResponses["approval"], want) {
					t.Error("continuation lost the explicit consent response")
				}
				continuations.Add(1)
				writeTaskResponse(w, request.ID, `{"resultType":"complete","content":[]}`)
			})
			_, err := client.CallInteractive(t.Context(), Tool{Name: "consent", InputSchema: map[string]any{"type": "object"}}, map[string]any{}, false,
				func(_ context.Context, form InputRequest) (InputResponse, error) {
					if properties, ok := form.Schema["properties"].(map[string]any); !ok || len(properties) != 0 {
						t.Fatal("responder did not receive the empty form")
					}
					prompts.Add(1)
					return want, nil
				})
			if err != nil || prompts.Load() != 1 || continuations.Load() != 1 {
				t.Fatalf("error=%v prompts=%d continuations=%d", err, prompts.Load(), continuations.Load())
			}
		})
	}
}

func TestTaskInputFormPropertyBounds(t *testing.T) {
	for _, properties := range []any{nil, []any{}, false, "invalid"} {
		request := TaskInputRequest{Method: "elicitation/create"}
		request.Params.Mode = "form"
		request.Params.RequestedSchema = map[string]any{"type": "object", "properties": properties}
		if _, err := parseTaskInputRequests("consent", map[string]TaskInputRequest{"approval": request}); err == nil {
			t.Fatal("non-object form properties were accepted")
		}
	}
	for _, count := range []int{0, 100, 101} {
		request := TaskInputRequest{Method: "elicitation/create"}
		request.Params.Mode = "form"
		properties := map[string]any{}
		for i := range count {
			properties[fmt.Sprintf("field%d", i)] = map[string]any{"type": "boolean"}
		}
		request.Params.RequestedSchema = map[string]any{"type": "object", "properties": properties}
		_, err := parseTaskInputRequests("consent", map[string]TaskInputRequest{"approval": request})
		if (err == nil) != (count <= 100) {
			t.Fatalf("form with %d fields: %v", count, err)
		}
	}
}

func TestTaskInputModeRejectsInvalidExplicitValues(t *testing.T) {
	for _, mode := range []string{`null`, `""`, `"invalid"`, `42`} {
		t.Run(mode, func(t *testing.T) {
			var requests map[string]TaskInputRequest
			body := `{"approval":{"method":"elicitation/create","params":{"mode":` + mode + `,"requestedSchema":{"type":"object","properties":{"approved":{"type":"boolean"}}}}}}`
			err := json.Unmarshal([]byte(body), &requests)
			if err == nil {
				_, err = parseTaskInputRequests("work", requests)
			}
			if err == nil {
				t.Fatal("invalid explicit mode was accepted")
			}
		})
	}
}
