package apidserver

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/mcpprotocol"
	"github.com/onebox-faas/faas/pkg/promql"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestDashboardMCPPerformanceOwnershipBeforeQuery(t *testing.T) {
	store := state.NewMemStore()
	owner, err := store.CreateAccount(t.Context(), "mcp-performance@example.com", "free")
	if err != nil {
		t.Fatal(err)
	}
	foreign, err := store.CreateAccount(t.Context(), "foreign-performance@example.com", "free")
	if err != nil {
		t.Fatal(err)
	}
	own, err := store.CreateApp(t.Context(), state.App{AccountID: owner.ID, Slug: "performance-own", Type: state.AppTypeApp, Runtime: "node22", Status: state.AppActive})
	if err != nil {
		t.Fatal(err)
	}
	_, err = store.CreateApp(t.Context(), state.App{AccountID: foreign.ID, Slug: "performance-foreign", Type: state.AppTypeApp, Runtime: "node22", Status: state.AppActive})
	if err != nil {
		t.Fatal(err)
	}
	var requests atomic.Int32
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if !strings.Contains(r.URL.Query().Get("query"), `app_id="`+own.ID+`"`) {
			t.Error("query escaped owned app scope")
		}
		w.Header().Set("Content-Type", "application/json")
		if strings.HasSuffix(r.URL.Path, "query_range") {
			_, _ = io.WriteString(w, `{"status":"success","data":{"resultType":"matrix","result":[]}}`)
		} else {
			_, _ = io.WriteString(w, `{"status":"success","data":{"resultType":"vector","result":[]}}`)
		}
	}))
	defer backend.Close()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := &server{store: store, log: log, promqlClient: promql.NewClient(backend.URL, backend.Client())}
	rec := httptest.NewRecorder()
	srv.renderMCPOperations(rec, httptest.NewRequest("GET", "/dashboard/mcp?app=performance-foreign", nil), log, owner)
	if rec.Code != 404 || requests.Load() != 0 {
		t.Fatal("foreign app reached metric backend")
	}
	// A missing timestamp is unavailable, not a successful zero-call dashboard.
	snapshot, err := mcpprotocol.Fetch(t.Context(), srv.promqlClient, own.ID, time.Now())
	if err == nil || len(snapshot.Tools) > 0 {
		t.Fatal("missing telemetry reported as healthy")
	}
	if requests.Load() != 13 {
		t.Fatalf("unexpected query fanout: %d", requests.Load())
	}
}
