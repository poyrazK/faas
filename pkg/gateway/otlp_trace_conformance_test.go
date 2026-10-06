// adr: 392
package gateway

import (
	"bytes"
	"compress/gzip"
	"context"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/google/uuid"
	"go.opentelemetry.io/collector/pdata/ptrace/ptraceotlp"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	statuspb "google.golang.org/genproto/googleapis/rpc/status"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"

	"github.com/onebox-faas/faas/pkg/ratelimit/peraccount"
)

const otlpConformanceJSON = `{"resourceSpans":[{"futureResourceField":true,"scopeSpans":[{"spans":[
 {"traceId":"0123456789ABCDEF0123456789ABCDEF","spanId":"0123456789ABCDEF","name":"first","kind":2,"futureSpanField":true},
 {"traceId":"1123456789abcdef0123456789abcdef","spanId":"1123456789abcdef","parentSpanId":"2123456789abcdef","name":"second","links":[{"traceId":"3123456789abcdef0123456789abcdef","spanId":"3123456789abcdef"}]}
]}]}],"futureRequestField":{}}`

func otlpConformanceHandler(accountID uuid.UUID) *OTelSpansHandler {
	return NewOTelSpansHandler(OTelSpansHandlerConfig{
		AuthClient: &fakeAuthClient{accountID: accountID.String(), plan: "scale"},
		Limiter:    peraccount.NewLimiter(),
		Acc:        NewSpansAccumulator(),
	})
}

func TestOTLPHTTPConformance(t *testing.T) {
	for _, encoding := range []string{"application/json", "application/x-protobuf"} {
		for _, compressed := range []bool{false, true} {
			t.Run(encoding+"/gzip="+strconv.FormatBool(compressed), func(t *testing.T) {
				body := []byte(otlpConformanceJSON)
				if encoding == "application/x-protobuf" {
					export := ptraceotlp.NewExportRequest()
					if err := export.UnmarshalJSON(body); err != nil {
						t.Fatal(err)
					}
					var err error
					body, err = export.MarshalProto()
					if err != nil {
						t.Fatal(err)
					}
				}
				if compressed {
					body = gzipOTLP(t, body)
				}
				h := otlpConformanceHandler(uuid.New())
				req := httptest.NewRequest(http.MethodPost, "/v1/otel/v1/traces", bytes.NewReader(body))
				req.Header.Set("Authorization", "Bearer test")
				req.Header.Set("Content-Type", encoding)
				if compressed {
					req.Header.Set("Content-Encoding", "gzip")
				}
				rr := httptest.NewRecorder()
				h.ServeHTTP(rr, req)
				if rr.Code != http.StatusOK || rr.Header().Get("Content-Type") != encoding {
					t.Fatalf("response = %d %s %s", rr.Code, rr.Header().Get("Content-Type"), rr.Body.Bytes())
				}
				response := ptraceotlp.NewExportResponse()
				var err error
				if encoding == "application/json" {
					err = response.UnmarshalJSON(rr.Body.Bytes())
				} else {
					err = response.UnmarshalProto(rr.Body.Bytes())
				}
				if err != nil || response.PartialSuccess().RejectedSpans() != 0 {
					t.Fatalf("standard export response: %v", err)
				}
				if h.cfg.Acc.Len() != 2 {
					t.Fatalf("trace buckets = %d, want 2", h.cfg.Acc.Len())
				}
				bucket, ok := h.cfg.Acc.buckets.Load("0123456789abcdef0123456789abcdef")
				if !ok {
					t.Fatal("hexadecimal trace ID changed at ingestion")
				}
				spans, _ := bucket.(*spansAccumulator).snapshot()
				if len(spans) != 1 || spans[0].SpanID != "0123456789abcdef" {
					t.Fatalf("span identity = %+v", spans)
				}
			})
		}
	}
}

func TestOTLPHTTPConformancePartialSuccess(t *testing.T) {
	accountID := uuid.New()
	h := otlpConformanceHandler(accountID)
	contested := "0123456789abcdef0123456789abcdef"
	if _, err := h.cfg.Acc.Add(contested, uuid.New(), []summarizedSpan{{SpanID: "original"}}); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/v1/otel/v1/traces", strings.NewReader(otlpConformanceJSON))
	req.Header.Set("Authorization", "Bearer test")
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	response := ptraceotlp.NewExportResponse()
	if err := response.UnmarshalJSON(rr.Body.Bytes()); err != nil {
		t.Fatal(err)
	}
	if rr.Code != http.StatusOK || response.PartialSuccess().RejectedSpans() != 1 || h.cfg.Acc.Len() != 2 {
		t.Fatalf("partial response = %d %s, buckets = %d", rr.Code, rr.Body.String(), h.cfg.Acc.Len())
	}
	bucket, _ := h.cfg.Acc.buckets.Load(contested)
	spans, _ := bucket.(*spansAccumulator).snapshot()
	if len(spans) != 1 || spans[0].SpanID != "original" {
		t.Fatalf("cross-account trace modified: %+v", spans)
	}
}

func TestOTLPHTTPConformanceEmptyAndInvalid(t *testing.T) {
	for name, body := range map[string]string{
		"empty":                `{}`,
		"malformed":            `{"resourceSpans":`,
		"zero ID":              strings.Replace(otlpConformanceJSON, "0123456789ABCDEF0123456789ABCDEF", strings.Repeat("0", 32), 1),
		"short ID":             strings.Replace(otlpConformanceJSON, "0123456789ABCDEF0123456789ABCDEF", "abcd", 1),
		"invalid second trace": strings.Replace(otlpConformanceJSON, "1123456789abcdef0123456789abcdef", "abcd", 1),
	} {
		t.Run(name, func(t *testing.T) {
			h := otlpConformanceHandler(uuid.New())
			req := httptest.NewRequest(http.MethodPost, "/v1/otel/v1/traces", strings.NewReader(body))
			req.Header.Set("Authorization", "Bearer test")
			req.Header.Set("Content-Type", "application/json")
			rr := httptest.NewRecorder()
			h.ServeHTTP(rr, req)
			want := http.StatusBadRequest
			if name == "empty" {
				want = http.StatusOK
				if rr.Body.String() != "{}" {
					t.Fatalf("empty export response = %s", rr.Body.String())
				}
			} else {
				var problem statuspb.Status
				if err := protojson.Unmarshal(rr.Body.Bytes(), &problem); err != nil || problem.Message == "" {
					t.Fatalf("OTLP error response = %s, error = %v", rr.Body.String(), err)
				}
			}
			if rr.Code != want || h.cfg.Acc.Len() != 0 {
				t.Fatalf("response = %d %s, buckets = %d", rr.Code, rr.Body.String(), h.cfg.Acc.Len())
			}
		})
	}
}

func TestOTLPHTTPConformanceSDKExporter(t *testing.T) {
	h := otlpConformanceHandler(uuid.New())
	mux := http.NewServeMux()
	mux.Handle("/v1/otel/v1/traces", h)
	server := httptest.NewServer(mux)
	defer server.Close()
	ctx := context.Background()
	exporter, err := otlptracehttp.New(ctx,
		otlptracehttp.WithEndpointURL(server.URL+"/v1/otel/v1/traces"),
		otlptracehttp.WithHeaders(map[string]string{"Authorization": "Bearer test"}),
		otlptracehttp.WithCompression(otlptracehttp.GzipCompression),
	)
	if err != nil {
		t.Fatal(err)
	}
	provider := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter))
	t.Cleanup(func() {
		if err := provider.Shutdown(ctx); err != nil {
			t.Error(err)
		}
	})
	_, span := provider.Tracer("conformance").Start(ctx, "standard-exporter")
	span.End()
	if h.cfg.Acc.Len() != 1 {
		t.Fatalf("SDK export produced %d trace buckets, want 1", h.cfg.Acc.Len())
	}
}

func TestOTLPHTTPConformanceGzipLimit(t *testing.T) {
	h := otlpConformanceHandler(uuid.New())
	body := gzipOTLP(t, bytes.Repeat([]byte(" "), (4<<20)+1))
	req := httptest.NewRequest(http.MethodPost, "/v1/otel/v1/traces", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer test")
	req.Header.Set("Content-Type", "application/x-protobuf")
	req.Header.Set("Content-Encoding", "gzip")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	var problem statuspb.Status
	if err := proto.Unmarshal(rr.Body.Bytes(), &problem); err != nil || rr.Code != http.StatusBadRequest {
		t.Fatalf("bounded protobuf error = %d %v", rr.Code, err)
	}
}

func TestOTLPHTTPConformanceErrors(t *testing.T) {
	for _, tc := range []struct {
		name, mediaType, encoding, body string
		status                          int
	}{
		{"unsupported media", "text/plain", "", "{}", http.StatusUnsupportedMediaType},
		{"unsupported encoding", "application/json", "br", "{}", http.StatusUnsupportedMediaType},
		{"invalid gzip", "application/json", "gzip", "{}", http.StatusBadRequest},
		{"invalid protobuf", "application/x-protobuf", "", "\xff", http.StatusBadRequest},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := otlpConformanceHandler(uuid.New())
			req := httptest.NewRequest(http.MethodPost, "/v1/otel/v1/traces", strings.NewReader(tc.body))
			req.Header.Set("Authorization", "Bearer test")
			req.Header.Set("Content-Type", tc.mediaType)
			req.Header.Set("Content-Encoding", tc.encoding)
			rr := httptest.NewRecorder()
			h.ServeHTTP(rr, req)
			var problem statuspb.Status
			var err error
			if tc.mediaType == "application/x-protobuf" {
				err = proto.Unmarshal(rr.Body.Bytes(), &problem)
			} else {
				err = protojson.Unmarshal(rr.Body.Bytes(), &problem)
			}
			if rr.Code != tc.status || err != nil || problem.Message == "" || h.cfg.Acc.Len() != 0 {
				t.Fatalf("error response = %d %s, decode = %v, buckets = %d", rr.Code, rr.Body.Bytes(), err, h.cfg.Acc.Len())
			}
		})
	}
}

type unreadOTLPBody struct{ t *testing.T }

func (b unreadOTLPBody) Read([]byte) (int, error) {
	b.t.Fatal("unauthorized OTLP request read the body")
	return 0, nil
}

func (unreadOTLPBody) Close() error { return nil }

func TestOTLPHTTPConformanceAuthBeforeGzip(t *testing.T) {
	h := otlpConformanceHandler(uuid.New())
	req := httptest.NewRequest(http.MethodPost, "/v1/otel/v1/traces", nil)
	req.Body = unreadOTLPBody{t}
	req.Header.Set("Content-Type", "application/x-protobuf")
	req.Header.Set("Content-Encoding", "gzip")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	var problem statuspb.Status
	if err := proto.Unmarshal(rr.Body.Bytes(), &problem); err != nil || rr.Code != http.StatusUnauthorized || problem.Message == "" {
		t.Fatalf("unauthorized protobuf response = %d %v", rr.Code, err)
	}
}

func gzipOTLP(t *testing.T, body []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	writer := gzip.NewWriter(&buf)
	if _, err := writer.Write(body); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}
