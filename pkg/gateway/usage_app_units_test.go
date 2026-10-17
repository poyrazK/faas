package gateway

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"

	authmw "github.com/onebox-faas/faas/pkg/auth/middleware"
	"github.com/onebox-faas/faas/pkg/usageoutbox"
)

// adr: 1318 — a consumer request's usage carries the units the app reported
// in Gregale-Usage; repeated meters add up and malformed pairs are dropped.
func TestUsageCommitRecordsAppReportedUnits(t *testing.T) {
	q, err := usageoutbox.Open(t.TempDir(), 4096)
	if err != nil {
		t.Fatal(err)
	}
	h := &Handler{usageOutbox: q, log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	app := uuid.New()
	r := withAppAndAccount(httptest.NewRequest(http.MethodPost, "/generate", nil), uuid.New(), app)
	r = r.WithContext(authmw.WithConsumer(r.Context(), authmw.ConsumerIdentity{ID: uuid.NewString(), AppID: app.String()}))
	r = withGuestExecutionEvidence(r)
	holder := &usageCommitHolder{}
	r = r.WithContext(withUsageCommitHolder(r.Context(), holder))
	h.armUsageCommit(r, Target{})
	dst := http.Header{}
	forwardedResponseHeader(r.Context(), dst, "Gregale-Usage", "tokens=10, images=2, bad, Upper=1, tokens=5")
	if dst.Get("Gregale-Usage") != "" {
		t.Fatal("Gregale-Usage reached the client")
	}
	rec := &statusRecorder{ResponseWriter: newUsageProbe(q), status: http.StatusOK, request: r, usage: holder}
	holder.rec = rec
	rec.Header().Set("Content-Length", "2")
	rec.WriteHeader(http.StatusOK)
	if _, err := rec.Write([]byte("ok")); err != nil {
		t.Fatal(err)
	}
	item, ok, err := q.Next()
	if err != nil || !ok || item.Event.MeterUnits["tokens"] != 15 || item.Event.MeterUnits["images"] != 2 || len(item.Event.MeterUnits) != 2 {
		t.Fatalf("usage event = %+v, ok=%t err=%v", item.Event, ok, err)
	}
}

// adr: 1318 — on the reverse-proxy path a Gregale-Usage trailer is recorded
// when the body ends and removed before trailers reach the client.
func TestUsageTrailerRecordedAtBodyEnd(t *testing.T) {
	r := withGuestExecutionEvidence(httptest.NewRequest(http.MethodGet, "/stream", nil))
	resp := &http.Response{Header: http.Header{}, Body: io.NopCloser(strings.NewReader("data")),
		Trailer: http.Header{"Gregale-Usage": {"tokens=42"}}, Request: r}
	stripGuestEvidenceResponseHeaders(resp)
	if _, err := io.ReadAll(resp.Body); err != nil {
		t.Fatal(err)
	}
	if resp.Trailer.Get("Gregale-Usage") != "" {
		t.Fatal("trailer reached the client")
	}
	if units := guestExecutionEvidenceOf(r).meterUnits(); units["tokens"] != 42 {
		t.Fatalf("meter units = %v", units)
	}
}
