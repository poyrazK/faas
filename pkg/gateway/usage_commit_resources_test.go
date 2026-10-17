package gateway

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	authmw "github.com/onebox-faas/faas/pkg/auth/middleware"
	"github.com/onebox-faas/faas/pkg/usageoutbox"
)

// adr: 1308 — a consumer request's usage carries the response size it
// completes with and the runtime's reported execution time.
func TestUsageCommitRecordsComputeAndResponseSize(t *testing.T) {
	q, err := usageoutbox.Open(t.TempDir(), 4096)
	if err != nil {
		t.Fatal(err)
	}
	h := &Handler{usageOutbox: q, log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	app := uuid.New()
	r := withAppAndAccount(httptest.NewRequest(http.MethodGet, "/items", nil), uuid.New(), app)
	r = r.WithContext(authmw.WithConsumer(r.Context(), authmw.ConsumerIdentity{ID: uuid.NewString(), AppID: app.String()}))
	r = withGuestExecutionEvidence(r)
	holder := &usageCommitHolder{}
	r = r.WithContext(withUsageCommitHolder(r.Context(), holder))
	h.armUsageCommit(r, Target{})
	recordGuestExecutionEvidence(r.Context(), api.GuestEvidenceDurationHeader, "250")
	rec := &statusRecorder{ResponseWriter: newUsageProbe(q), status: http.StatusOK, request: r, usage: holder}
	holder.rec = rec

	rec.Header().Set("Content-Length", "10")
	rec.WriteHeader(http.StatusOK)
	for _, chunk := range []string{"hello", "world"} {
		if _, err := rec.Write([]byte(chunk)); err != nil {
			t.Fatal(err)
		}
	}
	item, ok, err := q.Next()
	if err != nil || !ok || item.Event.ResponseBytes != 10 || item.Event.ComputeMS != 250 {
		t.Fatalf("usage event = %+v, ok=%t err=%v; want 10 response bytes and 250 ms", item.Event, ok, err)
	}
}

// adr: 1308 — a chunked response is measured when the handler exits, and
// without a runtime duration compute falls back to time since forwarding.
func TestUsageCommitMeasuresChunkedResponseAtExit(t *testing.T) {
	f := newUsageCommitFixture(t, http.MethodGet, 4096, true)
	f.holder.rec = f.rec
	f.rec.WriteHeader(http.StatusOK)
	if _, err := f.rec.Write([]byte("streamed")); err != nil {
		t.Fatal(err)
	}
	if aborted := abortPanics(f.holder, f.rec); aborted {
		t.Fatal("recorded response aborted")
	}
	item, ok, err := f.q.Next()
	if err != nil || !ok || item.Event.ResponseBytes != 8 || item.Event.ComputeMS < 0 {
		t.Fatalf("usage event = %+v, ok=%t err=%v", item.Event, ok, err)
	}
}

// adr: 1310 — a tenant-surface request's usage carries its billing route,
// execution time and response size like a consumer request's.
func TestUsageCommitRecordsTenantSurfaceResources(t *testing.T) {
	q, err := usageoutbox.Open(t.TempDir(), 4096)
	if err != nil {
		t.Fatal(err)
	}
	h := &Handler{usageOutbox: q, log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	tenant, surface := uuid.NewString(), uuid.NewString()
	r := withAppAndAccount(httptest.NewRequest(http.MethodPost, "/generate", nil), uuid.New(), uuid.New())
	r = r.WithContext(withAuthenticated(r.Context(), Authenticated{PlatformTenantID: tenant, PlatformTenantSurfaceID: surface}))
	r = withBillingRoute(r, "POST /generate")
	r = withGuestExecutionEvidence(r)
	holder := &usageCommitHolder{}
	r = r.WithContext(withUsageCommitHolder(r.Context(), holder))
	h.armUsageCommit(r, Target{})
	recordGuestExecutionEvidence(r.Context(), api.GuestEvidenceDurationHeader, "1500")
	rec := &statusRecorder{ResponseWriter: newUsageProbe(q), status: http.StatusOK, request: r, usage: holder}
	holder.rec = rec
	rec.Header().Set("Content-Length", "4")
	rec.WriteHeader(http.StatusOK)
	if _, err := rec.Write([]byte("done")); err != nil {
		t.Fatal(err)
	}
	item, ok, err := q.Next()
	if err != nil || !ok || item.Event.PlatformTenantSurfaceID != surface || item.Event.BillingRoute != "POST /generate" ||
		item.Event.ComputeMS != 1500 || item.Event.ResponseBytes != 4 {
		t.Fatalf("usage event = %+v, ok=%t err=%v", item.Event, ok, err)
	}
}

// adr: 1310 — a JWT rule that attributes a tenant gets the billing route of
// the original path; other requests keep none.
func TestAdmitJWTTenantBillingRoute(t *testing.T) {
	h := &Handler{}
	app := uuid.NewString()
	r := withBillingRouteCandidate(httptest.NewRequest(http.MethodGet, "/rewritten", nil), "GET /items/{id}")
	if got := billingRouteFrom(h.admitJWTTenantBillingRoute(r, app)); got != "" {
		t.Fatalf("unattributed request route = %q, want none", got)
	}
	r = r.WithContext(withAuthenticated(r.Context(), Authenticated{PlatformTenantID: uuid.NewString(), PlatformTenantJWTAuthorizationRuleID: uuid.NewString()}))
	if got := billingRouteFrom(h.admitJWTTenantBillingRoute(r, app)); got != "GET /items/{id}" {
		t.Fatalf("JWT-rule request route = %q", got)
	}
}
