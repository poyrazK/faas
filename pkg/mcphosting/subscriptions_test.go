package mcphosting

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestListenCatalogChangeSendsFiltersAndReturnsSelectedNotification(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Mcp-Method") != "subscriptions/listen" || r.Header.Get("MCP-Protocol-Version") != ProtocolVersion || r.Header.Get("Authorization") != "Bearer watch-token" {
			t.Errorf("unexpected subscription headers: %v", r.Header)
		}
		var request struct {
			ID     int    `json:"id"`
			Method string `json:"method"`
			Params struct {
				Notifications map[string]json.RawMessage `json:"notifications"`
				Meta          map[string]json.RawMessage `json:"_meta"`
			} `json:"params"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Errorf("decode listen request: %v", err)
			return
		}
		if request.Method != "subscriptions/listen" || request.ID == 0 {
			t.Errorf("unexpected request: %+v", request)
		}
		for name, want := range map[string]bool{"toolsListChanged": true, "resourcesListChanged": false, "promptsListChanged": true} {
			var got bool
			if err := json.Unmarshal(request.Params.Notifications[name], &got); err != nil || got != want {
				t.Errorf("filter %s=%v err=%v, want %t", name, got, err, want)
			}
		}
		var taskIDs []string
		if err := json.Unmarshal(request.Params.Notifications["taskIds"], &taskIDs); err != nil || len(taskIDs) != 0 {
			t.Errorf("catalog listen taskIds=%v err=%v, want empty array", taskIDs, err)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		flusher := w.(http.Flusher)
		writeSubscriptionSSE(t, w, map[string]any{
			"jsonrpc": "2.0", "method": "notifications/subscriptions/acknowledged",
			"params": map[string]any{"notifications": map[string]bool{"toolsListChanged": true, "promptsListChanged": true}},
		})
		flusher.Flush()
		writeSubscriptionSSE(t, w, map[string]any{"jsonrpc": "2.0", "method": string(CatalogToolsListChanged), "params": map[string]any{}})
		flusher.Flush()
	}))
	defer server.Close()
	client, err := NewClient(server.URL+"/mcp", "watch-token", ProtocolVersion)
	if err != nil {
		t.Fatal(err)
	}
	change, err := client.ListenCatalogChange(context.Background(), CatalogNotificationFilter{
		ToolsListChanged: true, PromptsListChanged: true,
	})
	if err != nil || change != CatalogToolsListChanged {
		t.Fatalf("change=%q err=%v", change, err)
	}
}

func TestListenCatalogChangeRequiresAcknowledgedFilters(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		writeSubscriptionSSE(t, w, map[string]any{
			"jsonrpc": "2.0", "method": "notifications/subscriptions/acknowledged",
			"params": map[string]any{"notifications": map[string]bool{"toolsListChanged": false}},
		})
		w.(http.Flusher).Flush()
	}))
	defer server.Close()
	client, err := NewClient(server.URL+"/mcp", "", ProtocolVersion)
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.ListenCatalogChange(context.Background(), CatalogNotificationFilter{ToolsListChanged: true})
	if !errors.Is(err, ErrSubscriptionsUnsupported) {
		t.Fatalf("err=%v, want unsupported subscription", err)
	}
}

func TestListenCatalogChangeRequiresAtLeastOneFilter(t *testing.T) {
	client := &Client{Version: ProtocolVersion}
	_, err := client.ListenCatalogChange(context.Background(), CatalogNotificationFilter{})
	if err == nil || err.Error() != "MCP catalog subscription requires at least one notification type" {
		t.Fatalf("err=%v", err)
	}
}

func TestListenResourceUpdatedSubscribesToURIAndReturnsUpdate(t *testing.T) {
	const resourceURI = "file:///reports/current"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			ID     int    `json:"id"`
			Method string `json:"method"`
			Params struct {
				Notifications map[string]json.RawMessage `json:"notifications"`
			} `json:"params"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Errorf("decode listen request: %v", err)
			return
		}
		if request.Method != "subscriptions/listen" {
			t.Errorf("method=%q", request.Method)
		}
		var uris []string
		if err := json.Unmarshal(request.Params.Notifications["resourceSubscriptions"], &uris); err != nil || len(uris) != 1 || uris[0] != resourceURI {
			t.Errorf("resource subscriptions=%v err=%v", uris, err)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		writeSubscriptionSSE(t, w, map[string]any{
			"jsonrpc": "2.0", "method": "notifications/subscriptions/acknowledged",
			"params": map[string]any{"notifications": map[string]any{"resourceSubscriptions": []string{resourceURI}}},
		})
		w.(http.Flusher).Flush()
		writeSubscriptionSSE(t, w, map[string]any{
			"jsonrpc": "2.0", "method": string(CatalogResourceUpdated),
			"params": map[string]any{"uri": resourceURI},
		})
		w.(http.Flusher).Flush()
	}))
	defer server.Close()
	client, err := NewClient(server.URL+"/mcp", "", ProtocolVersion)
	if err != nil {
		t.Fatal(err)
	}
	uri, err := client.ListenResourceUpdated(context.Background(), resourceURI)
	if err != nil || uri != resourceURI {
		t.Fatalf("uri=%q err=%v", uri, err)
	}
}

func TestListenResourceUpdatedFallsBackWhenURIWasNotAcknowledged(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		writeSubscriptionSSE(t, w, map[string]any{
			"jsonrpc": "2.0", "method": "notifications/subscriptions/acknowledged",
			"params": map[string]any{"notifications": map[string]any{"resourceSubscriptions": []string{}}},
		})
		w.(http.Flusher).Flush()
	}))
	defer server.Close()
	client, err := NewClient(server.URL+"/mcp", "", ProtocolVersion)
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.ListenResourceUpdated(context.Background(), "file:///not-shared")
	if !errors.Is(err, ErrSubscriptionsUnsupported) {
		t.Fatalf("err=%v, want unsupported subscription", err)
	}
}

func writeSubscriptionSSE(t *testing.T, w http.ResponseWriter, message any) {
	t.Helper()
	encoded, err := json.Marshal(message)
	if err != nil {
		t.Errorf("encode SSE message: %v", err)
		return
	}
	if _, err := fmt.Fprintf(w, "data: %s\n\n", encoded); err != nil {
		t.Errorf("write SSE message: %v", err)
	}
}
