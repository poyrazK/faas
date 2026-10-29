package apidserver_test

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
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
