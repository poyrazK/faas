package gateway

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/status"
)

// serveH2C runs handler behind the gRPC error wrapper on an unencrypted
// HTTP/2 listener, the way gateways see gRPC callers.
func serveH2C(t *testing.T, handler http.HandlerFunc) *grpc.ClientConn {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w, finish := wrapGRPCPlatformErrors(w, r)
		defer finish()
		handler(w, r)
	})}
	srv.Protocols = new(http.Protocols)
	srv.Protocols.SetUnencryptedHTTP2(true)
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Close() })
	conn, err := grpc.NewClient(ln.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return conn
}

func healthCheck(t *testing.T, conn *grpc.ClientConn) error {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err := healthpb.NewHealthClient(conn).Check(ctx, &healthpb.HealthCheckRequest{})
	return err
}

func TestGRPCCallerGetsUnavailableForPlatform502(t *testing.T) {
	conn := serveH2C(t, func(w http.ResponseWriter, _ *http.Request) {
		writeForwarderProblem(w, http.StatusBadGateway)
	})
	err := healthCheck(t, conn)
	st, _ := status.FromError(err)
	if st.Code() != codes.Unavailable {
		t.Fatalf("code = %v (%v), want Unavailable", st.Code(), err)
	}
	if msg := st.Message(); !strings.Contains(msg, "502") || !strings.Contains(msg, "Gregale could not complete the connection") {
		t.Fatalf("message = %q, want the platform's reason", msg)
	}
}

func TestGRPCCallerGetsUnavailableForBridgePlainText502(t *testing.T) {
	conn := serveH2C(t, func(w http.ResponseWriter, _ *http.Request) {
		// The bridge's own 502 body (cmd/vmmd-stream-bridge/bad_gateway.go);
		// it no longer carries the transport error (prod hunt r30).
		http.Error(w, "Bad gateway: the app closed the connection or did not send a response", http.StatusBadGateway)
	})
	st, _ := status.FromError(healthCheck(t, conn))
	if st.Code() != codes.Unavailable || !strings.Contains(st.Message(), "app closed the connection") {
		t.Fatalf("got %v %q, want Unavailable naming the bridge error", st.Code(), st.Message())
	}
}

func TestGRPCAppStatusPassesThrough(t *testing.T) {
	conn := serveH2C(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/grpc")
		w.Header().Set("Trailer", "Grpc-Status, Grpc-Message")
		w.WriteHeader(http.StatusOK)
		w.Header().Set("Grpc-Status", "5")
		w.Header().Set("Grpc-Message", "no such service")
	})
	st, _ := status.FromError(healthCheck(t, conn))
	if st.Code() != codes.NotFound || st.Message() != "no such service" {
		t.Fatalf("got %v %q, want the app's NotFound untouched", st.Code(), st.Message())
	}
}

func TestNonGRPCRequestKeepsHTTPError(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	w, finish := wrapGRPCPlatformErrors(rec, req)
	writeForwarderProblem(w, http.StatusBadGateway)
	finish()
	if rec.Code != http.StatusBadGateway || strings.Contains(rec.Header().Get("Content-Type"), "grpc") {
		t.Fatalf("HTTP caller got %d %q, want the unchanged 502 problem", rec.Code, rec.Header().Get("Content-Type"))
	}
}

// Recorders inside the wrapper (request telemetry) must still see the 502.
func TestGRPCWrapperKeepsInnerStatusForTelemetry(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/svc/M", nil)
	req.Header.Set("Content-Type", "application/grpc")
	rec := httptest.NewRecorder()
	w, finish := wrapGRPCPlatformErrors(rec, req)
	inner := &statusRecorder{ResponseWriter: w, status: http.StatusOK, request: req}
	writeForwarderProblem(inner, http.StatusServiceUnavailable)
	finish()
	if inner.status != http.StatusServiceUnavailable {
		t.Fatalf("inner recorder status = %d, want 503", inner.status)
	}
	if rec.Code != http.StatusOK || rec.Header().Get("Content-Type") != "application/grpc" {
		t.Fatalf("wire = %d %q, want 200 application/grpc", rec.Code, rec.Header().Get("Content-Type"))
	}
	if got := rec.Header().Get(http.TrailerPrefix + "Grpc-Status"); got != "14" {
		t.Fatalf("grpc-status trailer = %q, want 14", got)
	}
}

// A failed RPC is HTTP 200 on the wire; telemetry, metrics and health must
// record it as the failure its grpc-status says.
func TestObservedStatusCountsFailedGRPCCalls(t *testing.T) {
	for _, tc := range []struct {
		name   string
		header map[string]string
		want   int
	}{
		{"unavailable trailer", map[string]string{"Content-Type": "application/grpc", http.TrailerPrefix + "Grpc-Status": "14"}, http.StatusServiceUnavailable},
		{"trailers-only internal", map[string]string{"Content-Type": "application/grpc+proto", "Grpc-Status": "13"}, http.StatusInternalServerError},
		{"not found", map[string]string{"Content-Type": "application/grpc", http.TrailerPrefix + "Grpc-Status": "5"}, http.StatusNotFound},
		{"ok", map[string]string{"Content-Type": "application/grpc", http.TrailerPrefix + "Grpc-Status": "0"}, http.StatusOK},
		{"plain http 200", map[string]string{"Content-Type": "text/plain", "Grpc-Status": "14"}, http.StatusOK},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/svc/M", nil)
			rec := &statusRecorder{ResponseWriter: httptest.NewRecorder(), status: http.StatusOK, request: req}
			req = req.WithContext(withResponseRecorder(req.Context(), rec))
			rec.WriteHeader(http.StatusOK)
			for k, v := range tc.header {
				rec.Header()[k] = []string{v}
			}
			if got := observedStatus(req, http.StatusOK); got != tc.want {
				t.Fatalf("observedStatus = %d, want %d", got, tc.want)
			}
		})
	}
}

func TestGRPCPercentEncode(t *testing.T) {
	if got := grpcPercentEncode("a%b\nc é"); got != "a%25b%0Ac %C3%A9" {
		t.Fatalf("grpcPercentEncode = %q", got)
	}
}
