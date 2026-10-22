package apidserver_test

// adr: 20261020214632

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
)

// The API request a serving apid sends its successor before the hand-off is
// answered as the check expects by apid's real handler: without credentials,
// through routing, middleware and authentication.
func TestHandoffAPICheckGetsWhatItWants(t *testing.T) {
	checks := apidserver.HandoffChecks("127.0.0.1:8081", "127.0.0.1:9101")
	if len(checks) != 2 || checks[0].Name != "readyz" || checks[0].Address != "127.0.0.1:9101" || checks[0].Path != "/readyz" {
		t.Fatalf("checks = %+v, want readyz on the operator listener first", checks)
	}
	apiCheck := checks[1]
	if apiCheck.Address != "127.0.0.1:8081" {
		t.Fatalf("api check goes to %s, want the public listener", apiCheck.Address)
	}
	store := state.NewMemStore()
	if _, err := store.CreateAccount(context.Background(), "alice@example.com", api.PlanPro); err != nil {
		t.Fatal(err)
	}
	handler := apidserver.NewServer(store, slog.New(slog.NewTextHandler(io.Discard, nil)), "gregale.dev", apidserver.NoopNotifier{}).Handler()
	req := httptest.NewRequest(http.MethodGet, "http://"+apiCheck.Host+apiCheck.Path, nil)
	req.RemoteAddr = "127.0.0.1:0" // what a check connection reports
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if !slices.Contains(apiCheck.Want, rec.Code) {
		t.Fatalf("apid answered the check with %d (%s), want one of %v", rec.Code, rec.Body.String(), apiCheck.Want)
	}

	if got := apidserver.HandoffChecks("127.0.0.1:8081", ""); len(got) != 1 || got[0].Name != "api" {
		t.Fatalf("without an operator listener, checks = %+v, want the API request only", got)
	}
}
