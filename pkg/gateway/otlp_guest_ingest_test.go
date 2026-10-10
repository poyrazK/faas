package gateway

import (
	"bytes"
	"compress/gzip"
	"errors"
	"testing"

	"github.com/google/uuid"
	collectortracepb "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	tracepb "go.opentelemetry.io/proto/otlp/trace/v1"
	"google.golang.org/protobuf/proto"
)

func guestExportProto(t *testing.T, traceIDs ...[]byte) []byte {
	t.Helper()
	spans := make([]*tracepb.Span, 0, len(traceIDs))
	for i, tid := range traceIDs {
		spans = append(spans, &tracepb.Span{TraceId: tid, SpanId: []byte{0, 0, 0, 0, 0, 0, 0, byte(i + 1)}, Name: "SELECT", StartTimeUnixNano: 1, EndTimeUnixNano: 2})
	}
	body, err := proto.Marshal(&collectortracepb.ExportTraceServiceRequest{ResourceSpans: []*tracepb.ResourceSpans{{ScopeSpans: []*tracepb.ScopeSpans{{Spans: spans}}}}})
	if err != nil {
		t.Fatal(err)
	}
	return body
}

func traceID(b byte) []byte {
	id := make([]byte, 16)
	id[15] = b
	return id
}

func TestAddOTLPTraceExportProtobufAndGzip(t *testing.T) {
	acct := uuid.New()
	acc := NewSpansAccumulator()
	body := guestExportProto(t, traceID(1), traceID(1), traceID(2))
	accepted, rejected, err := acc.AddOTLPTraceExport(acct, "application/x-protobuf", "", body, 4<<20)
	if err != nil || accepted != 3 || rejected != 0 {
		t.Fatalf("protobuf: accepted=%d rejected=%d err=%v", accepted, rejected, err)
	}
	if acc.Len() != 2 {
		t.Fatalf("buckets = %d, want 2", acc.Len())
	}

	var gz bytes.Buffer
	w := gzip.NewWriter(&gz)
	_, _ = w.Write(guestExportProto(t, traceID(3)))
	_ = w.Close()
	accepted, _, err = acc.AddOTLPTraceExport(acct, "application/x-protobuf", "gzip", gz.Bytes(), 4<<20)
	if err != nil || accepted != 1 {
		t.Fatalf("gzip: accepted=%d err=%v", accepted, err)
	}
}

func TestAddOTLPTraceExportJSON(t *testing.T) {
	body := []byte(`{"resourceSpans":[{"scopeSpans":[{"spans":[{"traceId":"0000000000000000000000000000000a","spanId":"0000000000000001","name":"GET","startTimeUnixNano":"1","endTimeUnixNano":"2"}]}]}]}`)
	accepted, rejected, err := NewSpansAccumulator().AddOTLPTraceExport(uuid.New(), "application/json", "", body, 4<<20)
	if err != nil || accepted != 1 || rejected != 0 {
		t.Fatalf("json: accepted=%d rejected=%d err=%v", accepted, rejected, err)
	}
}

func TestAddOTLPTraceExportCrossAccountRejected(t *testing.T) {
	acc := NewSpansAccumulator()
	if _, _, err := acc.AddOTLPTraceExport(uuid.New(), "application/x-protobuf", "", guestExportProto(t, traceID(1)), 4<<20); err != nil {
		t.Fatal(err)
	}
	accepted, rejected, err := acc.AddOTLPTraceExport(uuid.New(), "application/x-protobuf", "", guestExportProto(t, traceID(1), traceID(2)), 4<<20)
	if err != nil || accepted != 1 || rejected != 1 {
		t.Fatalf("cross-account: accepted=%d rejected=%d err=%v", accepted, rejected, err)
	}
}

// adr: 934 — customer spans cannot claim platform dependency identity.
func TestAddOTLPTraceExportStripsPlatformAttributes(t *testing.T) {
	acc := NewSpansAccumulator()
	acct := uuid.New()
	tid := traceID(9)
	body, err := proto.Marshal(&collectortracepb.ExportTraceServiceRequest{ResourceSpans: []*tracepb.ResourceSpans{{ScopeSpans: []*tracepb.ScopeSpans{{Spans: []*tracepb.Span{{
		TraceId: tid, SpanId: []byte{0, 0, 0, 0, 0, 0, 0, 1}, Name: "forged", StartTimeUnixNano: 1, EndTimeUnixNano: 2,
		Attributes: []*commonpb.KeyValue{
			{Key: "gregale.dependency.type", Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: "managed_binding"}}},
			{Key: "gregale.dependency.kind", Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: "postgres"}}},
			{Key: "db.system", Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: "postgresql"}}},
		},
	}}}}}}})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := acc.AddOTLPTraceExport(acct, "application/x-protobuf", "", body, 4<<20); err != nil {
		t.Fatal(err)
	}
	spans, _ := acc.DrainAndRemove(formatTraceID(tid))
	if len(spans) != 1 {
		t.Fatalf("spans = %d", len(spans))
	}
	attrs := spans[0].Attributes
	if _, ok := attrs["gregale.dependency.type"]; ok {
		t.Fatalf("platform attribute survived: %v", attrs)
	}
	if _, ok := attrs["gregale.dependency.kind"]; ok {
		t.Fatalf("platform attribute survived: %v", attrs)
	}
	if attrs["db.system"] != "postgresql" {
		t.Fatalf("semantic attribute dropped: %v", attrs)
	}
}

func TestAddOTLPTraceExportInvalid(t *testing.T) {
	acc := NewSpansAccumulator()
	big := bytes.Repeat([]byte{0}, 64)
	for name, tc := range map[string]struct {
		contentType, encoding string
		body                  []byte
		limit                 int
	}{
		"media type":    {contentType: "text/plain", body: []byte("x"), limit: 1024},
		"encoding":      {contentType: "application/x-protobuf", encoding: "br", body: []byte("x"), limit: 1024},
		"bad gzip":      {contentType: "application/x-protobuf", encoding: "gzip", body: []byte("x"), limit: 1024},
		"bad protobuf":  {contentType: "application/x-protobuf", body: []byte{0xff, 0xff}, limit: 1024},
		"bad json":      {contentType: "application/json", body: []byte("{"), limit: 1024},
		"decoded limit": {contentType: "application/x-protobuf", body: big, limit: 32},
		"zero trace id": {contentType: "application/x-protobuf", body: guestExportProto(t, make([]byte, 16)), limit: 1024},
	} {
		t.Run(name, func(t *testing.T) {
			_, _, err := acc.AddOTLPTraceExport(uuid.New(), tc.contentType, tc.encoding, tc.body, tc.limit)
			if !errors.Is(err, ErrOTLPExportInvalid) {
				t.Fatalf("err = %v, want ErrOTLPExportInvalid", err)
			}
		})
	}
	if acc.Len() != 0 {
		t.Fatalf("invalid exports buffered %d buckets", acc.Len())
	}
}
