package api

// adr: 604

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestGetEventStorageUsage(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" || r.URL.Path != "/v1/events/storage" {
			t.Errorf("request=%s %s", r.Method, r.URL)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"retained_events":2,"retained_bytes":1024,"pending_events":1,"oldest_pending_at":"2026-10-05T10:00:00Z","limits":{"retained_events":16384,"retained_bytes":67108864}}`))
	}))
	defer server.Close()
	usage, err := NewClient(server.URL, "").GetEventStorageUsage(context.Background())
	if err != nil || usage.RetainedEvents != 2 || usage.RetainedBytes != 1024 || usage.OldestPendingAt == nil || usage.Limits.RetainedEvents != 16384 {
		t.Fatalf("usage=%+v err=%v", usage, err)
	}
}
