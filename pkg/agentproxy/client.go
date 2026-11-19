package agentproxy

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
)

// Client is vmmd's side of the registration API: it reaches the gateway's
// tunnel address over the node's wg-agent route, so the WireGuard peer
// vouches for the node (ADR-20261117185346 §4).
type Client struct {
	// BaseURL is the registration listener, http://<gateway tunnel address>:<port>.
	BaseURL string
	HTTP    *http.Client
}

// NewClient checks base is a plain-HTTP URL on an IPv4 address: the tunnel
// authenticates the node, and a name would need DNS the tunnel does not
// carry.
func NewClient(base string, hc *http.Client) (*Client, error) {
	u, err := url.Parse(strings.TrimSpace(base))
	if err != nil {
		return nil, fmt.Errorf("agent proxy URL %q: %w", base, err)
	}
	addr, err := netip.ParseAddr(u.Hostname())
	if u.Scheme != "http" || err != nil || !addr.Is4() || u.Port() == "" || (u.Path != "" && u.Path != "/") {
		return nil, fmt.Errorf("agent proxy URL %q must be http://<IPv4 tunnel address>:<port>", base)
	}
	if hc == nil {
		hc = http.DefaultClient
	}
	return &Client{BaseURL: "http://" + u.Host, HTTP: hc}, nil
}

// Register adds or renews addr's registration.
func (c *Client) Register(ctx context.Context, addr netip.Addr, accountID, sandboxID string) error {
	body, err := json.Marshal(RegistrationRequest{AccountID: accountID, SandboxID: sandboxID})
	if err != nil {
		return fmt.Errorf("encode registration: %w", err)
	}
	return c.do(ctx, http.MethodPut, addr, body, http.StatusOK)
}

// Unregister drops addr's registration.
func (c *Client) Unregister(ctx context.Context, addr netip.Addr) error {
	return c.do(ctx, http.MethodDelete, addr, nil, http.StatusNoContent)
}

func (c *Client) do(ctx context.Context, method string, addr netip.Addr, body []byte, want int) error {
	req, err := http.NewRequestWithContext(ctx, method, c.BaseURL+"/v1/registrations/"+addr.String(), bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("agent proxy %s %s: %w", method, addr, err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return fmt.Errorf("agent proxy %s %s: %w", method, addr, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != want {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("agent proxy %s %s: %s: %s", method, addr, resp.Status, strings.TrimSpace(string(msg)))
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4<<10))
	return nil
}
