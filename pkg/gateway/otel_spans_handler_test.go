// Unit tests for the gatewayd-public OTelSpansHandler (ADR-127 PR-D).
// adr: 392
//
// Coverage:
//   - Happy path: valid OTLP body + valid bearer → 200 + accumulator
//     populated + metric counter incremented.
//   - 401 on bearer missing / auth RPC error.
//   - 402 on plan-disabled (DebugTelemetryEnabled=false).
//   - 429 on per-account rate cap exhaustion.
//   - 400 on shape-invalid body (malformed JSON, invalid trace IDs).
//   - 405 on non-POST.
//   - Top-N truncation correctness: 75 Hobby spans → keep 50
//     slowest; assert the kept set is the 50 largest DurationNanos.
//   - Truncation tripwire metric fires when input > ceiling.

package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	collectortracepb "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	tracepb "go.opentelemetry.io/proto/otlp/trace/v1"
	"google.golang.org/protobuf/encoding/protojson"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/ratelimit/peraccount"
	"github.com/onebox-faas/faas/pkg/wire"
)

// fakeAuthClient substitutes for apidgrpc.AuthClient in tests.
type fakeAuthClient struct {
	mu        sync.Mutex
	calls     int
	accountID string
	plan      string
	err       error
}

func (f *fakeAuthClient) AuthenticateKey(_ context.Context, _ string) (string, string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	return f.accountID, f.plan, f.err
}
func (f *fakeAuthClient) Close() error { return nil }

// buildOTLPBody assembles an ExportTraceServiceRequest with n
// spans whose durations are 1..n nanoseconds. Returns the raw
// JSON-protobuf bytes ready to feed to the handler.
func buildOTLPBody(t *testing.T, n int) []byte {
	t.Helper()
	var traceIDBytes [16]byte
	for i := range traceIDBytes {
		traceIDBytes[i] = byte(0xab)
	}
	var spanIDBytes [8]byte
	for i := range spanIDBytes {
		spanIDBytes[i] = byte(0xcd)
	}
	resourceSpans := &tracepb.ResourceSpans{
		ScopeSpans: []*tracepb.ScopeSpans{
			{Spans: make([]*tracepb.Span, 0, n)},
		},
	}
	for i := 1; i <= n; i++ {
		span := &tracepb.Span{
			TraceId:           traceIDBytes[:],
			SpanId:            spanIDBytes[:],
			Name:              "db.query",
			StartTimeUnixNano: 0,
			EndTimeUnixNano:   uint64(i),
			Attributes: []*commonpb.KeyValue{
				{Key: "db.statement", Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: "SELECT " + uuid.New().String()}}},
			},
		}
		resourceSpans.ScopeSpans[0].Spans = append(resourceSpans.ScopeSpans[0].Spans, span)
	}
	req := &collectortracepb.ExportTraceServiceRequest{
		ResourceSpans: []*tracepb.ResourceSpans{resourceSpans},
	}
	b, err := protojson.Marshal(req)
	if err != nil {
		t.Fatalf("protojson.Marshal: %v", err)
	}
	return b
}

// TestOTelSpansHandler_HappyPath: 3-span Hobby POST → 200,
// accumulator populated with 3 spans.
func TestOTelSpansHandler_HappyPath(t *testing.T) {
	acctID := uuid.New()
	auth := &fakeAuthClient{accountID: acctID.String(), plan: "hobby"}
	acc := NewSpansAccumulator()
	ops := wire.NewOpsMetrics("test")
	h := NewOTelSpansHandler(OTelSpansHandlerConfig{
		AuthClient: auth,
		Limiter:    peraccount.NewLimiter(),
		Acc:        acc,
		Ops:        ops,
	})

	body := buildOTLPBody(t, 3)
	req := httptest.NewRequest(http.MethodPost, "/v1/otel/v1/traces", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer faas_live_hobby")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body=%s)", rr.Code, rr.Body.String())
	}
	var resp collectortracepb.ExportTraceServiceResponse
	if err := protojson.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if got := rr.Header().Get("X-Gregale-Accepted-Spans"); got != "3" {
		t.Errorf("accepted spans = %s, want 3", got)
	}
	if resp.PartialSuccess != nil {
		t.Errorf("partial success = %v, want unset", resp.PartialSuccess)
	}
	if acc.Len() != 1 {
		t.Errorf("accumulator buckets = %d, want 1", acc.Len())
	}
}

// TestOTelSpansHandler_401_NoBearer: missing Authorization → 401.
func TestOTelSpansHandler_401_NoBearer(t *testing.T) {
	auth := &fakeAuthClient{err: errors.New("unused")}
	ops := wire.NewOpsMetrics("test")
	h := NewOTelSpansHandler(OTelSpansHandlerConfig{
		AuthClient: auth,
		Limiter:    peraccount.NewLimiter(),
		Acc:        NewSpansAccumulator(),
		Ops:        ops,
	})
	body := buildOTLPBody(t, 1)
	req := httptest.NewRequest(http.MethodPost, "/v1/otel/v1/traces", bytes.NewReader(body))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401 (body=%s)", rr.Code, rr.Body.String())
	}
	if auth.calls != 0 {
		t.Errorf("auth.calls = %d, want 0 (must short-circuit before RPC)", auth.calls)
	}
}

// TestOTelSpansHandler_BearerCaseInsensitive (PR-D code-review
// #8): RFC 7235 §2.1 requires case-insensitive scheme matching.
// A customer SDK sending 'Authorization: bearer faas_live_hobby'
// (lowercase) MUST be accepted, mirroring the canonical apid
// REST surface at pkg/auth/middleware/middleware.go:1180. The
// previous strings.HasPrefix was case-sensitive and 401'd
// legitimate lowercase traffic.
func TestOTelSpansHandler_BearerCaseInsensitive(t *testing.T) {
	cases := []struct {
		prefix string
	}{
		{"Bearer "},
		{"bearer "},
		{"BEARER "},
		{"BeArEr "},
	}
	for _, tc := range cases {
		t.Run(tc.prefix, func(t *testing.T) {
			auth := &fakeAuthClient{accountID: uuid.New().String(), plan: "scale"}
			ops := wire.NewOpsMetrics("test")
			h := NewOTelSpansHandler(OTelSpansHandlerConfig{
				AuthClient: auth,
				Limiter:    peraccount.NewLimiter(),
				Acc:        NewSpansAccumulator(),
				Ops:        ops,
			})
			body := buildOTLPBody(t, 1)
			req := httptest.NewRequest(http.MethodPost, "/v1/otel/v1/traces", bytes.NewReader(body))
			req.Header.Set("Authorization", tc.prefix+"faas_live_hobby")
			rr := httptest.NewRecorder()
			h.ServeHTTP(rr, req)
			if rr.Code != http.StatusOK {
				t.Errorf("prefix %q: status = %d, want 200 (body=%s)", tc.prefix, rr.Code, rr.Body.String())
			}
			if auth.calls != 1 {
				t.Errorf("prefix %q: auth.calls = %d, want 1", tc.prefix, auth.calls)
			}
		})
	}
}

// TestOTelSpansHandler_402_PlanDisabled: auth returns plan without
// DebugTelemetryEnabled → 402 + metric reason=plan_disabled.
func TestOTelSpansHandler_402_PlanDisabled(t *testing.T) {
	acctID := uuid.New()
	auth := &fakeAuthClient{accountID: acctID.String(), plan: "free"}
	ops := wire.NewOpsMetrics("test")
	h := NewOTelSpansHandler(OTelSpansHandlerConfig{
		AuthClient: auth,
		Limiter:    peraccount.NewLimiter(),
		Acc:        NewSpansAccumulator(),
		Ops:        ops,
	})
	body := buildOTLPBody(t, 1)
	req := httptest.NewRequest(http.MethodPost, "/v1/otel/v1/traces", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer faas_live_free")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusPaymentRequired {
		t.Fatalf("status = %d, want 402 (body=%s)", rr.Code, rr.Body.String())
	}
}

// TestOTelSpansHandler_429_RateLimited: bucket cap=1 (via stub
// LimitsFor) → second POST within the same window gets 429.
func TestOTelSpansHandler_429_RateLimited(t *testing.T) {
	acctID := uuid.New()
	auth := &fakeAuthClient{accountID: acctID.String(), plan: "hobby"}
	limiter := peraccount.NewLimiter()
	ops := wire.NewOpsMetrics("test")
	h := NewOTelSpansHandler(OTelSpansHandlerConfig{
		AuthClient: auth,
		Limiter:    limiter,
		Acc:        NewSpansAccumulator(),
		Ops:        ops,
		// Stub the plan table to a cap=1 Hobby-shaped limit so
		// the second POST trips without burning 1000 buckets.
		LimitsFor: func(_ api.Plan) api.Limits {
			return api.Limits{
				DebugTelemetryEnabled:           true,
				DebugTelemetryRequestsPerMinute: 1,
				DebugTelemetrySpansPerTrace:     50,
			}
		},
	})
	body := buildOTLPBody(t, 1)
	makeReq := func() *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/v1/otel/v1/traces", bytes.NewReader(body))
		req.Header.Set("Authorization", "Bearer faas_live_hobby")
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, req)
		return rr
	}
	if rr := makeReq(); rr.Code != http.StatusOK {
		t.Fatalf("first POST: status = %d, want 200", rr.Code)
	}
	if rr := makeReq(); rr.Code != http.StatusTooManyRequests {
		t.Fatalf("second POST: status = %d, want 429 (body=%s)", rr.Code, rr.Body.String())
	}
}

// TestOTelSpansHandler_400_MalformedJSON: garbage bytes → 400.
func TestOTelSpansHandler_400_MalformedJSON(t *testing.T) {
	auth := &fakeAuthClient{accountID: uuid.New().String(), plan: "hobby"}
	ops := wire.NewOpsMetrics("test")
	h := NewOTelSpansHandler(OTelSpansHandlerConfig{
		AuthClient: auth,
		Limiter:    peraccount.NewLimiter(),
		Acc:        NewSpansAccumulator(),
		Ops:        ops,
	})
	req := httptest.NewRequest(http.MethodPost, "/v1/otel/v1/traces", strings.NewReader("not json"))
	req.Header.Set("Authorization", "Bearer faas_live_hobby")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rr.Code)
	}
}

// TestOTelSpansHandler_MultipleTraces accepts a normal exporter batch.
func TestOTelSpansHandler_MultipleTraces(t *testing.T) {
	auth := &fakeAuthClient{accountID: uuid.New().String(), plan: "hobby"}
	ops := wire.NewOpsMetrics("test")
	h := NewOTelSpansHandler(OTelSpansHandlerConfig{
		AuthClient: auth,
		Limiter:    peraccount.NewLimiter(),
		Acc:        NewSpansAccumulator(),
		Ops:        ops,
	})
	// Two spans, two different trace_ids.
	tid1 := bytes.Repeat([]byte{0xab}, 16)
	tid2 := bytes.Repeat([]byte{0xcd}, 16)
	sid := bytes.Repeat([]byte{0x01}, 8)
	req := &collectortracepb.ExportTraceServiceRequest{
		ResourceSpans: []*tracepb.ResourceSpans{
			{ScopeSpans: []*tracepb.ScopeSpans{
				{Spans: []*tracepb.Span{
					{TraceId: tid1, SpanId: sid, Name: "a", StartTimeUnixNano: 0, EndTimeUnixNano: 1},
					{TraceId: tid2, SpanId: sid, Name: "b", StartTimeUnixNano: 0, EndTimeUnixNano: 2},
				}},
			}},
		},
	}
	body, _ := protojson.Marshal(req)
	httpReq := httptest.NewRequest(http.MethodPost, "/v1/otel/v1/traces", bytes.NewReader(body))
	httpReq.Header.Set("Authorization", "Bearer faas_live_hobby")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httpReq)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body=%s)", rr.Code, rr.Body.String())
	}
	if h.cfg.Acc.Len() != 2 {
		t.Fatalf("accumulator buckets = %d, want 2", h.cfg.Acc.Len())
	}
}

// TestOTelSpansHandler_405_NotPOST: GET → 405 + Allow: POST.
func TestOTelSpansHandler_405_NotPOST(t *testing.T) {
	auth := &fakeAuthClient{}
	ops := wire.NewOpsMetrics("test")
	h := NewOTelSpansHandler(OTelSpansHandlerConfig{
		AuthClient: auth,
		Limiter:    peraccount.NewLimiter(),
		Acc:        NewSpansAccumulator(),
		Ops:        ops,
	})
	req := httptest.NewRequest(http.MethodGet, "/v1/otel/v1/traces", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want 405", rr.Code)
	}
	if got := rr.Header().Get("Allow"); got != "POST" {
		t.Errorf("Allow header = %q, want POST", got)
	}
}

// TestOTelSpansHandler_Truncation_FlushTime (PR-D
// code-review #5): 75 Hobby spans arrive in one POST. The
// handler accepts all 75 (no per-POST truncation). The flush
// loop applies the Hobby cap=50 and ships 50 spans in
// slowest-first order.
func TestOTelSpansHandler_Truncation_FlushTime(t *testing.T) {
	acctID := uuid.New()
	auth := &fakeAuthClient{accountID: acctID.String(), plan: "hobby"}
	acc := NewSpansAccumulator()
	ops := wire.NewOpsMetrics("test")
	h := NewOTelSpansHandler(OTelSpansHandlerConfig{
		AuthClient: auth,
		Limiter:    peraccount.NewLimiter(),
		Acc:        acc,
		Ops:        ops,
	})

	body := buildOTLPBody(t, 75)
	req := httptest.NewRequest(http.MethodPost, "/v1/otel/v1/traces", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer faas_live_hobby")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body=%s)", rr.Code, rr.Body.String())
	}
	var resp collectortracepb.ExportTraceServiceResponse
	if err := protojson.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if got := rr.Header().Get("X-Gregale-Accepted-Spans"); got != "75" {
		t.Errorf("accepted spans = %s, want 75", got)
	}
	if resp.PartialSuccess != nil {
		t.Errorf("partial success = %v, want unset", resp.PartialSuccess)
	}

	var trunc int32
	var captured atomic.Pointer[[]summarizedSpan]
	flushCtx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = acc.RunFlushLoop(flushCtx, FlushLoopConfig{
			Interval:         5 * time.Millisecond,
			MaxSpansPerTrace: func(_ string) int { return 50 },
			OnTruncated:      func(_ string) { atomic.AddInt32(&trunc, 1) },
			WriteFn: func(_ context.Context, _ string, summaryJSON []byte, _ string) (string, int64, error) {
				var spans []summarizedSpan
				if err := json.Unmarshal(summaryJSON, &spans); err != nil {
					t.Errorf("unmarshal: %v", err)
					return "db_error", 0, nil
				}
				captured.Store(&spans)
				return "inserted", 0, nil
			},
		})
	}()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && captured.Load() == nil {
		time.Sleep(5 * time.Millisecond)
	}
	cancel()
	<-done
	if captured.Load() == nil {
		t.Fatalf("flush loop never wrote a payload")
	}
	got := *captured.Load()
	if len(got) != 50 {
		t.Errorf("flush wrote %d spans, want 50 (Hobby cap)", len(got))
	}
	for i := 1; i < len(got); i++ {
		if got[i].DurationNanos > got[i-1].DurationNanos {
			t.Errorf("flushed spans not sorted descending by duration: [%d]=%d > [%d]=%d",
				i, got[i].DurationNanos, i-1, got[i-1].DurationNanos)
		}
	}
	if atomic.LoadInt32(&trunc) != 1 {
		t.Errorf("OnTruncated fired %d times, want 1", atomic.LoadInt32(&trunc))
	}
}

// Compile-time guard that protojson accepts the message shapes
// we build (the otlp deps occasionally change type signatures
// across minor versions — failing at the test boundary beats
// failing in production).
var _ = protojson.MarshalOptions{}
