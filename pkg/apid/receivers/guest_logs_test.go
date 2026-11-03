package receivers

// adr: 213 — runtime adapter: guest OTLP logs become redacted, bounded,
// trace-correlated log_events rows.

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	collectorlogspb "go.opentelemetry.io/proto/otlp/collector/logs/v1"
	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	logspb "go.opentelemetry.io/proto/otlp/logs/v1"
	"google.golang.org/protobuf/proto"

	apidpb "github.com/onebox-faas/faas/api/proto/onebox/faas/apid/v1"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/ratelimit/peraccount"
	"github.com/onebox-faas/faas/pkg/state"
)

var guestLogTraceID = []byte{0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 9}

func guestLogRecord(body string, severity logspb.SeverityNumber, traceID []byte) *logspb.LogRecord {
	return &logspb.LogRecord{
		TimeUnixNano:   uint64(time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC).UnixNano()),
		SeverityNumber: severity,
		Body:           &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: body}},
		TraceId:        traceID,
		SpanId:         []byte{0, 0, 0, 0, 0, 0, 0, 7},
	}
}

func guestLogsPayload(t *testing.T, records ...*logspb.LogRecord) []byte {
	t.Helper()
	body, err := proto.Marshal(&collectorlogspb.ExportLogsServiceRequest{ResourceLogs: []*logspb.ResourceLogs{{ScopeLogs: []*logspb.ScopeLogs{{LogRecords: records}}}}})
	if err != nil {
		t.Fatal(err)
	}
	return body
}

func newGuestLogsTest(t *testing.T) (*GuestSpansIngester, *state.MemStore, uuid.UUID, uuid.UUID) {
	t.Helper()
	pro, free := uuid.New(), uuid.New()
	accounts := &fakeGuestSpansAccounts{plans: map[string]api.Plan{pro.String(): api.PlanPro, free.String(): api.PlanFree}}
	store := state.NewMemStore()
	return NewGuestSpansIngester(accounts, peraccount.NewLimiter(), discardLogger()).WithLogs(store), store, pro, free
}

func listGuestLogs(t *testing.T, store *state.MemStore, req *apidpb.IngestGuestSpansRequest) []state.LogEvent {
	t.Helper()
	events, _, err := store.ListLogEvents(context.Background(), state.LogEventFilter{
		AccountID: req.GetAccountId(), AppID: req.GetAppId(), Source: state.LogEventSourceRuntime,
		Since: time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC), Until: time.Now().Add(time.Hour), Limit: 500,
	})
	if err != nil {
		t.Fatal(err)
	}
	return events
}

func TestGuestLogsProjectRedactedTraceCorrelatedRows(t *testing.T) {
	g, store, pro, _ := newGuestLogsTest(t)
	req := guestSpansRequest(pro, api.TraceCodecProtobuf, guestLogsPayload(t,
		guestLogRecord("charge failed for alice@example.com\n", logspb.SeverityNumber_SEVERITY_NUMBER_ERROR, guestLogTraceID),
		guestLogRecord("booting worker", logspb.SeverityNumber_SEVERITY_NUMBER_INFO, nil),
		guestLogRecord("   ", logspb.SeverityNumber_SEVERITY_NUMBER_INFO, guestLogTraceID),
	))
	resp := g.ingestLogs(context.Background(), req)
	if resp.GetOutcome() != guestSpansAccepted || resp.GetAcceptedRecords() != 1 || resp.GetRejectedRecords() != 2 {
		t.Fatalf("response = %+v, want 1 accepted (untraced and empty lines dropped)", resp)
	}
	events := listGuestLogs(t, store, req)
	if len(events) != 1 {
		t.Fatalf("events = %+v", events)
	}
	event := events[0]
	if event.TraceID != "00000000000000000000000000000009" || event.Level != "error" || event.Source != state.LogEventSourceRuntime ||
		event.DeploymentID != req.GetDeploymentId() || event.InstanceID != "i-1" {
		t.Fatalf("event = %+v", event)
	}
	if strings.Contains(event.Message, "alice@example.com") || !strings.Contains(event.Message, "[REDACTED:email]") || strings.HasSuffix(event.Message, "\n") {
		t.Fatalf("message not redacted and trimmed: %q", event.Message)
	}
	if !strings.Contains(string(event.Fields), `"span_id":"0000000000000007"`) {
		t.Fatalf("fields = %s", event.Fields)
	}

	// A retried export converges on the same rows.
	if again := g.ingestLogs(context.Background(), req); again.GetOutcome() != guestSpansAccepted {
		t.Fatalf("retry outcome = %+v", again)
	}
	if n := len(listGuestLogs(t, store, req)); n != 1 {
		t.Fatalf("retry duplicated rows: %d", n)
	}
}

func TestGuestLogsBoundsAndOutcomes(t *testing.T) {
	g, store, pro, free := newGuestLogsTest(t)
	ctx := context.Background()

	var records []*logspb.LogRecord
	for i := 0; i < api.TraceMaxLogRecordsPerExport+5; i++ {
		records = append(records, guestLogRecord("line "+strings.Repeat("x", i), logspb.SeverityNumber_SEVERITY_NUMBER_INFO, guestLogTraceID))
	}
	req := guestSpansRequest(pro, api.TraceCodecProtobuf, guestLogsPayload(t, records...))
	resp := g.ingestLogs(ctx, req)
	if resp.GetAcceptedRecords() != api.TraceMaxLogRecordsPerExport || resp.GetRejectedRecords() != 5 {
		t.Fatalf("bounded export = %+v", resp)
	}
	long := guestSpansRequest(pro, api.TraceCodecProtobuf, guestLogsPayload(t, guestLogRecord(strings.Repeat("y", 20000), logspb.SeverityNumber_SEVERITY_NUMBER_WARN, guestLogTraceID)))
	if resp := g.ingestLogs(ctx, long); resp.GetAcceptedRecords() != 1 {
		t.Fatalf("long line = %+v", resp)
	}
	if events := listGuestLogs(t, store, long); len(events) != 1 || len(events[0].Message) > api.TraceMaxLogMessageBytes+3 || events[0].Level != "warn" {
		t.Fatalf("long line stored as %d bytes level %q", len(events[0].Message), events[0].Level)
	}

	for _, tc := range []struct {
		name string
		req  *apidpb.IngestGuestSpansRequest
		want string
	}{
		{name: "plan without debugger", req: guestSpansRequest(free, api.TraceCodecProtobuf, guestLogsPayload(t, guestLogRecord("x", 9, guestLogTraceID))), want: guestSpansDisabled},
		{name: "json refused", req: guestSpansRequest(pro, api.TraceCodecJSON, []byte(`{"resourceLogs":[]}`)), want: guestSpansInvalid},
		{name: "garbage", req: guestSpansRequest(pro, api.TraceCodecProtobuf, []byte{0xff, 0xff}), want: guestSpansInvalid},
		{name: "logs bit is not a payload codec", req: guestSpansRequest(pro, api.TraceCodecProtobuf|api.TraceCodecLogsSignal, []byte("x")), want: guestSpansInvalid},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := g.ingestLogs(ctx, tc.req).GetOutcome(); got != tc.want {
				t.Fatalf("outcome = %q, want %q", got, tc.want)
			}
		})
	}

	// Log exports have their own per-account budget.
	limited := 0
	for i := 0; i < api.MustLimitsFor(api.PlanPro).DebugGuestLogExportsPerMinute+5; i++ {
		if g.ingestLogs(ctx, guestSpansRequest(pro, api.TraceCodecProtobuf, guestLogsPayload(t, guestLogRecord("z", 9, guestLogTraceID)))).GetOutcome() == guestSpansRateLimited {
			limited++
		}
	}
	if limited == 0 {
		t.Fatal("log exports were never rate limited")
	}
	if got := g.ingest(ctx, guestSpansRequest(pro, api.TraceCodecProtobuf, guestSpansPayload(t, 4))).GetOutcome(); got != guestSpansAccepted {
		t.Fatalf("trace exports must keep their own budget, got %q", got)
	}
}

func TestGuestLogLevel(t *testing.T) {
	for _, tc := range []struct {
		number logspb.SeverityNumber
		text   string
		want   string
	}{
		{logspb.SeverityNumber_SEVERITY_NUMBER_TRACE, "", "trace"},
		{logspb.SeverityNumber_SEVERITY_NUMBER_DEBUG2, "", "debug"},
		{logspb.SeverityNumber_SEVERITY_NUMBER_INFO4, "", "info"},
		{logspb.SeverityNumber_SEVERITY_NUMBER_WARN, "", "warn"},
		{logspb.SeverityNumber_SEVERITY_NUMBER_ERROR3, "", "error"},
		{logspb.SeverityNumber_SEVERITY_NUMBER_FATAL, "", "fatal"},
		{logspb.SeverityNumber_SEVERITY_NUMBER_UNSPECIFIED, "WARNING", "warn"},
		{logspb.SeverityNumber_SEVERITY_NUMBER_UNSPECIFIED, "CRITICAL", "fatal"},
		{logspb.SeverityNumber_SEVERITY_NUMBER_UNSPECIFIED, "verbose", ""},
	} {
		if got := guestLogLevel(tc.number, tc.text); got != tc.want {
			t.Errorf("guestLogLevel(%v, %q) = %q, want %q", tc.number, tc.text, got, tc.want)
		}
	}
}

func TestGuestLogsUnavailableWithoutWithLogs(t *testing.T) {
	g := NewGuestSpansIngester(&fakeGuestSpansAccounts{}, peraccount.NewLimiter(), discardLogger())
	if got := g.ingestLogs(context.Background(), guestSpansRequest(uuid.New(), api.TraceCodecProtobuf, []byte("x"))).GetOutcome(); got != guestSpansUnavailable {
		t.Fatalf("outcome = %q", got)
	}
}
