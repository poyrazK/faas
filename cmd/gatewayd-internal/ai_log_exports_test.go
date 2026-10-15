package main

// adr: 1340 — a drain receives its app's AI gateway lines as their own
// source, in order, skipping another account's lines and resuming after
// the last one it queued.

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/logdrain"
	"github.com/onebox-faas/faas/pkg/state"
)

type fakeAILogExportStore struct {
	rows  []state.AIGatewayLogExport
	calls []int64
}

func (s *fakeAILogExportStore) ListAIGatewayLogExports(_ context.Context, _ string, afterID int64, _ time.Time,
	_ time.Duration, limit int) ([]state.AIGatewayLogExport, error) {
	s.calls = append(s.calls, afterID)
	var out []state.AIGatewayLogExport
	for _, row := range s.rows {
		if row.ID > afterID && len(out) < limit {
			out = append(out, row)
		}
	}
	return out, nil
}

func TestCopyAILogExportsIntoADrain(t *testing.T) {
	var mu sync.Mutex
	var delivered []logdrain.Record
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var record logdrain.Record
		_ = json.Unmarshal(body, &record)
		mu.Lock()
		delivered = append(delivered, record)
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	sender, err := logdrain.New(logdrain.Config{Kind: logdrain.KindHTTPJSON, TargetURL: server.URL})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go sender.Run(ctx)
	spec := state.AppLogDrain{ID: "drain-1", AppID: "app-1", AccountID: "acct-1"}
	store := &fakeAILogExportStore{rows: []state.AIGatewayLogExport{
		{ID: 3, AccountID: "acct-1", Line: `{"source":"ai_gateway","model":"a"}`, Attributes: map[string]string{"gen_ai.system": "openai"}},
		{ID: 5, AccountID: "acct-other", Line: `{"source":"ai_gateway","model":"foreign"}`},
		{ID: 8, AccountID: "acct-1", Line: `{"source":"ai_gateway","model":"b"}`},
	}}
	manager := &appLogDrainManager{log: slog.Default()}
	cursor := manager.copyAILogExports(ctx, spec, sender, store, 0, time.Time{})
	if cursor != 8 {
		t.Fatalf("cursor = %d", cursor)
	}
	if again := manager.copyAILogExports(ctx, spec, sender, store, cursor, time.Time{}); again != 8 || store.calls[len(store.calls)-1] != 8 {
		t.Fatalf("second pass cursor = %d, calls %v", again, store.calls)
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		mu.Lock()
		count := len(delivered)
		mu.Unlock()
		if count >= 2 || time.Now().After(deadline) {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(delivered) != 2 || delivered[0].Sequence != 3 || delivered[1].Sequence != 8 || delivered[0].InstanceID != aiLogExportSource ||
		delivered[0].Stream != aiLogExportStream || delivered[0].Attributes["gen_ai.system"] != "openai" || delivered[1].AppID != "app-1" {
		t.Fatalf("delivered = %+v", delivered)
	}
}
