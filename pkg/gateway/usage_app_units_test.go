package gateway

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
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
	h := &Handler{usageOutbox: q, apiMonetization: monetizationOn, log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	app := uuid.New()
	r := withAppAndAccount(httptest.NewRequest(http.MethodPost, "/generate", nil), uuid.New(), app)
	r = r.WithContext(authmw.WithConsumer(r.Context(), authmw.ConsumerIdentity{ID: uuid.NewString(), AppID: app.String()}))
	r = withGuestExecutionEvidence(r, true)
	holder := &usageCommitHolder{}
	r = r.WithContext(withUsageCommitHolder(r.Context(), holder))
	h.armUsageCommit(r, Target{})
	dst := http.Header{}
	forwardedResponseHeader(r.Context(), dst, "Gregale-Usage", "output_tokens__standard=10, output_tokens__premium=2, bad, Upper=1, output_tokens__standard=5")
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
	if err != nil || !ok || item.Event.MeterUnits["output_tokens__standard"] != 15 || item.Event.MeterUnits["output_tokens__premium"] != 2 || len(item.Event.MeterUnits) != 2 {
		t.Fatalf("usage event = %+v, ok=%t err=%v", item.Event, ok, err)
	}
}

// adr: 1318 — on the reverse-proxy path a Gregale-Usage trailer is recorded
// when the body ends and removed before trailers reach the client.
func TestUsageTrailerRecordedAtBodyEnd(t *testing.T) {
	r := withGuestExecutionEvidence(httptest.NewRequest(http.MethodGet, "/stream", nil), true)
	resp := &http.Response{Header: http.Header{}, Body: io.NopCloser(strings.NewReader("data")),
		Trailer: http.Header{"Gregale-Usage": {"output_tokens__premium=42"}}, Request: r}
	stripGuestEvidenceResponseHeaders(resp)
	if _, err := io.ReadAll(resp.Body); err != nil {
		t.Fatal(err)
	}
	if resp.Trailer.Get("Gregale-Usage") != "" {
		t.Fatal("trailer reached the client")
	}
	if units := guestExecutionEvidenceOf(r).meterUnits(); units["output_tokens__premium"] != 42 {
		t.Fatalf("meter units = %v", units)
	}
}

// adr: 1318 — model-qualified app meters remain distinct in the usage ledger.
func TestGenerateRecordsBothModelMeters(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/generate" {
			http.NotFound(w, r)
			return
		}
		switch r.URL.Query().Get("model") {
		case "standard":
			w.Header().Set("Gregale-Usage", "output_tokens__standard=100")
		case "premium":
			w.Header().Set("Gregale-Usage", "output_tokens__premium=120")
		default:
			http.Error(w, "unknown model", http.StatusBadRequest)
			return
		}
		_, _ = io.WriteString(w, "ok")
	}))
	t.Cleanup(upstream.Close)

	accountID, appID, consumerID := uuid.NewString(), uuid.NewString(), uuid.NewString()
	store, token := consumerAuthFixture()
	store.key.AccountID, store.key.AppID, store.key.ConsumerID = accountID, appID, consumerID
	store.consumer.AccountID, store.consumer.AppID, store.consumer.ID = accountID, appID, consumerID
	address := upstream.Listener.Addr().String()
	backend := &fakeBackend{app: App{ID: appID, AccountID: accountID, Plan: api.PlanPro,
		ConsumerAuthMode: api.ConsumerAuthModeRequired}, host: "metered.example", upstream: address}
	backend.AddTarget(Target{NodeID: address, InstanceID: uuid.NewString()})
	outbox, err := usageoutbox.Open(t.TempDir(), 4096)
	if err != nil {
		t.Fatal(err)
	}
	h := NewHandlerWith(backend, NewMetrics(), nil).WithConsumerAuth(store).WithAPIMonetization(monetizationOn)
	h.WithUsageOutbox(outbox)
	h.requestTelemetry = makeTestRecorder()

	for _, tc := range []struct {
		model string
		units int64
	}{{"standard", 100}, {"premium", 120}} {
		request := httptest.NewRequest(http.MethodPost, "http://metered.example/generate?model="+tc.model, nil)
		request.Header.Set("Authorization", "Bearer "+token)
		response := httptest.NewRecorder()
		h.ServeHTTP(response, request)
		if response.Code != http.StatusOK || response.Header().Get("Gregale-Usage") != "" {
			t.Fatalf("generate %s: status=%d headers=%v", tc.model, response.Code, response.Header())
		}
		item, ok, err := outbox.Next()
		if err != nil || !ok || item.Event.ConsumerID != consumerID || item.Event.BillingRoute != "POST /generate" ||
			item.Event.MeterUnits["output_tokens__"+tc.model] != tc.units || len(item.Event.MeterUnits) != 1 {
			t.Fatalf("generate %s usage: %+v (found=%t, err=%v)", tc.model, item.Event, ok, err)
		}
		if err := outbox.Ack(item); err != nil {
			t.Fatal(err)
		}
	}
}
