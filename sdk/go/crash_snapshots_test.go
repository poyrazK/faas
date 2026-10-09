package faas

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestCaptureCrashSnapshot(t *testing.T) {
	var got map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %s", r.Method)
		}
		_ = json.NewDecoder(r.Body).Decode(&got)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"captured","capture_id":"c1","in_fork":true}`))
	}))
	defer srv.Close()

	res := CaptureCrashSnapshot(context.Background(), CrashSnapshotOptions{
		Reason: "checkout panic", Route: "/orders", Wait: 5 * time.Second, Endpoint: srv.URL,
	})
	if res != (CrashSnapshotResult{Status: "captured", CaptureID: "c1", InFork: true}) {
		t.Fatalf("result = %+v", res)
	}
	if got["reason"] != "checkout panic" || got["route"] != "/orders" || got["wait_ms"] != float64(5000) {
		t.Fatalf("request body = %v", got)
	}
}

func TestCaptureCrashSnapshotUnavailable(t *testing.T) {
	res := CaptureCrashSnapshot(context.Background(), CrashSnapshotOptions{Endpoint: "http://127.0.0.1:1/none"})
	if res.Status != "unavailable" {
		t.Fatalf("unreachable endpoint = %+v, want unavailable", res)
	}
}
