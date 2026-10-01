package gateway

// adr: 126 — a gRPC client receives replies before closing its request stream.
// adr: 426 — native gRPC admission must not wait for request EOF.

import (
	"context"
	"github.com/onebox-faas/faas/pkg/api"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"
)

func TestHandlerGRPCRepliesBeforeRequestEOF(t *testing.T) {
	for _, streaming := range []bool{false, true} {
		t.Run(map[bool]string{false: "buffered", true: "streaming"}[streaming], func(t *testing.T) {
			for _, contentType := range []string{"application/grpc", "application/grpc+proto"} {
				t.Run(contentType, func(t *testing.T) {
					backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						_ = http.NewResponseController(w).EnableFullDuplex()
						prefix := make([]byte, 4)
						if _, err := io.ReadFull(r.Body, prefix); err != nil {
							return
						}
						w.Header().Set("Content-Type", contentType)
						w.Header().Add("Trailer", "Grpc-Status")
						_, _ = w.Write(prefix)
						_ = http.NewResponseController(w).Flush()
						_, _ = io.Copy(io.Discard, r.Body)
						w.Header().Set("Grpc-Status", "0")
					}))
					defer backend.Close()
					proxy := NewInternalReverseProxy(&stubDialer{server: backend}, &url.URL{Scheme: "http", Host: "internal"}, slog.New(slog.NewTextHandler(io.Discard, nil)), false)
					b := &fakeBackend{
						app:  App{ID: "grpc-writer", AccountID: "grpc-account", Plan: api.PlanScale, AppProtocol: api.AppProtocolGRPC, StreamingEnabled: streaming},
						host: "app.example.com", upstream: backend.Listener.Addr().String(), running: true,
					}
					b.setLegacyHot()
					h := NewHandlerWith(b, NewMetrics(), slog.New(slog.NewTextHandler(io.Discard, nil)))
					h.WithStreamingEnabled(true)
					h.WithForwarding(func(Target) http.Handler { return proxy })
					edge := httptest.NewServer(h)
					defer edge.Close()
					body, upload := io.Pipe()
					defer upload.Close()
					ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
					defer cancel()
					stopClose := context.AfterFunc(ctx, func() { _ = upload.CloseWithError(ctx.Err()) })
					defer stopClose()
					request, err := http.NewRequestWithContext(ctx, http.MethodPost, edge.URL+"/audit.Echo/Bidi", body)
					if err != nil {
						t.Fatal(err)
					}
					request.Host = "app.example.com"
					request.Header.Set("Content-Type", contentType)
					sent := make(chan error, 1)
					go func() { _, err := upload.Write([]byte("ping")); sent <- err }()
					response, err := edge.Client().Do(request)
					if err != nil {
						t.Fatalf("gRPC response blocked until request EOF: %v", err)
					}
					defer response.Body.Close()
					prefix := make([]byte, 4)
					if _, err := io.ReadFull(response.Body, prefix); err != nil || string(prefix) != "ping" {
						t.Fatalf("first response before request EOF = %q, err=%v", prefix, err)
					}
					if err := <-sent; err != nil {
						t.Fatal(err)
					}
					if err := upload.Close(); err != nil {
						t.Fatal(err)
					}
					if _, err := io.Copy(io.Discard, response.Body); err != nil {
						t.Fatal(err)
					}
					if response.Trailer.Get("Grpc-Status") != "0" {
						t.Fatalf("trailers=%v", response.Trailer)
					}
				})
			}
		})
	}
}

func TestHandlerOrdinaryBodiesWaitForRequestEOF(t *testing.T) {
	for _, tc := range []struct {
		name, protocol, mediaType string
	}{
		{"HTTP app with gRPC header", api.AppProtocolHTTP1, "application/grpc"},
		{"gRPC web media", api.AppProtocolGRPC, "application/grpc-web"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			started := make(chan struct{}, 1)
			backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				prefix := make([]byte, 4)
				if _, err := io.ReadFull(r.Body, prefix); err != nil {
					return
				}
				started <- struct{}{}
				_, _ = io.Copy(io.Discard, r.Body)
				_, _ = w.Write(prefix)
			}))
			defer backend.Close()
			b := &fakeBackend{
				app:  App{ID: "ordinary-upload", Plan: api.PlanScale, AppProtocol: tc.protocol},
				host: "app.example.com", upstream: backend.Listener.Addr().String(), running: true,
			}
			b.setLegacyHot()
			h := NewHandlerWith(b, NewMetrics(), slog.New(slog.NewTextHandler(io.Discard, nil)))
			h.WithForwarding(func(Target) http.Handler {
				return NewInternalReverseProxy(&stubDialer{server: backend}, &url.URL{Scheme: "http", Host: "internal"}, nil, false)
			})
			edge := httptest.NewServer(h)
			defer edge.Close()
			body, upload := io.Pipe()
			defer upload.Close()
			ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
			defer cancel()
			stop := context.AfterFunc(ctx, func() { _ = upload.CloseWithError(ctx.Err()) })
			defer stop()
			req, err := http.NewRequestWithContext(ctx, http.MethodPost, edge.URL+"/ordinary", body)
			if err != nil {
				t.Fatal(err)
			}
			req.Host = "app.example.com"
			req.Header.Set("Content-Type", tc.mediaType)
			type responseResult struct {
				body []byte
				err  error
			}
			response := make(chan responseResult, 1)
			go func() {
				r, err := edge.Client().Do(req)
				if err != nil {
					response <- responseResult{err: err}
					return
				}
				defer r.Body.Close()
				body, err := io.ReadAll(r.Body)
				response <- responseResult{body: body, err: err}
			}()
			if _, err := upload.Write([]byte("ping")); err != nil {
				t.Fatal(err)
			}
			select {
			case <-started:
				t.Fatal("ordinary request dispatched before upload EOF")
			case <-time.After(100 * time.Millisecond):
			}
			_ = upload.Close()
			select {
			case resp := <-response:
				if resp.err != nil || string(resp.body) != "ping" {
					t.Fatalf("admitted response=%q err=%v", resp.body, resp.err)
				}
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
		})
	}
}
