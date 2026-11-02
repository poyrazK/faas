package gateway

// Tracing tenancy (2026-10-31 logs/traces audit, T4/T5): producers name the
// rows their spans merge into, and the OTLP endpoint answers apid's key
// decisions.

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel/attribute"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	oteltrace "go.opentelemetry.io/otel/trace"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/onebox-faas/faas/pkg/ratelimit/peraccount"
	"github.com/onebox-faas/faas/pkg/wire"
)

func TestSpansTargetsBufferAndFlushApart(t *testing.T) {
	const tid = "0000000000000000000000000000000b"
	acc := NewSpansAccumulator()
	account := uuid.New()
	first := SpanTarget{AccountID: account, AppID: uuid.New()}
	second := SpanTarget{AccountID: account, AppID: uuid.New()}
	if _, err := acc.AddForTarget(tid, first, "", []summarizedSpan{noRowTestSpan("a", 10)}); err != nil {
		t.Fatal(err)
	}
	if _, err := acc.AddForTarget(tid, second, "", []summarizedSpan{noRowTestSpan("b", 20)}); err != nil {
		t.Fatal(err)
	}
	if acc.Len() != 2 {
		t.Fatalf("buffered traces = %d, want one per app", acc.Len())
	}
	written := map[SpanTarget]int{}
	acc.drainOnce(context.Background(), FlushLoopConfig{
		Log:        slog.Default(),
		MaxRetries: 3,
		WriteTargetFn: func(_ context.Context, traceID string, _ []byte, target SpanTarget) (string, int64, error) {
			if traceID != tid {
				t.Errorf("trace = %s", traceID)
			}
			written[target]++
			return "inserted", 0, nil
		},
	}, map[spanBucketKey]*pendingEntry{})
	if written[first] != 1 || written[second] != 1 || len(written) != 2 {
		t.Fatalf("writes = %v, want one per app target", written)
	}
}

func TestSpansNarrowedTargetNeverWidensThroughWriteFn(t *testing.T) {
	const tid = "0000000000000000000000000000000c"
	acc := NewSpansAccumulator()
	if _, err := acc.AddForTarget(tid, SpanTarget{AccountID: uuid.New(), OrgID: uuid.New(), CustomerCredential: true}, "", []summarizedSpan{noRowTestSpan("a", 10)}); err != nil {
		t.Fatal(err)
	}
	calls := 0
	cfg := FlushLoopConfig{
		Log:        slog.Default(),
		MaxRetries: 1,
		WriteFn: func(context.Context, string, []byte, string) (string, int64, error) {
			calls++
			return "inserted", 0, nil
		},
	}
	pending := map[spanBucketKey]*pendingEntry{}
	acc.drainOnce(context.Background(), cfg, pending)
	acc.drainOnce(context.Background(), cfg, pending)
	if calls != 0 || len(pending) != 0 {
		t.Fatalf("account-only writer called %d times, pending %d; want the narrowed trace dropped unwritten", calls, len(pending))
	}
}

func TestSpansTargetRequest(t *testing.T) {
	target := SpanTarget{AccountID: uuid.New(), AppID: uuid.New(), OrgID: uuid.New(), CustomerCredential: true}
	req := target.Request("0000000000000000000000000000000d", []byte(`[]`))
	if req.GetAccountId() != target.AccountID.String() || req.GetAppId() != target.AppID.String() ||
		req.GetOrgId() != target.OrgID.String() || !req.GetCustomerCredential() || string(req.GetSummaryJson()) != "[]" {
		t.Fatalf("request = %+v", req)
	}
	plain := SpanTarget{AccountID: target.AccountID}.Request("0000000000000000000000000000000d", nil)
	if plain.GetAppId() != "" || plain.GetOrgId() != "" || plain.GetCustomerCredential() {
		t.Fatalf("account-wide request = %+v, want no narrowing", plain)
	}
}

func TestSpansRetainedDependencyTargetsTheCallingApp(t *testing.T) {
	accountID, caller := uuid.New(), uuid.New()
	acc := NewSpansAccumulator()
	provider := sdktrace.NewTracerProvider(
		sdktrace.WithSyncer(NewRetainedServiceSpansExporter(acc, nil)),
		sdktrace.WithSampler(sdktrace.AlwaysSample()),
	)
	t.Cleanup(func() { _ = provider.Shutdown(context.Background()) })
	_, span := provider.Tracer("test").Start(context.Background(), "service.orders",
		oteltrace.WithAttributes(
			attribute.String("gregale.dependency.type", "managed_binding"),
			attribute.String("gregale.dependency.kind", "service_proxy"),
			attribute.String(retainedSpanAccountIDAttribute, accountID.String()),
			attribute.String("gregale.service.caller_app_id", caller.String()),
		),
	)
	span.End()
	traceID := span.SpanContext().TraceID().String()
	if spans, _ := acc.DrainAndRemove(traceID, accountID); len(spans) != 0 {
		t.Fatal("the caller's span was buffered account-wide")
	}
	if spans, _ := acc.DrainAndRemoveTarget(traceID, SpanTarget{AccountID: accountID, AppID: caller}); len(spans) != 1 {
		t.Fatalf("spans for the calling app = %d, want 1", len(spans))
	}
}

func TestSpansOTLPHandlerAnswersKeyDecisions(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
		want int
	}{
		{"read-only key", status.Error(codes.PermissionDenied, "scope"), http.StatusForbidden},
		{"tracing not released", status.Error(codes.NotFound, "released"), http.StatusNotFound},
		{"unknown key", status.Error(codes.Unauthenticated, "unknown"), http.StatusUnauthorized},
		{"apid down", errors.New("dial"), http.StatusInternalServerError},
	} {
		t.Run(tc.name, func(t *testing.T) {
			acc := NewSpansAccumulator()
			h := NewOTelSpansHandler(OTelSpansHandlerConfig{
				AuthClient: &fakeAuthClient{err: tc.err},
				Limiter:    peraccount.NewLimiter(),
				Acc:        acc,
				Ops:        wire.NewOpsMetrics("test"),
			})
			req := httptest.NewRequest(http.MethodPost, "/v1/otel/v1/traces", bytes.NewReader(buildOTLPBody(t, 1)))
			req.Header.Set("Authorization", "Bearer faas_live_key")
			rr := httptest.NewRecorder()
			h.ServeHTTP(rr, req)
			if rr.Code != tc.want || acc.Len() != 0 {
				t.Fatalf("status = %d (buffered %d), want %d", rr.Code, acc.Len(), tc.want)
			}
		})
	}
}

func TestSpansOTLPHandlerBuffersForTheKeysTarget(t *testing.T) {
	holder, app, org := uuid.New(), uuid.New(), uuid.New()
	for _, tc := range []struct {
		name string
		auth *fakeAuthClient
		want SpanTarget
	}{
		{"personal key", &fakeAuthClient{accountID: holder.String(), plan: "pro"}, SpanTarget{AccountID: holder, CustomerCredential: true}},
		{"company key", &fakeAuthClient{accountID: holder.String(), plan: "pro", orgID: org.String()}, SpanTarget{AccountID: holder, OrgID: org, CustomerCredential: true}},
		{"app-bound key", &fakeAuthClient{accountID: holder.String(), plan: "pro", appID: app.String()}, SpanTarget{AccountID: holder, AppID: app, CustomerCredential: true}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			acc := NewSpansAccumulator()
			h := NewOTelSpansHandler(OTelSpansHandlerConfig{AuthClient: tc.auth, Limiter: peraccount.NewLimiter(), Acc: acc, Ops: wire.NewOpsMetrics("test")})
			req := httptest.NewRequest(http.MethodPost, "/v1/otel/v1/traces", bytes.NewReader(buildOTLPBody(t, 2)))
			req.Header.Set("Authorization", "Bearer faas_live_key")
			rr := httptest.NewRecorder()
			h.ServeHTTP(rr, req)
			if rr.Code != http.StatusOK {
				t.Fatalf("status = %d (%s)", rr.Code, rr.Body.String())
			}
			// buildOTLPBody's spans share one span ID, so they dedupe to one.
			if spans, _ := acc.DrainAndRemoveTarget("abababababababababababababababab", tc.want); len(spans) == 0 {
				t.Fatalf("no spans buffered for target %+v", tc.want)
			}
		})
	}
	bad := NewOTelSpansHandler(OTelSpansHandlerConfig{
		AuthClient: &fakeAuthClient{accountID: holder.String(), plan: "pro", orgID: "not-a-uuid"},
		Limiter:    peraccount.NewLimiter(), Acc: NewSpansAccumulator(), Ops: wire.NewOpsMetrics("test"),
	})
	req := httptest.NewRequest(http.MethodPost, "/v1/otel/v1/traces", bytes.NewReader(buildOTLPBody(t, 1)))
	req.Header.Set("Authorization", "Bearer faas_live_key")
	rr := httptest.NewRecorder()
	bad.ServeHTTP(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("malformed organization from apid: status = %d, want 401", rr.Code)
	}
}
