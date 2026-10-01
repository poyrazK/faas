// adr: 423
package sched

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/executionproto"
	"github.com/onebox-faas/faas/pkg/outbound"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

func TestLoopbackExecutionOutboundRelayUsesFixedGatewayAndFiltersHeaders(t *testing.T) {
	const integrationID = "11111111-1111-4111-8111-111111111111"
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if request.URL.Scheme != "http" || request.URL.Host != executionOutboundGatewayAddress || request.URL.Path != "/i/"+integrationID+"/v1/issues" || request.URL.RawQuery != "state=open" {
			t.Fatalf("gateway target = %s", request.URL)
		}
		if request.Header.Get(outbound.ExecutionIdentityHeader) != "host-only-assertion" || request.Header.Get("Authorization") != "" {
			t.Fatalf("gateway identity headers = %#v", request.Header)
		}
		if request.Header.Get("Content-Type") != "application/json" || request.Method != http.MethodPost {
			t.Fatalf("gateway method/content-type = %s/%q", request.Method, request.Header.Get("Content-Type"))
		}
		body, err := io.ReadAll(request.Body)
		if err != nil || string(body) != `{"title":"agent"}` {
			t.Fatalf("gateway body = %q, %v", body, err)
		}
		return &http.Response{
			StatusCode: http.StatusCreated,
			Header: http.Header{
				"Content-Type":                   []string{"application/json"},
				"Set-Cookie":                     []string{"sid=secret"},
				outbound.ExecutionIdentityHeader: []string{"must-not-return"},
			},
			Body: io.NopCloser(strings.NewReader(`{"id":"item-1"}`)), Request: request,
		}, nil
	})}
	relay := &loopbackExecutionOutboundRelay{client: client}
	response, err := relay.Call(context.Background(), executionproto.OutboundRequest{
		ID: 1, IntegrationID: integrationID, Method: http.MethodPost,
		Path: "/v1/issues?state=open", Body: []byte(`{"title":"agent"}`),
	}, "host-only-assertion")
	if err != nil {
		t.Fatal(err)
	}
	if response.Status != http.StatusCreated || string(response.Body) != `{"id":"item-1"}` || len(response.Headers) != 1 || response.Headers["content-type"] != "application/json" {
		t.Fatalf("safe provider response = %+v", response)
	}
}

func TestLoopbackExecutionOutboundRelayRejectsOversizedResponse(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(strings.Repeat("x", executionproto.MaxOutboundBodyBytes+1))), Request: request}, nil
	})}
	relay := &loopbackExecutionOutboundRelay{client: client}
	_, err := relay.Call(context.Background(), executionproto.OutboundRequest{
		ID: 1, IntegrationID: "11111111-1111-4111-8111-111111111111", Method: http.MethodGet, Path: "/v1/items",
	}, "host-only-assertion")
	if err == nil {
		t.Fatal("oversized response was accepted")
	}
}
