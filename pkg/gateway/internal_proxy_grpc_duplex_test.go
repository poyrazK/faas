package gateway

// adr: 126 — a gRPC client receives replies before closing its request stream.

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"
)

func TestInternalProxyGRPCRepliesBeforeRequestEOF(t *testing.T) {
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
			edge := httptest.NewServer(proxy)
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
}
