package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// mcpOIDCHTTPClient fetches an external issuer's metadata; tests replace it.
var mcpOIDCHTTPClient = &http.Client{Timeout: 10 * time.Second}

// mcpOIDCMetadataMaxBytes bounds a discovery document.
const mcpOIDCMetadataMaxBytes = 256 << 10

// mcpDiscoverJWKS reads the OpenID Connect discovery document of issuer, or
// its OAuth authorization-server metadata, and returns the key set URL. The
// document must name exactly this issuer (OpenID Connect Discovery §4.3,
// RFC 8414 §3.3), so a redirect or a mistyped tenant cannot substitute
// another issuer's keys (ADR-20261123192254).
func mcpDiscoverJWKS(ctx context.Context, issuer string) (string, error) {
	u, err := url.Parse(issuer)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return "", errors.New("--issuer must be an HTTPS URL without credentials, query or fragment")
	}
	base := strings.TrimSuffix(issuer, "/")
	candidates := []string{base + "/.well-known/openid-configuration"}
	// RFC 8414 inserts the well-known segment before the issuer's path.
	if path := strings.TrimSuffix(u.Path, "/"); path != "" {
		candidates = append(candidates, u.Scheme+"://"+u.Host+"/.well-known/oauth-authorization-server"+path)
	} else {
		candidates = append(candidates, base+"/.well-known/oauth-authorization-server")
	}
	var errs []error
	for _, candidate := range candidates {
		jwks, err := mcpFetchJWKSURI(ctx, candidate, issuer)
		if err == nil {
			return jwks, nil
		}
		errs = append(errs, err)
	}
	return "", fmt.Errorf("discover %s: %w", issuer, errors.Join(errs...))
}

func mcpFetchJWKSURI(ctx context.Context, metadataURL, issuer string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, metadataURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", "application/json")
	resp, err := mcpOIDCHTTPClient.Do(req)
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("%s answered %d", metadataURL, resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, mcpOIDCMetadataMaxBytes+1))
	if err != nil {
		return "", err
	}
	if len(body) > mcpOIDCMetadataMaxBytes {
		return "", fmt.Errorf("%s is larger than %d bytes", metadataURL, mcpOIDCMetadataMaxBytes)
	}
	var metadata struct {
		Issuer  string `json:"issuer"`
		JWKSURI string `json:"jwks_uri"`
	}
	if err := json.Unmarshal(body, &metadata); err != nil {
		return "", fmt.Errorf("%s is not JSON metadata: %w", metadataURL, err)
	}
	if metadata.Issuer != issuer {
		return "", fmt.Errorf("%s names issuer %q, not %q; pass --issuer exactly as the provider spells it", metadataURL, metadata.Issuer, issuer)
	}
	jwks, err := url.Parse(metadata.JWKSURI)
	if err != nil || jwks.Scheme != "https" || jwks.Host == "" || jwks.User != nil || jwks.Fragment != "" {
		return "", fmt.Errorf("%s has no HTTPS jwks_uri", metadataURL)
	}
	return metadata.JWKSURI, nil
}
