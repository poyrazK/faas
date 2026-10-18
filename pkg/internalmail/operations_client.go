package internalmail

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// OperatorClient is Unix-only, follows no redirects, uses no HTTP proxy, and
// never retries a mutation automatically. Persist request IDs before writes.
type OperatorClient struct {
	token   string
	enabled func() bool
	client  *http.Client
}

func NewOperatorClient(socket, token string, enabled func() bool) (*OperatorClient, error) {
	if enabled == nil || !enabled() {
		return nil, ErrDisabled
	}
	if !filepath.IsAbs(socket) || filepath.Clean(socket) != socket || !validToken(token) {
		return nil, ErrInvalid
	}
	transport := &http.Transport{DisableKeepAlives: true, DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", socket)
	}}
	return &OperatorClient{token: token, enabled: enabled, client: &http.Client{Transport: transport, Timeout: api.InternalMailAPITimeout + 5*time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}, nil
}

func (c *OperatorClient) Request(ctx context.Context, method, route string, input any) (json.RawMessage, error) {
	if !c.enabled() {
		return nil, ErrDisabled
	}
	u, err := url.ParseRequestURI(route)
	if err != nil || u.IsAbs() || u.Host != "" || !strings.HasPrefix(u.Path, OperationsPath+"/") || strings.Contains(u.Path, "..") || method != http.MethodGet && method != http.MethodPost {
		return nil, ErrInvalid
	}
	var body []byte
	if input != nil {
		body, err = json.Marshal(input)
		if err != nil {
			return nil, ErrInvalid
		}
	}
	req, err := http.NewRequestWithContext(ctx, method, "http://maild"+route, bytes.NewReader(body))
	if err != nil {
		return nil, ErrInvalid
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	if input != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if !c.enabled() {
		return nil, ErrDisabled
	}
	response, err := c.client.Do(req)
	if err != nil {
		return nil, errors.New("internal mail operator service unavailable; inspect audit before retrying a write")
	}
	defer func() { _ = response.Body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(response.Body, api.InternalMailMaxOperatorResponseBytes+1))
	if err != nil || len(raw) > api.InternalMailMaxOperatorResponseBytes {
		return nil, errors.New("internal mail operator response unreadable; inspect audit before retrying a write")
	}
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("internal mail operator request failed (HTTP %d)", response.StatusCode)
	}
	if !json.Valid(raw) {
		return nil, errors.New("internal mail operator response is not JSON")
	}
	return json.RawMessage(raw), nil
}
