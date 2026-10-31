package grpcproblem_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/status"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/gateway/grpcproblem"
)

// h2cServer serves h over cleartext HTTP/2, as gatewayd-public does behind
// Caddy's h2c hop.
func h2cServer(t *testing.T, h http.Handler) string {
	t.Helper()
	srv := httptest.NewUnstartedServer(h)
	srv.Config.Protocols = new(http.Protocols)
	srv.Config.Protocols.SetHTTP1(true)
	srv.Config.Protocols.SetUnencryptedHTTP2(true)
	srv.Start()
	t.Cleanup(srv.Close)
	return strings.TrimPrefix(srv.URL, "http://")
}

func healthCheck(t *testing.T, addr string) error {
	t.Helper()
	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("grpc client: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err = healthpb.NewHealthClient(conn).Check(ctx, &healthpb.HealthCheckRequest{})
	return err
}

// TestGatewayRefusalReachesGRPCClientsAsStatus is the Hunt 14 H14-56
// regression: an application-auth refusal must reach a gRPC client as
// UNAUTHENTICATED with the gateway's reason, not as a stream with no trailers.
func TestGatewayRefusalReachesGRPCClientsAsStatus(t *testing.T) {
	refuse := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("WWW-Authenticate", `Bearer realm="app"`)
		api.WriteProblem(w, api.NewProblem(http.StatusUnauthorized, api.CodeUnauthorized,
			"Authentication required", "this app requires a bearer token"))
	})
	err := healthCheck(t, h2cServer(t, grpcproblem.Middleware(refuse)))
	st, ok := status.FromError(err)
	if !ok {
		t.Fatalf("health check error %v is not a gRPC status", err)
	}
	if st.Code() != codes.Unauthenticated {
		t.Fatalf("code = %s (%q), want Unauthenticated", st.Code(), st.Message())
	}
	want := "Authentication required: this app requires a bearer token (" + api.CodeUnauthorized + ")"
	if st.Message() != want {
		t.Fatalf("message = %q, want %q", st.Message(), want)
	}
}

// TestGRPCServerResponsesPassThrough proves a real gRPC reply, whose status
// travels in trailers, is not touched.
func TestGRPCServerResponsesPassThrough(t *testing.T) {
	gs := grpc.NewServer()
	hs := health.NewServer()
	healthpb.RegisterHealthServer(gs, hs)
	t.Cleanup(gs.Stop)
	addr := h2cServer(t, grpcproblem.Middleware(gs))
	if err := healthCheck(t, addr); err != nil {
		t.Fatalf("serving health check through the middleware: %v", err)
	}
	hs.SetServingStatus("", healthpb.HealthCheckResponse_NOT_SERVING)
	if err := healthCheck(t, addr); err != nil {
		t.Fatalf("not-serving health check through the middleware: %v", err)
	}
	hs.Shutdown()
	unknown := grpc.NewServer() // no services: every call is Unimplemented, from the trailer
	t.Cleanup(unknown.Stop)
	if code := status.Code(healthCheck(t, h2cServer(t, grpcproblem.Middleware(unknown)))); code != codes.Unimplemented {
		t.Fatalf("unregistered service code = %s, want Unimplemented from the server's trailer", code)
	}
}

func TestMiddlewareTranslatesOnlyGRPCErrorResponses(t *testing.T) {
	for _, tc := range []struct {
		name        string
		contentType string
		respond     func(http.ResponseWriter)
		wantStatus  int
		wantGRPC    string // grpc-status, "" when untranslated
		wantBody    bool
	}{{
		name:        "rate limit problem to gRPC",
		contentType: "application/grpc",
		respond: func(w http.ResponseWriter) {
			api.WriteProblem(w, api.NewProblem(http.StatusTooManyRequests, "rate_limited", "Too many requests", "slow down"))
		},
		wantStatus: http.StatusOK, wantGRPC: "8",
	}, {
		name:        "plain-text bad gateway to gRPC+proto",
		contentType: "application/grpc+proto",
		respond:     func(w http.ResponseWriter) { http.Error(w, "upstream gone", http.StatusBadGateway) },
		wantStatus:  http.StatusOK, wantGRPC: "14",
	}, {
		name:        "problem to a plain HTTP request",
		contentType: "application/json",
		respond: func(w http.ResponseWriter) {
			api.WriteProblem(w, api.NewProblem(http.StatusUnauthorized, api.CodeUnauthorized, "Authentication required", ""))
		},
		wantStatus: http.StatusUnauthorized, wantBody: true,
	}, {
		name:        "problem to gRPC-Web, which carries status in the body",
		contentType: "application/grpc-web+proto",
		respond: func(w http.ResponseWriter) {
			api.WriteProblem(w, api.NewProblem(http.StatusServiceUnavailable, api.CodeCapacity, "Busy", ""))
		},
		wantStatus: http.StatusServiceUnavailable, wantBody: true,
	}, {
		name:        "gRPC error reply from the app",
		contentType: "application/grpc",
		respond: func(w http.ResponseWriter) {
			w.Header().Set("Content-Type", "application/grpc")
			w.Header().Set("Grpc-Status", "5")
			w.WriteHeader(http.StatusOK)
		},
		wantStatus: http.StatusOK, wantGRPC: "5",
	}, {
		name:        "successful non-gRPC body to gRPC is left alone",
		contentType: "application/grpc",
		respond:     func(w http.ResponseWriter) { _, _ = w.Write([]byte("ok")) },
		wantStatus:  http.StatusOK, wantBody: true,
	}} {
		t.Run(tc.name, func(t *testing.T) {
			h := grpcproblem.Middleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { tc.respond(w) }))
			req := httptest.NewRequest(http.MethodPost, "/grpc.health.v1.Health/Check", strings.NewReader(""))
			req.Header.Set("Content-Type", tc.contentType)
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			if rec.Code != tc.wantStatus {
				t.Fatalf("status = %d, want %d", rec.Code, tc.wantStatus)
			}
			if got := rec.Header().Get("Grpc-Status"); got != tc.wantGRPC {
				t.Fatalf("grpc-status = %q, want %q", got, tc.wantGRPC)
			}
			if tc.wantGRPC != "" && rec.Header().Get("Content-Type") != "application/grpc" {
				t.Fatalf("content-type = %q, want application/grpc", rec.Header().Get("Content-Type"))
			}
			if gotBody := rec.Body.Len() > 0; gotBody != tc.wantBody {
				t.Fatalf("body %q, want body present = %v", rec.Body.String(), tc.wantBody)
			}
		})
	}
}

func TestLongRefusalMessageIsCutOnARuneBoundary(t *testing.T) {
	detail := strings.Repeat("ç", 700) // 1,400 bytes of two-byte runes
	h := grpcproblem.Middleware(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		api.WriteProblem(w, api.NewProblem(http.StatusForbidden, "forbidden", "x", detail))
	}))
	req := httptest.NewRequest(http.MethodPost, "/grpc.health.v1.Health/Check", strings.NewReader(""))
	req.Header.Set("Content-Type", "application/grpc")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	message, err := url.PathUnescape(rec.Header().Get("Grpc-Message"))
	if err != nil {
		t.Fatalf("decode grpc-message: %v", err)
	}
	if len(message) == 0 || len(message) > 1024 {
		t.Fatalf("grpc-message is %d bytes, want 1..1024", len(message))
	}
	if !utf8.ValidString(message) {
		t.Fatalf("grpc-message is not valid UTF-8 after truncation: %q", message[len(message)-4:])
	}
}

func TestCodeForHTTPStatus(t *testing.T) {
	for status, want := range map[int]codes.Code{
		http.StatusBadRequest:            codes.InvalidArgument,
		http.StatusUnauthorized:          codes.Unauthenticated,
		http.StatusPaymentRequired:       codes.FailedPrecondition,
		http.StatusForbidden:             codes.PermissionDenied,
		http.StatusNotFound:              codes.NotFound,
		http.StatusConflict:              codes.Aborted,
		http.StatusRequestEntityTooLarge: codes.ResourceExhausted,
		http.StatusTooManyRequests:       codes.ResourceExhausted,
		http.StatusInternalServerError:   codes.Internal,
		http.StatusNotImplemented:        codes.Unimplemented,
		http.StatusBadGateway:            codes.Unavailable,
		http.StatusServiceUnavailable:    codes.Unavailable,
		http.StatusGatewayTimeout:        codes.DeadlineExceeded,
		418:                              codes.Unknown,
	} {
		if got := codes.Code(grpcproblem.CodeForHTTPStatus(status)); got != want {
			t.Errorf("CodeForHTTPStatus(%d) = %s, want %s", status, got, want)
		}
	}
}

func TestEncodeMessage(t *testing.T) {
	for in, want := range map[string]string{
		"plain text":  "plain text",
		"100% full":   "100%25 full",
		"line\nbreak": "line%0Abreak",
		"café":        "caf%C3%A9",
	} {
		if got := grpcproblem.EncodeMessage(in); got != want {
			t.Errorf("EncodeMessage(%q) = %q, want %q", in, got, want)
		}
	}
}
