package apidserver_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/statetest"
)

// panickingAppsStore panics inside GET /v1/apps after auth has resolved
// the principal, the same place a nil dereference in a handler would.
type panickingAppsStore struct {
	Store state.Store
}

func (panickingAppsStore) ListApps(context.Context, string) ([]state.App, error) {
	panic("boom: nil dereference in listApps")
}

// TestV1PanicReturnsProblemAndCountsServerError pins issue #4385. A panic in
// a /v1 handler must come back as an RFC 7807 500 that carries the request
// ID, and observeWrap must still count it as a server error. Before the fix
// the panic unwound to net/http, which dropped the connection and skipped
// apid_ops_total and apid_request_total.
func TestV1PanicReturnsProblemAndCountsServerError(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	srv := apidserver.NewServer(statetest.NewStore(panickingAppsStore{Store: *e.Store()}),
		slog.New(slog.NewTextHandler(io.Discard, nil)), "gregale.dev", apidserver.NoopNotifier{}).
		WithOpsMetrics(context.Background(), *e.Ops())
	*e.H() = srv.Handler()

	rec := e.Do(t, http.MethodGet, "/v1/apps", nil, nil)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500; body=%s", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); !strings.Contains(ct, "application/problem+json") {
		t.Fatalf("content-type = %q, want application/problem+json", ct)
	}
	if rec.Header().Get("x-faas-request-id") == "" {
		t.Fatal("500 has no x-faas-request-id; RequestID must sit outside Recovery")
	}
	var problem api.Problem
	if err := json.Unmarshal(rec.Body.Bytes(), &problem); err != nil {
		t.Fatalf("decode problem: %v; body=%s", err, rec.Body.String())
	}
	if problem.Code != api.CodeInternal {
		t.Fatalf("code = %q, want %q", problem.Code, api.CodeInternal)
	}
	if strings.Contains(rec.Body.String(), "boom") {
		t.Fatalf("body leaked the panic value: %s", rec.Body.String())
	}

	metrics := httptest.NewRecorder()
	(*e.Ops()).Handler().ServeHTTP(metrics, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	body := metrics.Body.String()
	for _, want := range []string{
		`apid_test_ops_total{code="err",op="GET /v1/apps"} 1`,
		fmt.Sprintf(`apid_test_request_total{account_id=%q,code="err",route="GET /v1/apps"} 1`, e.Acct().ID),
	} {
		if !strings.Contains(body, want) {
			t.Errorf("metrics missing %q; panics must reach the 5xx counters", want)
		}
	}
}
