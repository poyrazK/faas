package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestExportAccountFile_LargeRetryIsAtomicAndReusesRequestID(t *testing.T) {
	largeData := strings.Repeat("x", 5<<20)
	bundle, err := json.Marshal(api.AccountExportResponse{
		SchemaVersion: 2,
		ExportedAt:    "2026-09-16T00:00:00Z",
		AuditTrail: []api.GdprAuditExportResponse{{
			Source: "event", Kind: "account.fixture", RequestedAt: "2026-09-16T00:00:00Z",
			Data: json.RawMessage(`{"payload":"` + largeData + `"}`),
		}},
	})
	if err != nil || len(bundle) <= 4<<20 {
		t.Fatalf("fixture bytes=%d err=%v", len(bundle), err)
	}

	var mu sync.Mutex
	var requestIDs []string
	attempt := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		requestIDs = append(requestIDs, r.Header.Get("X-Faas-Request-Id"))
		attempt++
		current := attempt
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Content-Length", strconv.Itoa(len(bundle)))
		if current == 1 {
			_, _ = w.Write(bundle[:1024])
			return
		}
		_, _ = w.Write(bundle)
	}))
	defer srv.Close()

	out := filepath.Join(t.TempDir(), "export.json")
	if err := ExportAccountFile(NewClient(srv.URL, "token"), context.Background(), out, true); err != nil {
		t.Fatalf("ExportAccountFile: %v", err)
	}
	mu.Lock()
	ids := append([]string(nil), requestIDs...)
	mu.Unlock()
	if len(ids) != 2 || ids[0] == "" || ids[0] != ids[1] {
		t.Fatalf("request ids = %q, want two equal non-empty ids", ids)
	}
	got, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(bundle) {
		t.Fatalf("export bytes=%d, want exact %d-byte response", len(got), len(bundle))
	}
	info, err := os.Stat(out)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("mode=%o, want 0600", info.Mode().Perm())
	}
	temps, err := filepath.Glob(filepath.Join(filepath.Dir(out), ".gregale-export-*.tmp"))
	if err != nil || len(temps) != 0 {
		t.Fatalf("temporary exports=%v err=%v", temps, err)
	}
}
