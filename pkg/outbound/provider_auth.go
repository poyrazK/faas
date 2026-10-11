package outbound

import (
	"context"
	"crypto"
	"crypto/hmac"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

var errProviderCredential = errors.New("outbound provider credential is unusable")

// applyProviderCredential places a managed credential on an upstream
// request in the provider's own scheme (ADR-984). body is the exact body
// sent, needed for AWS signing. The credential is never logged or returned.
func (h *Handler) applyProviderCredential(ctx context.Context, req *http.Request, integration Integration, credential string, body []byte) error {
	switch integration.AIProvider {
	case api.OutboundAIProviderAnthropic:
		req.Header.Del("Authorization")
		req.Header.Set("X-Api-Key", strings.TrimPrefix(credential, "Bearer "))
	case api.OutboundAIProviderGemini:
		req.Header.Del("Authorization")
		req.Header.Set("X-Goog-Api-Key", strings.TrimPrefix(credential, "Bearer "))
	case api.OutboundAIProviderAzureOpenAI:
		req.Header.Del("Authorization")
		req.Header.Del("Api-Key")
		if strings.HasPrefix(credential, "Bearer ") {
			req.Header.Set("Authorization", credential)
		} else {
			req.Header.Set("Api-Key", credential)
		}
	case api.OutboundAIProviderBedrock:
		key, err := parseAWSCredential(credential)
		if err != nil {
			return err
		}
		return signAWSRequest(req, body, key, "bedrock", time.Now().UTC())
	case api.OutboundAIProviderVertex:
		token, err := h.vertexAccessToken(ctx, integration.ID, credential)
		if err != nil {
			return err
		}
		req.Header.Set("Authorization", "Bearer "+token)
	default:
		req.Header.Set("Authorization", credential)
	}
	return nil
}

// providerNeedsBufferedBody reports providers whose credential covers the
// body (ADR-984): AWS signatures hash it.
func providerNeedsBufferedBody(provider string) bool {
	return provider == api.OutboundAIProviderBedrock
}

type awsCredential struct {
	accessKeyID, secretAccessKey, sessionToken string
}

// parseAWSCredential reads "aws:<access-key-id>:<secret>[:<session-token>]".
func parseAWSCredential(value string) (awsCredential, error) {
	rest, ok := strings.CutPrefix(value, "aws:")
	if !ok {
		return awsCredential{}, errProviderCredential
	}
	parts := strings.SplitN(rest, ":", 3)
	if len(parts) < 2 || parts[0] == "" || parts[1] == "" {
		return awsCredential{}, errProviderCredential
	}
	key := awsCredential{accessKeyID: parts[0], secretAccessKey: parts[1]}
	if len(parts) == 3 {
		key.sessionToken = parts[2]
	}
	return key, nil
}

// awsRegionFromHost reads the region of a bedrock-runtime endpoint.
func awsRegionFromHost(host string) (string, bool) {
	host = strings.ToLower(host)
	if h, _, found := strings.Cut(host, ":"); found {
		host = h
	}
	for _, prefix := range []string{"bedrock-runtime.", "bedrock-runtime-fips."} {
		if rest, ok := strings.CutPrefix(host, prefix); ok {
			region, suffix, found := strings.Cut(rest, ".")
			if found && region != "" && (suffix == "amazonaws.com" || suffix == "amazonaws.com.cn") {
				return region, true
			}
		}
	}
	return "", false
}

// signAWSRequest adds an AWS Signature Version 4 Authorization header.
// Caller-sent Authorization and X-Amz-* headers are removed first so the
// caller cannot influence what is signed.
func signAWSRequest(req *http.Request, body []byte, key awsCredential, service string, now time.Time) error {
	region, ok := awsRegionFromHost(req.URL.Host)
	if !ok {
		return errProviderCredential
	}
	return signAWSRequestRegion(req, body, key, service, region, now)
}

func signAWSRequestRegion(req *http.Request, body []byte, key awsCredential, service, region string, now time.Time) error {
	req.Header.Del("Authorization")
	for name := range req.Header {
		if strings.HasPrefix(strings.ToLower(name), "x-amz-") {
			req.Header.Del(name)
		}
	}
	amzDate := now.Format("20060102T150405Z")
	date := now.Format("20060102")
	payloadHash := sha256Hex(body)
	req.Header.Set("X-Amz-Date", amzDate)
	if key.sessionToken != "" {
		req.Header.Set("X-Amz-Security-Token", key.sessionToken)
	}
	host := req.Host
	if host == "" {
		host = req.URL.Host
	}
	signed := map[string]string{"host": host}
	for name, values := range req.Header {
		lower := strings.ToLower(name)
		if lower == "content-type" || strings.HasPrefix(lower, "x-amz-") {
			signed[lower] = strings.Join(trimAll(values), ",")
		}
	}
	names := make([]string, 0, len(signed))
	for name := range signed {
		names = append(names, name)
	}
	sort.Strings(names)
	var canonicalHeaders strings.Builder
	for _, name := range names {
		canonicalHeaders.WriteString(name + ":" + signed[name] + "\n")
	}
	signedHeaders := strings.Join(names, ";")
	canonicalRequest := strings.Join([]string{
		req.Method, awsCanonicalURI(req.URL.EscapedPath()), awsCanonicalQuery(req.URL.Query()),
		canonicalHeaders.String(), signedHeaders, payloadHash,
	}, "\n")
	scope := date + "/" + region + "/" + service + "/aws4_request"
	stringToSign := "AWS4-HMAC-SHA256\n" + amzDate + "\n" + scope + "\n" + sha256Hex([]byte(canonicalRequest))
	signingKey := hmacSHA256([]byte("AWS4"+key.secretAccessKey), date)
	for _, part := range []string{region, service, "aws4_request"} {
		signingKey = hmacSHA256(signingKey, part)
	}
	signature := hex.EncodeToString(hmacSHA256(signingKey, stringToSign))
	req.Header.Set("Authorization", "AWS4-HMAC-SHA256 Credential="+key.accessKeyID+"/"+scope+
		", SignedHeaders="+signedHeaders+", Signature="+signature)
	return nil
}

func trimAll(values []string) []string {
	out := make([]string, len(values))
	for i, value := range values {
		out[i] = strings.Join(strings.Fields(value), " ")
	}
	return out
}

// awsCanonicalURI encodes each segment of the request's escaped path once
// more, as SigV4 requires for every service except S3.
func awsCanonicalURI(escapedPath string) string {
	if escapedPath == "" {
		return "/"
	}
	segments := strings.Split(escapedPath, "/")
	for i, segment := range segments {
		segments[i] = awsURIEncode(segment)
	}
	return strings.Join(segments, "/")
}

func awsCanonicalQuery(values url.Values) string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	var parts []string
	for _, key := range keys {
		vals := append([]string(nil), values[key]...)
		sort.Strings(vals)
		for _, value := range vals {
			parts = append(parts, awsURIEncode(key)+"="+awsURIEncode(value))
		}
	}
	return strings.Join(parts, "&")
}

// awsURIEncode percent-encodes every byte except RFC 3986 unreserved ones.
func awsURIEncode(value string) string {
	var b strings.Builder
	for i := 0; i < len(value); i++ {
		c := value[i]
		if (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '-' || c == '_' || c == '.' || c == '~' {
			b.WriteByte(c)
			continue
		}
		fmt.Fprintf(&b, "%%%02X", c)
	}
	return b.String()
}

func sha256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func hmacSHA256(key []byte, data string) []byte {
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(data))
	return mac.Sum(nil)
}

// googleTokenURI is the only token endpoint a Vertex service-account key
// may name; any other value is refused (ADR-984).
const googleTokenURI = "https://oauth2.googleapis.com/token"

const vertexTokenScope = "https://www.googleapis.com/auth/cloud-platform"

type googleServiceAccount struct {
	Type         string `json:"type"`
	ClientEmail  string `json:"client_email"`
	PrivateKey   string `json:"private_key"`
	PrivateKeyID string `json:"private_key_id"`
	TokenURI     string `json:"token_uri"`
}

type vertexToken struct {
	value   string
	expires time.Time
}

// vertexTokenCache keeps exchanged access tokens per integration and key.
type vertexTokenCache struct {
	mu     sync.Mutex
	tokens map[string]vertexToken
}

// vertexAccessToken returns a Google access token for a Vertex credential:
// a literal "Bearer" token, or one exchanged from a service-account key and
// cached until five minutes before it expires.
func (h *Handler) vertexAccessToken(ctx context.Context, integrationID, credential string) (string, error) {
	if token, ok := strings.CutPrefix(credential, "Bearer "); ok {
		return token, nil
	}
	cacheKey := integrationID + ":" + sha256Hex([]byte(credential))
	now := time.Now()
	h.vertexTokens.mu.Lock()
	cached, ok := h.vertexTokens.tokens[cacheKey]
	h.vertexTokens.mu.Unlock()
	if ok && now.Before(cached.expires.Add(-5*time.Minute)) {
		return cached.value, nil
	}
	var account googleServiceAccount
	if json.Unmarshal([]byte(credential), &account) != nil || account.Type != "service_account" || account.ClientEmail == "" {
		return "", errProviderCredential
	}
	if account.TokenURI != "" && account.TokenURI != googleTokenURI {
		return "", errProviderCredential
	}
	assertion, err := googleServiceAccountAssertion(account, now)
	if err != nil {
		return "", err
	}
	token, err := exchangeGoogleAssertion(ctx, h.Client, assertion, now)
	if err != nil {
		return "", err
	}
	h.vertexTokens.mu.Lock()
	if h.vertexTokens.tokens == nil {
		h.vertexTokens.tokens = make(map[string]vertexToken)
	}
	h.vertexTokens.tokens[cacheKey] = token
	h.vertexTokens.mu.Unlock()
	return token.value, nil
}

// googleServiceAccountAssertion signs the RS256 JWT bearer assertion.
func googleServiceAccountAssertion(account googleServiceAccount, now time.Time) (string, error) {
	block, _ := pem.Decode([]byte(account.PrivateKey))
	if block == nil {
		return "", errProviderCredential
	}
	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		if rsaKey, rsaErr := x509.ParsePKCS1PrivateKey(block.Bytes); rsaErr == nil {
			parsed = rsaKey
		} else {
			return "", errProviderCredential
		}
	}
	key, ok := parsed.(*rsa.PrivateKey)
	if !ok {
		return "", errProviderCredential
	}
	header := map[string]string{"alg": "RS256", "typ": "JWT"}
	if account.PrivateKeyID != "" {
		header["kid"] = account.PrivateKeyID
	}
	claims := map[string]any{
		"iss": account.ClientEmail, "scope": vertexTokenScope, "aud": googleTokenURI,
		"iat": now.Unix(), "exp": now.Add(time.Hour).Unix(),
	}
	encodedHeader, err := json.Marshal(header)
	if err != nil {
		return "", err
	}
	encodedClaims, err := json.Marshal(claims)
	if err != nil {
		return "", err
	}
	signingInput := base64.RawURLEncoding.EncodeToString(encodedHeader) + "." + base64.RawURLEncoding.EncodeToString(encodedClaims)
	digest := sha256.Sum256([]byte(signingInput))
	signature, err := rsa.SignPKCS1v15(nil, key, crypto.SHA256, digest[:])
	if err != nil {
		return "", errProviderCredential
	}
	return signingInput + "." + base64.RawURLEncoding.EncodeToString(signature), nil
}

func exchangeGoogleAssertion(ctx context.Context, client *http.Client, assertion string, now time.Time) (vertexToken, error) {
	form := url.Values{"grant_type": {"urn:ietf:params:oauth:grant-type:jwt-bearer"}, "assertion": {assertion}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, googleTokenURI, strings.NewReader(form.Encode()))
	if err != nil {
		return vertexToken{}, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := client.Do(req)
	if err != nil {
		return vertexToken{}, fmt.Errorf("vertex token exchange: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if err != nil {
		return vertexToken{}, fmt.Errorf("vertex token exchange: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return vertexToken{}, fmt.Errorf("vertex token exchange: status %d", resp.StatusCode)
	}
	var token struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int64  `json:"expires_in"`
	}
	if json.Unmarshal(body, &token) != nil || token.AccessToken == "" || !ValidManagedAuthorization("Bearer "+token.AccessToken) {
		return vertexToken{}, errProviderCredential
	}
	lifetime := time.Duration(token.ExpiresIn) * time.Second
	if lifetime <= 0 || lifetime > time.Hour {
		lifetime = time.Hour
	}
	return vertexToken{value: token.AccessToken, expires: now.Add(lifetime)}, nil
}
