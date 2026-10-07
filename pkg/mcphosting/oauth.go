package mcphosting

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

// VerifyOAuth checks the public challenge and resource metadata without
// forwarding any credentials. It never follows provider discovery URLs.
func (c *Client) VerifyOAuth(ctx context.Context) error {
	unauth, err := NewClient(c.Endpoint, "", ProtocolVersion)
	if err != nil {
		return err
	}
	unauth.HTTP = c.HTTP
	u, _ := url.Parse(c.Endpoint)
	metadataURL := *u
	metadataURL.Path = "/.well-known/oauth-protected-resource" + u.Path
	metadataURL.RawPath = ""
	// Candidate deployments keep the production OAuth audience. Fetch metadata
	// from the candidate transport, while checking its canonical resource identity.
	resource := c.Endpoint
	if c.ExpectedAuth != nil && c.ExpectedAuth.Resource != "" {
		resource = c.ExpectedAuth.Resource
	}
	canonical, err := url.Parse(resource)
	if err != nil || canonical.Host == "" {
		return fmt.Errorf("invalid canonical OAuth resource")
	}
	challengeURL := *canonical
	challengeURL.Path = "/.well-known/oauth-protected-resource" + canonical.Path
	challengeURL.RawPath = ""
	x, _ := unauth.request(ctx, "server/discover", nil, nil, false)
	scheme, _, _ := strings.Cut(x.AuthChallenge, " ")
	if x.HTTPStatus != http.StatusUnauthorized || !strings.EqualFold(scheme, "Bearer") || !strings.Contains(x.AuthChallenge, `resource_metadata="`+challengeURL.String()+`"`) {
		return fmt.Errorf("OAuth endpoint must return 401 with canonical protected-resource metadata in WWW-Authenticate")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, metadataURL.String(), nil)
	if err != nil {
		return fmt.Errorf("create metadata probe: %w", err)
	}
	res, err := unauth.HTTP.Do(req)
	if err != nil {
		return fmt.Errorf("read protected-resource metadata: %w", err)
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("protected-resource metadata returned HTTP %d", res.StatusCode)
	}
	var metadata struct {
		Resource string   `json:"resource"`
		Servers  []string `json:"authorization_servers"`
		Scopes   []string `json:"scopes_supported"`
	}
	body, err := io.ReadAll(io.LimitReader(res.Body, (64<<10)+1))
	if err != nil {
		return fmt.Errorf("read resource metadata: %w", err)
	}
	if len(body) > 64<<10 || json.Unmarshal(body, &metadata) != nil {
		return fmt.Errorf("invalid protected-resource metadata")
	}
	if metadata.Resource != resource || len(metadata.Servers) == 0 {
		return fmt.Errorf("protected-resource metadata must identify this endpoint and an authorization server")
	}
	for _, issuer := range metadata.Servers {
		u, err := url.Parse(issuer)
		if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
			return fmt.Errorf("authorization server must be an HTTPS issuer URL")
		}
	}
	if expected := c.ExpectedAuth; expected != nil {
		found := false
		for _, issuer := range metadata.Servers {
			if issuer == expected.Issuer {
				found = true
			}
		}
		if !found {
			return fmt.Errorf("resource metadata does not advertise the configured issuer")
		}
		for _, scope := range expected.Scopes {
			found = false
			for _, advertised := range metadata.Scopes {
				if advertised == scope {
					found = true
				}
			}
			if !found {
				return fmt.Errorf("resource metadata does not advertise the configured scopes")
			}
		}
	}
	// Metadata and a missing-token challenge do not prove that bearer tokens
	// are validated. Probe discovery only, with a known malformed credential;
	// never send the caller's real token to metadata or execute a tool.
	invalid, err := NewClient(c.Endpoint, "gregale-invalid-bearer-probe", ProtocolVersion)
	if err != nil {
		return err
	}
	invalid.HTTP = c.HTTP
	x, _ = invalid.request(ctx, "server/discover", nil, nil, false)
	if x.HTTPStatus != http.StatusUnauthorized {
		return fmt.Errorf("OAuth endpoint must reject malformed bearer credentials with HTTP 401")
	}
	return nil
}
