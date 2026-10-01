package executor

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/executionproto"
)

type outboundBrokerFunc func(context.Context, executionproto.OutboundRequest) (executionproto.OutboundResponse, error)

func (f outboundBrokerFunc) Call(ctx context.Context, request executionproto.OutboundRequest) (executionproto.OutboundResponse, error) {
	return f(ctx, request)
}

func TestExecutionOutboundHelperExposesSameContractToNodeAndPython(t *testing.T) {
	const integrationID = "11111111-1111-4111-8111-111111111111"
	cases := []struct {
		name    string
		runtime api.ExecutionRuntime
		source  string
	}{
		{
			name:    "node",
			runtime: api.ExecutionRuntimeNode22,
			source: fmt.Sprintf(`export default async (_, context) => {
  const response = await context.outbound.request(%q, {method: "POST", path: "/v1/items", body: {name: "agent"}});
  return {status: response.status, body: JSON.parse(response.body)};
};`, integrationID),
		},
		{
			name:    "python",
			runtime: api.ExecutionRuntimePython313,
			source:  fmt.Sprintf("import json\ndef main(input, context):\n    response = context['outbound']['request'](%q, method='POST', path='/v1/items', body={'name': 'agent'})\n    return {'status': response['status'], 'body': json.loads(response['body'])}\n", integrationID),
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			if _, err := exec.LookPath(map[api.ExecutionRuntime]string{
				api.ExecutionRuntimeNode22: "node", api.ExecutionRuntimePython313: "python3",
			}[testCase.runtime]); err != nil {
				t.Skip("runtime executable is unavailable")
			}
			calls := 0
			host, guest := net.Pipe()
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			serveDone := make(chan error, 1)
			go func() {
				serveDone <- executionproto.ServeWithBroker(ctx, guest, func(ctx context.Context, request executionproto.Request, stdout, stderr *executionproto.OutputWriter, guestBroker executionproto.OutboundBroker) (executionproto.Result, error) {
					return New().HandleWithBroker(ctx, request, stdout, stderr, guestBroker)
				})
			}()
			broker := func(_ context.Context, request executionproto.OutboundRequest) (executionproto.OutboundResponse, error) {
				calls++
				if request.IntegrationID != integrationID || request.Method != "POST" || request.Path != "/v1/items" || string(request.Body) != `{"name":"agent"}` {
					return executionproto.OutboundResponse{}, fmt.Errorf("unexpected broker request: %+v", request)
				}
				return executionproto.OutboundResponse{ID: request.ID, Status: 200, Headers: map[string]string{"content-type": "application/json"}, Body: []byte(`{"id":"item-1"}`)}, nil
			}
			client, err := executionproto.NewClient(host)
			if err != nil {
				t.Fatal(err)
			}
			result, err := client.ExecuteWithOutputAndBroker(ctx, executionproto.Request{
				Version: executionproto.Version, ExecutionID: "agent-run", Runtime: testCase.runtime,
				Source: testCase.source, Input: json.RawMessage("null"), TimeoutMS: 5_000,
				MaxOutput: 4 << 10, NetworkMode: api.ExecutionNetworkNone, OutboundEnabled: true,
			}, nil, broker)
			if err != nil {
				t.Fatalf("execution failed: %v; guest result: %+v", err, result)
			}
			if result.Status != api.ExecutionStatusSucceeded || string(result.Result) != `{"status":200,"body":{"id":"item-1"}}` || calls != 1 {
				t.Fatalf("result/calls = %+v / %d", result, calls)
			}
			_ = host.Close()
			_ = guest.Close()
			if err := <-serveDone; err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestExecutionOutboundHelperRejectsArbitraryURLAndOversizedBody(t *testing.T) {
	const integrationID = "11111111-1111-4111-8111-111111111111"
	for _, path := range []string{"https://provider.example/v1", "//provider.example/v1", "/../admin"} {
		t.Run(strings.ReplaceAll(path, "/", "_"), func(t *testing.T) {
			called := false
			broker := outboundBrokerFunc(func(context.Context, executionproto.OutboundRequest) (executionproto.OutboundResponse, error) {
				called = true
				return executionproto.OutboundResponse{}, nil
			})
			response := invokeOutboundHelper(t, broker, integrationID, path, []byte(`{}`))
			if response.StatusCode == 200 || called {
				t.Fatalf("unsafe path reached broker: status=%d called=%t", response.StatusCode, called)
			}
		})
	}
	called := false
	broker := outboundBrokerFunc(func(context.Context, executionproto.OutboundRequest) (executionproto.OutboundResponse, error) {
		called = true
		return executionproto.OutboundResponse{}, nil
	})
	response := invokeOutboundHelper(t, broker, integrationID, "/v1/items", make([]byte, executionproto.MaxOutboundBodyBytes+1))
	if response.StatusCode != 413 || called {
		t.Fatalf("oversized body result = status=%d called=%t", response.StatusCode, called)
	}
}

type helperResponse struct{ StatusCode int }

func invokeOutboundHelper(t *testing.T, broker executionproto.OutboundBroker, integrationID, path string, body []byte) helperResponse {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	helper, err := startExecutionOutboundHelper(ctx, broker)
	if err != nil {
		t.Fatal(err)
	}
	defer helper.Close()
	request, err := http.NewRequest(http.MethodPost, helper.endpoint+"/i/"+integrationID+path, bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	response, err := (&http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}).Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	return helperResponse{StatusCode: response.StatusCode}
}
