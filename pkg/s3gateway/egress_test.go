package s3gateway

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

type readEgressMetrics struct {
	gatewayRequestMetrics
	reserved, delivered int64
	reserveError        error
	contextError        error
}

func (m *readEgressMetrics) ReserveObjectStorageGatewayEgress(_ context.Context, _ string, n int64, _ time.Time, _ api.ObjectStoragePolicy) error {
	if m.reserveError != nil {
		return m.reserveError
	}
	m.reserved += n
	return nil
}

func (m *readEgressMetrics) RecordObjectStorageProviderEgress(ctx context.Context, _ string, n int64, _ time.Time) error {
	m.delivered += n
	m.contextError = ctx.Err()
	return nil
}

func TestGatewayReadEgressCoverage(t *testing.T) {
	for _, tc := range []struct {
		name, method string
		status       int
		length       int64
		reserveError error
		wantStatus   int
		wantBody     string
		wantBytes    int64
	}{
		{"full", "GET", 200, 6, nil, 200, "abcdef", 6},
		{"range", "GET", 206, 3, nil, 206, "abc", 3},
		{"head", "HEAD", 200, 6, nil, 200, "", 0},
		{"not modified", "GET", 304, 6, nil, 304, "", 0},
		{"precondition", "GET", 412, 6, nil, 412, "", 0},
		{"empty", "GET", 200, 0, nil, 200, "", 0},
		{"unknown length", "GET", 200, -1, nil, 503, "", 0},
		{"exhausted", "GET", 200, 6, state.ErrObjectBudget, 402, "", 0},
		{"database failure", "GET", 200, 6, errors.New("offline"), 503, "", 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h, store, _ := newGatewayTestHandler(t, state.ObjectBucketPermissionRead, nil)
			h.registry.Accounting.AccountingMode = api.ObjectStorageGatewaySafetyV1
			metrics := &readEgressMetrics{reserveError: tc.reserveError}
			h.requestMetrics = metrics
			w := httptest.NewRecorder()
			r := httptest.NewRequest(tc.method, "https://s3.gregale.dev/assets/key", nil)
			response := &http.Response{StatusCode: tc.status, ContentLength: tc.length, Header: http.Header{}, Body: io.NopCloser(strings.NewReader("abcdef"))}
			h.writeGatewayRead(w, r, requestContext{bucket: store.bucket}, "key", response)
			if w.Code != tc.wantStatus || metrics.reserved != tc.wantBytes || metrics.delivered != 0 {
				t.Fatal(w.Code, metrics, w.Body.String())
			}
			if w.Code < 300 && w.Body.String() != tc.wantBody {
				t.Fatal("forwarded unreserved bytes", w.Body.String())
			}
		})
	}
}

type disconnectedResponseWriter struct{ header http.Header }

func (w *disconnectedResponseWriter) Header() http.Header     { return w.header }
func (*disconnectedResponseWriter) WriteHeader(int)           {}
func (*disconnectedResponseWriter) Write([]byte) (int, error) { return 2, io.ErrClosedPipe }

func TestGatewayEgressDisconnectSemantics(t *testing.T) {
	for _, mode := range []string{"", api.ObjectStorageGatewaySafetyV1} {
		t.Run(mode, func(t *testing.T) {
			h, store, _ := newGatewayTestHandler(t, state.ObjectBucketPermissionRead, nil)
			h.registry.Accounting.AccountingMode = mode
			metrics := &readEgressMetrics{}
			h.requestMetrics = metrics
			ctx, cancel := context.WithCancel(t.Context())
			cancel()
			r := httptest.NewRequest("GET", "https://s3.gregale.dev/assets/key", nil).WithContext(ctx)
			response := &http.Response{StatusCode: 200, ContentLength: 6, Header: http.Header{}, Body: io.NopCloser(strings.NewReader("abcdef"))}
			h.writeGatewayRead(&disconnectedResponseWriter{header: http.Header{}}, r, requestContext{bucket: store.bucket}, "key", response)
			if mode == "" && (metrics.delivered != 2 || metrics.contextError != nil) {
				t.Fatal("disconnect lost legacy delivered bytes", metrics)
			}
			if mode != "" && (metrics.reserved != 6 || metrics.delivered != 0) {
				t.Fatal("disconnect refunded safety reservation", metrics)
			}
		})
	}
}
