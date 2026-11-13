package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestWaitHTTPBoundsStalledResponse(t *testing.T) {
	for _, headersSent := range []bool{false, true} {
		t.Run(map[bool]string{false: "headers", true: "body"}[headersSent], func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if headersSent {
					w.WriteHeader(http.StatusOK)
					w.(http.Flusher).Flush()
				}
				<-r.Context().Done()
			}))
			defer server.Close()
			runtime := hostRuntime{readyTimeout: 30 * time.Millisecond}
			done := make(chan error, 1)
			go func() { done <- runtime.waitHTTP(context.Background(), server.URL) }()
			select {
			case err := <-done:
				if err == nil {
					t.Fatal("a stalled readiness request succeeded")
				}
			case <-time.After(time.Second):
				t.Fatal("readiness deadline did not interrupt HTTP")
			}
		})
	}
}
