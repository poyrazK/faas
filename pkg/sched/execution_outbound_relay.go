package sched

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/executionproto"
	"github.com/onebox-faas/faas/pkg/outbound"
)

const executionOutboundGatewayAddress = "127.0.0.1:8095"

// ExecutionOutboundRelay sends one already-validated guest request through
// schedd's private loopback connection to outboundd. The assertion is
// host-only and is never copied to the guest response.
type ExecutionOutboundRelay interface {
	Call(context.Context, executionproto.OutboundRequest, string) (executionproto.OutboundResponse, error)
}

type loopbackExecutionOutboundRelay struct{ client *http.Client }

// NewLoopbackExecutionOutboundRelay creates a relay that can dial only the
// local outboundd listener. It deliberately ignores proxy environment
// variables and refuses redirects so the integration path never becomes an
// arbitrary URL fetcher.
func NewLoopbackExecutionOutboundRelay() ExecutionOutboundRelay {
	dialer := &net.Dialer{Timeout: 3 * time.Second, KeepAlive: 30 * time.Second}
	transport := &http.Transport{
		Proxy: nil,
		DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
			if address != executionOutboundGatewayAddress {
				return nil, errors.New("scheduler outbound relay rejected a non-loopback target")
			}
			return dialer.DialContext(ctx, network, executionOutboundGatewayAddress)
		},
		DisableKeepAlives: false,
	}
	return &loopbackExecutionOutboundRelay{client: &http.Client{
		Transport:     transport,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}}
}

func cloneOutboundHeaders(source map[string]string) map[string]string {
	if len(source) == 0 {
		return nil
	}
	copyHeaders := make(map[string]string, len(source))
	for key, value := range source {
		copyHeaders[key] = value
	}
	return copyHeaders
}

func (r *loopbackExecutionOutboundRelay) Call(ctx context.Context, request executionproto.OutboundRequest, assertion string) (executionproto.OutboundResponse, error) {
	var zero executionproto.OutboundResponse
	if r == nil || r.client == nil || assertion == "" || len(assertion) > 8192 {
		return zero, errors.New("scheduler outbound relay is unavailable")
	}
	if err := request.Validate(); err != nil {
		return zero, errors.New("scheduler outbound request is invalid")
	}
	target := "http://" + executionOutboundGatewayAddress + outbound.Prefix + request.IntegrationID + request.Path
	httpRequest, err := http.NewRequestWithContext(ctx, request.Method, target, bytes.NewReader(request.Body))
	if err != nil {
		return zero, errors.New("scheduler outbound request could not be constructed")
	}
	httpRequest.Header.Set(outbound.ExecutionIdentityHeader, assertion)
	if len(request.Body) != 0 {
		httpRequest.Header.Set("Content-Type", "application/json")
	}
	response, err := r.client.Do(httpRequest)
	if err != nil {
		return zero, errors.New("outbound integration request failed")
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, executionproto.MaxOutboundBodyBytes+1))
	if err != nil || len(body) > executionproto.MaxOutboundBodyBytes {
		return zero, errors.New("outbound integration response exceeds the Runs limit")
	}
	headers := make(map[string]string)
	for _, name := range []string{"Content-Type", "Cache-Control", "ETag", "Last-Modified", "Retry-After", "X-Request-Id"} {
		values := response.Header.Values(name)
		if len(values) == 1 && safeExecutionOutboundHeaderValue(values[0]) {
			headers[strings.ToLower(name)] = values[0]
		}
	}
	out := executionproto.OutboundResponse{ID: request.ID, Status: response.StatusCode, Headers: headers, Body: body}
	if err := out.Validate(); err != nil {
		return zero, fmt.Errorf("outbound integration response is invalid: %w", err)
	}
	return out, nil
}

func safeExecutionOutboundHeaderValue(value string) bool {
	if strings.TrimSpace(value) != value {
		return false
	}
	for i := range len(value) {
		if value[i] < 0x20 || value[i] > 0x7e {
			return false
		}
	}
	return true
}
