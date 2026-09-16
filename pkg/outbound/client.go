package outbound

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
)

// Client is the small opt-in adapter applications can use instead of building
// the gateway URL and headers themselves. It only targets the configured
// gateway endpoint; provider authentication headers remain application-owned
// and are forwarded by Handler.
type Client struct {
	BaseURL       string
	IntegrationID string
	Token         string
	AppID         string
	HTTP          *http.Client
}

func NewClient(baseURL, integrationID, token, appID string, httpClient *http.Client) (*Client, error) {
	base, err := url.Parse(strings.TrimRight(baseURL, "/"))
	if err != nil || base.Scheme == "" || base.Host == "" || base.User != nil || base.RawQuery != "" || base.Fragment != "" {
		return nil, errors.New("outbound client base URL must be absolute")
	}
	if integrationID == "" || token == "" || appID == "" {
		return nil, errors.New("outbound client integration ID, token, and app ID are required")
	}
	if httpClient == nil {
		httpClient = &http.Client{Transport: http.DefaultTransport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	} else {
		clone := *httpClient
		clone.Jar = nil
		clone.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
		if clone.Transport == nil {
			clone.Transport = http.DefaultTransport
		}
		httpClient = &clone
	}
	return &Client{BaseURL: strings.TrimRight(base.String(), "/"), IntegrationID: integrationID, Token: token, AppID: appID, HTTP: httpClient}, nil
}

func (c *Client) Do(ctx context.Context, method, path string, body io.Reader) (*http.Response, error) {
	if c == nil || c.HTTP == nil {
		return nil, errors.New("outbound client is nil")
	}
	if path == "" || !strings.HasPrefix(path, "/") || strings.HasPrefix(path, "//") {
		return nil, errors.New("outbound client path must begin with a single slash")
	}
	u, err := url.Parse(c.BaseURL + "/i/" + url.PathEscape(c.IntegrationID) + path)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, method, u.String(), body)
	if err != nil {
		return nil, err
	}
	req.Header.Set(TokenHeader, c.Token)
	req.Header.Set(AppHeader, c.AppID)
	return c.HTTP.Do(req)
}
