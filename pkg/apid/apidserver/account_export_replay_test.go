package apidserver_test

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
)

// TestExportAccount_ReplayHonoredOnlyBriefly: a retry with the export's
// request ID skipped the 24 h limit forever, so one replayed ID ran
// unlimited full exports. Past api.ExportReplayWindow it is a new export.
func TestExportAccount_ReplayHonoredOnlyBriefly(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	acct, err := store.CreateAccount(ctx, "export-replay@example.com", api.PlanHobby)
	if err != nil {
		t.Fatal(err)
	}
	key, hash, err := api.GenerateAPIKey()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateAPIKey(ctx, acct.ID, hash, "export", api.ScopesAdminOnly); err != nil {
		t.Fatal(err)
	}
	h := apidserver.NewServer(store, slog.New(slog.NewTextHandler(io.Discard, nil)), "gregale.dev", apidserver.NoopNotifier{}).Handler()
	export := func() *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, "/v1/account/export?include_secrets=false", nil)
		req.Header.Set("Authorization", "Bearer "+key)
		req.Header.Set("X-Faas-Request-Id", "replayed-export-id")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec
	}

	if rec := export(); rec.Code != http.StatusOK {
		t.Fatalf("first export: %d %s", rec.Code, rec.Body)
	}
	store.BackdateGdprRequestsForTest(ctx, acct.ID, state.GdprActionExport, time.Now().Add(-api.ExportReplayWindow-time.Minute))
	if rec := export(); rec.Code != http.StatusTooManyRequests {
		t.Fatalf("replay after the window: %d, want 429", rec.Code)
	}
}

// TestExportAccount_ReplaysAreCounted: inside the replay window every retry
// with the export's request ID rebuilt the full bundle, so one replayed ID
// ran exports back to back. Only api.ExportReplaysMax retries are honoured.
func TestExportAccount_ReplaysAreCounted(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	acct, err := store.CreateAccount(ctx, "export-replays@example.com", api.PlanHobby)
	if err != nil {
		t.Fatal(err)
	}
	key, hash, err := api.GenerateAPIKey()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateAPIKey(ctx, acct.ID, hash, "export", api.ScopesAdminOnly); err != nil {
		t.Fatal(err)
	}
	h := apidserver.NewServer(store, slog.New(slog.NewTextHandler(io.Discard, nil)), "gregale.dev", apidserver.NoopNotifier{}).Handler()
	export := func(client int) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, "/v1/account/export?include_secrets=false", nil)
		req.Header.Set("Authorization", "Bearer "+key)
		req.Header.Set("X-Faas-Request-Id", "replayed-export-id")
		// Distinct addresses keep the per-IP export bucket out of the way.
		req.RemoteAddr = "198.51.100." + strconv.Itoa(client) + ":4321"
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec
	}
	if rec := export(1); rec.Code != http.StatusOK {
		t.Fatalf("first export: %d %s", rec.Code, rec.Body)
	}
	for i := range api.ExportReplaysMax {
		if rec := export(2 + i); rec.Code != http.StatusOK || rec.Header().Get("X-Idempotent-Replay") != "true" {
			t.Fatalf("replay %d: %d replay=%q", i+1, rec.Code, rec.Header().Get("X-Idempotent-Replay"))
		}
	}
	rec := export(10)
	if rec.Code != http.StatusTooManyRequests || !strings.Contains(rec.Body.String(), api.CodeExportRateLimited) {
		t.Fatalf("replay past the count: %d %s, want 429 %s", rec.Code, rec.Body, api.CodeExportRateLimited)
	}
}
