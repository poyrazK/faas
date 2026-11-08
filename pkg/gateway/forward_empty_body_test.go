package gateway_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/gateway"
)

// A bodiless request ends the stream's request half right after its init
// frame, with no body frames; a request with a body still streams it.
func TestForwardingReverseProxy_RequestBodyFrames(t *testing.T) {
	for _, tc := range []struct {
		name       string
		method     string
		body       string
		wantChunks string
	}{
		{"bodiless GET", http.MethodGet, "", ""},
		{"POST with a body", http.MethodPost, "payload", "payload"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			stream := noContentHTTPStream()
			proxy := gateway.ForwardingReverseProxy(&fakeNodeLookup{cli: &fakeVmmdClient{Stream: stream}}, nil)
			r := httptest.NewRequest(tc.method, "http://app.test/", strings.NewReader(tc.body))
			if tc.body == "" {
				r.Body = http.NoBody
			}
			r.Header.Set("x-faas-instance", "i-test")
			rec := httptest.NewRecorder()
			proxy(gateway.Target{NodeID: "node-1", InstanceID: "i-test"}).ServeHTTP(rec, r)
			if rec.Code != http.StatusNoContent {
				t.Fatalf("status = %d, want 204", rec.Code)
			}
			stream.mu.Lock()
			defer stream.mu.Unlock()
			if !stream.closed {
				t.Fatal("the request half was never closed")
			}
			var chunks strings.Builder
			for _, frame := range stream.Sends[1:] {
				chunks.Write(frame.GetBodyChunk())
			}
			if chunks.String() != tc.wantChunks || (tc.body == "" && len(stream.Sends) != 1) {
				t.Fatalf("sent %d frames with body %q, want body %q", len(stream.Sends), chunks.String(), tc.wantChunks)
			}
			if init := stream.Sends[0].GetInit(); init.GetContentLength() != int64(len(tc.body)) || !init.GetContentLengthKnown() {
				t.Fatalf("init declared length %d known=%t, want %d known", init.GetContentLength(), init.GetContentLengthKnown(), len(tc.body))
			}
		})
	}
}
