// spec: §4.1
package gateway_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/gateway"
)

func TestForwardingReverseProxy_StripsConnectionOptionsBothDirections(t *testing.T) {
	stream := &fakeBidiStream{Responses: []*vmmdpb.ForwardHTTPStreamResponse{
		{Frame: &vmmdpb.ForwardHTTPStreamResponse_Init{Init: &vmmdpb.ForwardHTTPResponseInit{
			Status: http.StatusOK,
			Headers: []*vmmdpb.Header{
				{Name: "X-Response-Hop-One", Value: "remove"},
				{Name: "Connection", Value: "x-response-hop-one"},
				{Name: "Connection", Value: "X-Response-Hop-Two"},
				{Name: "X-Response-Hop-Two", Value: "remove"},
				{Name: "X-Custom", Value: "keep"},
			},
		}}},
		{Frame: &vmmdpb.ForwardHTTPStreamResponse_BodyChunk{BodyChunk: []byte("payload")}},
	}}
	lookup := &fakeNodeLookup{cli: &fakeVmmdClient{Stream: stream}}
	proxy := gateway.ForwardingReverseProxy(lookup, nil)
	r := httptest.NewRequest(http.MethodGet, "http://app.example/", nil)
	r.Header.Add("Connection", "X-Request-Hop-One")
	r.Header.Add("Connection", "x-request-hop-two")
	r.Header.Set("X-Request-Hop-One", "remove")
	r.Header.Set("X-Request-Hop-Two", "remove")
	r.Header.Set("X-Custom", "keep")
	w := httptest.NewRecorder()
	proxy(gateway.Target{NodeID: "node-1"}).ServeHTTP(w, r)
	if w.Code != http.StatusOK || w.Body.String() != "payload" {
		t.Fatalf("forwarder response = %d %q", w.Code, w.Body.String())
	}
	if len(stream.Sends) == 0 || stream.Sends[0].GetInit() == nil {
		t.Fatal("forwarder did not send a request")
	}
	requestHeaders := make(http.Header)
	for _, header := range stream.Sends[0].GetInit().GetHeaders() {
		requestHeaders.Add(header.GetName(), header.GetValue())
	}
	for name, header := range map[string]http.Header{"request": requestHeaders, "response": w.Header()} {
		for _, key := range []string{"Connection", "X-Request-Hop-One", "X-Request-Hop-Two", "X-Response-Hop-One", "X-Response-Hop-Two"} {
			if header.Get(key) != "" {
				t.Errorf("%s forwarded hop-specific header %s", name, key)
			}
		}
		if header.Get("X-Custom") != "keep" {
			t.Errorf("%s lost the end-to-end header", name)
		}
	}
}
