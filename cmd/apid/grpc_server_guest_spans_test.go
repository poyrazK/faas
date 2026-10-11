package main

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	apidpb "github.com/onebox-faas/faas/api/proto/onebox/faas/apid/v1"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/ratelimit/peraccount"
	"github.com/onebox-faas/faas/pkg/state"
	collectortracepb "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	tracepb "go.opentelemetry.io/proto/otlp/trace/v1"
	"google.golang.org/protobuf/proto"
)

type fakeGuestSpansAccounts struct {
	mu    sync.Mutex
	plans map[string]api.Plan
	reads int
}

func (f *fakeGuestSpansAccounts) AccountByID(_ context.Context, id string) (state.Account, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.reads++
	plan, ok := f.plans[id]
	if !ok {
		return state.Account{}, state.ErrNotFound
	}
	return state.Account{ID: id, Plan: plan}, nil
}

func guestSpansPayload(t *testing.T, traceByte byte) []byte {
	t.Helper()
	tid := make([]byte, 16)
	tid[15] = traceByte
	body, err := proto.Marshal(&collectortracepb.ExportTraceServiceRequest{ResourceSpans: []*tracepb.ResourceSpans{{ScopeSpans: []*tracepb.ScopeSpans{{Spans: []*tracepb.Span{
		{TraceId: tid, SpanId: []byte{0, 0, 0, 0, 0, 0, 0, 1}, Name: "SELECT orders", StartTimeUnixNano: 1, EndTimeUnixNano: 191_000_001},
	}}}}}})
	if err != nil {
		t.Fatal(err)
	}
	return body
}

func guestSpansRequest(account uuid.UUID, codec byte, payload []byte) *apidpb.IngestGuestSpansRequest {
	return &apidpb.IngestGuestSpansRequest{AccountId: account.String(), AppId: uuid.NewString(), DeploymentId: uuid.NewString(), InstanceId: "i-1", Codec: uint32(codec), Payload: payload}
}

func TestGuestSpansIngestOutcomes(t *testing.T) {
	pro, free := uuid.New(), uuid.New()
	accounts := &fakeGuestSpansAccounts{plans: map[string]api.Plan{pro.String(): api.PlanPro, free.String(): api.PlanFree}}
	g := newGuestSpansIngester(accounts, peraccount.NewLimiter(), discardLogger())
	ctx := context.Background()

	for _, tc := range []struct {
		name string
		req  *apidpb.IngestGuestSpansRequest
		want string
	}{
		{name: "accepted", req: guestSpansRequest(pro, api.TraceCodecProtobuf, guestSpansPayload(t, 1)), want: guestSpansAccepted},
		{name: "plan disabled", req: guestSpansRequest(free, api.TraceCodecProtobuf, guestSpansPayload(t, 2)), want: guestSpansDisabled},
		{name: "unknown account", req: guestSpansRequest(uuid.New(), api.TraceCodecProtobuf, guestSpansPayload(t, 3)), want: guestSpansDisabled},
		{name: "bad account", req: &apidpb.IngestGuestSpansRequest{AccountId: "nope", AppId: "a", DeploymentId: "d", Codec: uint32(api.TraceCodecProtobuf), Payload: []byte("x")}, want: guestSpansInvalid},
		{name: "missing app", req: &apidpb.IngestGuestSpansRequest{AccountId: pro.String(), DeploymentId: "d", Codec: uint32(api.TraceCodecProtobuf), Payload: []byte("x")}, want: guestSpansInvalid},
		{name: "unknown codec", req: guestSpansRequest(pro, 0x7f, guestSpansPayload(t, 4)), want: guestSpansInvalid},
		{name: "oversized codec", req: &apidpb.IngestGuestSpansRequest{AccountId: pro.String(), AppId: "a", DeploymentId: "d", Codec: 0x101, Payload: []byte("x")}, want: guestSpansInvalid},
		{name: "empty payload", req: guestSpansRequest(pro, api.TraceCodecProtobuf, nil), want: guestSpansInvalid},
		{name: "oversized payload", req: guestSpansRequest(pro, api.TraceCodecProtobuf, make([]byte, api.TraceMaxFrameBytes+1)), want: guestSpansInvalid},
		{name: "undecodable", req: guestSpansRequest(pro, api.TraceCodecProtobuf, []byte{0xff, 0xff}), want: guestSpansInvalid},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := g.ingest(ctx, tc.req).GetOutcome(); got != tc.want {
				t.Fatalf("outcome = %q, want %q", got, tc.want)
			}
		})
	}
	if g.acc.Len() != 1 {
		t.Fatalf("buffered traces = %d, want 1", g.acc.Len())
	}
	reads := accounts.reads
	g.ingest(ctx, guestSpansRequest(pro, api.TraceCodecProtobuf, guestSpansPayload(t, 5)))
	if accounts.reads != reads {
		t.Fatal("cached plan limits were not reused")
	}
}

func TestGuestSpansIngestRateLimited(t *testing.T) {
	acct := uuid.New()
	accounts := &fakeGuestSpansAccounts{plans: map[string]api.Plan{acct.String(): api.PlanHobby}}
	limiter := peraccount.NewLimiter()
	g := newGuestSpansIngester(accounts, limiter, discardLogger())
	cap := api.MustLimitsFor(api.PlanHobby).DebugTelemetryRequestsPerMinute
	for i := 0; i < cap; i++ {
		limiter.Take(acct, cap)
	}
	resp := g.ingest(context.Background(), guestSpansRequest(acct, api.TraceCodecProtobuf, guestSpansPayload(t, 1)))
	if resp.GetOutcome() != guestSpansRateLimited || resp.GetRetryAfterMs() <= 0 {
		t.Fatalf("response = %+v, want rate_limited with retry", resp)
	}
}

func TestGuestSpansReceiverWithoutIngesterIsUnavailable(t *testing.T) {
	r := newSpansWriterReceiver(&fakeSpansWriterStore{}, nil, nil, true)
	resp, err := r.IngestGuestSpans(context.Background(), guestSpansRequest(uuid.New(), api.TraceCodecProtobuf, []byte("x")))
	if err != nil || resp.GetOutcome() != guestSpansUnavailable {
		t.Fatalf("resp=%+v err=%v", resp, err)
	}
}

func TestGuestSpansFlushWritesThroughSpansWriter(t *testing.T) {
	acct := uuid.New()
	accounts := &fakeGuestSpansAccounts{plans: map[string]api.Plan{acct.String(): api.PlanPro}}
	store := &fakeSpansWriterStore{}
	written := make(chan []byte, 1)
	store.updateFn = func(traceID string, accountID uuid.UUID, summary []byte) error {
		if traceID != "00000000000000000000000000000007" || accountID != acct {
			t.Errorf("write for trace=%s account=%s", traceID, accountID)
		}
		written <- summary
		return nil
	}
	limiter := peraccount.NewLimiter()
	g := newGuestSpansIngester(accounts, limiter, discardLogger())
	writer := newSpansWriterReceiver(store, nil, limiter, true)
	writer.guest = g
	if resp, _ := writer.IngestGuestSpans(context.Background(), guestSpansRequest(acct, api.TraceCodecProtobuf, guestSpansPayload(t, 7))); resp.GetOutcome() != guestSpansAccepted {
		t.Fatalf("ingest outcome = %q", resp.GetOutcome())
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = g.run(ctx, 10*time.Millisecond, writer.WriteSpansSummary) }()
	select {
	case summary := <-written:
		var decoded any
		if err := json.Unmarshal(summary, &decoded); err != nil {
			t.Fatalf("summary is not JSON: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("flush loop did not write the guest spans")
	}
}

func TestOTelFlushInterval(t *testing.T) {
	env := map[string]string{}
	getenv := func(k string) string { return env[k] }
	if got := otelFlushInterval(getenv); got != 30*time.Second {
		t.Fatalf("default = %v", got)
	}
	env["FAAS_OTEL_FLUSH_INTERVAL"] = "5s"
	if got := otelFlushInterval(getenv); got != 5*time.Second {
		t.Fatalf("override = %v", got)
	}
	env["FAAS_OTEL_FLUSH_INTERVAL"] = "-1s"
	if got := otelFlushInterval(getenv); got != 30*time.Second {
		t.Fatalf("invalid override = %v", got)
	}
}
