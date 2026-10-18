package mcphosting

import (
	"context"
	"crypto/ecdh"
	"encoding/base64"
	"encoding/json"
	"io"
	"math/big"
	"net/http"
	"net/url"
	"strings"
)

// CheckOAuthProvider follows only the explicit, validated source configuration.
// It sends no bearer credentials, follows no redirects, and bounds each body.
// Public metadata does not establish real token issuance or token authorization.
func CheckOAuthProvider(ctx context.Context, auth AuthConfig) []Check {
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	return checkOAuthProvider(ctx, client, auth)
}

func checkOAuthProvider(ctx context.Context, client *http.Client, auth AuthConfig) []Check {
	checks := []Check{}
	add := func(name string, ok bool, detail string) {
		state := "failed"
		if ok {
			state = "passed"
		}
		checks = append(checks, Check{name, state, detail})
	}
	if !doctorHTTPS(auth.Issuer) || !doctorHTTPS(auth.JWKSURL) {
		add("oauth_provider_configuration", false, "Issuer and JWKS must be explicit HTTPS URLs without credentials, query or fragment.")
		return checks
	}
	// OIDC discovery preserves issuer path components as defined by OIDC Discovery.
	var metadata struct {
		Issuer string `json:"issuer"`
		JWKS   string `json:"jwks_uri"`
	}
	discovered := doctorPublicJSON(ctx, client, strings.TrimSuffix(auth.Issuer, "/")+"/.well-known/openid-configuration", &metadata)
	if !discovered && ctx.Err() == nil {
		metadata.Issuer, metadata.JWKS = "", ""
		issuer, _ := url.Parse(auth.Issuer)
		issuer.Path = "/.well-known/oauth-authorization-server" + strings.TrimSuffix(issuer.Path, "/")
		issuer.RawPath = ""
		discovered = doctorPublicJSON(ctx, client, issuer.String(), &metadata)
	}
	ok := discovered && metadata.Issuer == auth.Issuer && metadata.JWKS == auth.JWKSURL
	add("oauth_provider_discovery", ok, "OIDC or OAuth authorization-server discovery must match the reviewed issuer and JWKS URL.")
	var keys struct {
		Keys []struct {
			Kty string `json:"kty"`
			Use string `json:"use"`
			Alg string `json:"alg"`
			N   string `json:"n"`
			E   string `json:"e"`
			D   string `json:"d"`
			P   string `json:"p"`
			Q   string `json:"q"`
			DP  string `json:"dp"`
			DQ  string `json:"dq"`
			QI  string `json:"qi"`
			K   string `json:"k"`
			Crv string `json:"crv"`
			X   string `json:"x"`
			Y   string `json:"y"`
		} `json:"keys"`
	}
	ok = doctorPublicJSON(ctx, client, auth.JWKSURL, &keys)
	usable := false
	for _, key := range keys.Keys {
		if key.D != "" || key.K != "" || key.P != "" || key.Q != "" || key.DP != "" || key.DQ != "" || key.QI != "" {
			ok = false
		}
		if key.Use == "" || key.Use == "sig" {
			if key.Kty == "RSA" && (key.Alg == "" || key.Alg == "RS256") && doctorRSAKey(key.N, key.E) {
				usable = true
			}
			if key.Kty == "EC" && key.Crv == "P-256" && (key.Alg == "" || key.Alg == "ES256") && doctorECKey(key.X, key.Y) {
				usable = true
			}
		}
	}
	add("oauth_signing_keys", ok && usable, "JWKS must expose a public signing key compatible with the starters' RS256/ES256 contract and no private key material.")
	return checks
}

func doctorHTTPS(value string) bool {
	u, err := url.Parse(value)
	return err == nil && u.Scheme == "https" && u.Host != "" && u.User == nil && u.RawQuery == "" && u.Fragment == ""
}
func doctorPublicJSON(ctx context.Context, client *http.Client, endpoint string, destination any) bool {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return false
	}
	response, err := client.Do(req)
	if err != nil {
		return false
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		return false
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, (64<<10)+1))
	return err == nil && len(body) <= 64<<10 && json.Unmarshal(body, destination) == nil
}

func doctorRSAKey(n, e string) bool {
	modulus, err := base64.RawURLEncoding.DecodeString(n)
	if err != nil || new(big.Int).SetBytes(modulus).BitLen() < 2048 {
		return false
	}
	exponent, err := base64.RawURLEncoding.DecodeString(e)
	if err != nil || len(exponent) == 0 || len(exponent) > 4 {
		return false
	}
	value := new(big.Int).SetBytes(exponent).Uint64()
	return value >= 3 && value%2 == 1
}

func doctorECKey(x, y string) bool {
	xb, err := base64.RawURLEncoding.DecodeString(x)
	if err != nil || len(xb) != 32 {
		return false
	}
	yb, err := base64.RawURLEncoding.DecodeString(y)
	if err != nil || len(yb) != 32 {
		return false
	}
	encoded := append([]byte{4}, xb...)
	encoded = append(encoded, yb...)
	_, err = ecdh.P256().NewPublicKey(encoded)
	return err == nil
}
