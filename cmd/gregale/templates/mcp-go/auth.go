package main

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"math/big"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	maxJWKSBytes       = 1 << 20
	maxBearerTokenSize = 32 << 10
)

var (
	endpointPattern    = regexp.MustCompile(`^/(?:[A-Za-z0-9._~-]+/?)*$`)
	bearerPattern      = regexp.MustCompile(`(?i)^Bearer +([^ ]+)$`)
	controlPattern     = regexp.MustCompile(`[\x00-\x20\x7f]`)
	variablePattern    = regexp.MustCompile(`\{([A-Za-z][A-Za-z0-9_.-]*)\}`)
	catalogHashPattern = regexp.MustCompile(`^[a-f0-9]{64}$`)
)

type mcpConfig struct {
	Version               int             `json:"version"`
	Endpoint              string          `json:"endpoint"`
	Transport             string          `json:"transport"`
	Mode                  string          `json:"mode"`
	Legacy                bool            `json:"legacy"`
	Auth                  json.RawMessage `json:"auth"`
	Tasks                 json.RawMessage `json:"tasks"`
	CatalogExpectedSHA256 string          `json:"-"`
}

type rawAuthConfig struct {
	Mode           string          `json:"mode"`
	Issuer         string          `json:"issuer"`
	JWKSURL        string          `json:"jwks_url"`
	Resource       string          `json:"resource"`
	Scopes         []string        `json:"scopes"`
	ToolScopes     json.RawMessage `json:"tool_scopes"`
	ResourceScopes json.RawMessage `json:"resource_scopes"`
	PromptScopes   json.RawMessage `json:"prompt_scopes"`
}

type authContext struct {
	scopes    map[string]struct{}
	subject   string
	clientID  string
	resource  string
	expiresAt float64
}

type authFailure struct {
	status int
	error  string
	scopes []string
}

type scopePolicy struct {
	configured bool
	rules      map[string][]string
	templates  []resourceScopeRule
}

type resourceScopeRule struct {
	pattern   string
	variables []string
	scopes    []string
	matcher   *regexp.Regexp
}

type authenticator struct {
	mode           string
	issuer         string
	resource       string
	endpoint       string
	metadataURL    string
	baseScopes     []string
	toolPolicy     scopePolicy
	resourcePolicy scopePolicy
	promptPolicy   scopePolicy
	keyResolver    *jwkResolver
}

func loadMCPConfig() (mcpConfig, error) {
	path := os.Getenv("MCP_CONFIG_PATH")
	if path == "" {
		path = "gregale-mcp.json"
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return mcpConfig{}, errors.New("could not read gregale-mcp.json")
	}
	var config mcpConfig
	if err := json.Unmarshal(data, &config); err != nil {
		return mcpConfig{}, errors.New("gregale-mcp.json is invalid JSON")
	}
	if config.Version != 1 || config.Transport != "streamable-http" || config.Mode != "stateless" {
		return mcpConfig{}, errors.New("unsupported MCP hosting profile")
	}
	if !endpointPattern.MatchString(config.Endpoint) || strings.Contains(config.Endpoint, "//") {
		return mcpConfig{}, errors.New("invalid MCP endpoint")
	}
	for _, part := range strings.Split(config.Endpoint, "/") {
		if part == "." || part == ".." {
			return mcpConfig{}, errors.New("invalid MCP endpoint")
		}
	}
	config.CatalogExpectedSHA256 = strings.ToLower(strings.TrimSpace(os.Getenv("MCP_CATALOG_EXPECTED_SHA256")))
	if config.CatalogExpectedSHA256 != "" && !catalogHashPattern.MatchString(config.CatalogExpectedSHA256) {
		return mcpConfig{}, errors.New("MCP_CATALOG_EXPECTED_SHA256 must be a 64-character SHA-256 hex digest")
	}
	return config, nil
}

func newAuthenticator(config mcpConfig) (*authenticator, error) {
	if len(config.Auth) == 0 || string(config.Auth) == "null" {
		return nil, errors.New("auth settings are required")
	}
	var raw rawAuthConfig
	if err := json.Unmarshal(config.Auth, &raw); err != nil {
		return nil, errors.New("auth settings are invalid")
	}
	auth := &authenticator{
		mode:       raw.Mode,
		issuer:     raw.Issuer,
		resource:   raw.Resource,
		endpoint:   config.Endpoint,
		baseScopes: raw.Scopes,
	}
	if raw.Mode != "open" && raw.Mode != "external-oauth" {
		return nil, errors.New("auth.mode must be open or external-oauth")
	}
	var err error
	if auth.toolPolicy, err = parseScopePolicy(raw.ToolScopes, "tool_scopes", raw.Mode, false); err != nil {
		return nil, err
	}
	if auth.resourcePolicy, err = parseScopePolicy(raw.ResourceScopes, "resource_scopes", raw.Mode, true); err != nil {
		return nil, err
	}
	if auth.promptPolicy, err = parseScopePolicy(raw.PromptScopes, "prompt_scopes", raw.Mode, false); err != nil {
		return nil, err
	}
	if err := validateScopeList(raw.Scopes); err != nil && len(raw.Scopes) > 0 {
		return nil, errors.New("auth.scopes must contain valid OAuth scope tokens")
	}
	if raw.Mode == "open" {
		if raw.Issuer != "" || raw.JWKSURL != "" || raw.Resource != "" || len(raw.Scopes) > 0 {
			return nil, errors.New("open auth must not contain OAuth settings")
		}
		return auth, nil
	}
	for _, value := range []string{raw.Issuer, raw.JWKSURL, raw.Resource} {
		if err := validateHTTPSURL(value); err != nil {
			return nil, err
		}
	}
	resourceURL, _ := url.Parse(raw.Resource)
	if resourceURL.Path != config.Endpoint {
		return nil, errors.New("auth.resource must identify the MCP endpoint")
	}
	if len(raw.Scopes) == 0 {
		return nil, errors.New("external-oauth requires nonempty auth.scopes")
	}
	metadataURL := *resourceURL
	metadataURL.Path = "/.well-known/oauth-protected-resource" + config.Endpoint
	metadataURL.RawPath = ""
	metadataURL.RawQuery = ""
	metadataURL.Fragment = ""
	auth.metadataURL = metadataURL.String()
	auth.keyResolver = newJWKResolver(raw.JWKSURL)
	return auth, nil
}

func parseScopePolicy(raw json.RawMessage, field, mode string, resource bool) (scopePolicy, error) {
	policy := scopePolicy{configured: len(raw) > 0, rules: make(map[string][]string)}
	if !policy.configured {
		return policy, nil
	}
	if strings.TrimSpace(string(raw)) == "null" {
		return scopePolicy{}, fmt.Errorf("%s must be an object", field)
	}
	var entries map[string]json.RawMessage
	if err := json.Unmarshal(raw, &entries); err != nil || entries == nil {
		return scopePolicy{}, fmt.Errorf("%s must be an object", field)
	}
	for name, encodedScopes := range entries {
		if resource {
			if _, err := compileResourceScopeRule(name, nil); err != nil {
				return scopePolicy{}, fmt.Errorf("%s keys must be absolute resource URIs or simple URI templates", field)
			}
		} else if !validPolicyName(name) {
			return scopePolicy{}, fmt.Errorf("%s keys must be nonempty names without whitespace or controls", field)
		}
		scopeJSON := strings.TrimSpace(string(encodedScopes))
		if len(scopeJSON) == 0 || scopeJSON[0] != '[' {
			return scopePolicy{}, fmt.Errorf("%s values must be arrays", field)
		}
		var scopes []string
		if err := json.Unmarshal(encodedScopes, &scopes); err != nil {
			return scopePolicy{}, fmt.Errorf("%s values must be arrays", field)
		}
		if err := validateScopeList(scopes); err != nil {
			return scopePolicy{}, err
		}
		if mode == "open" && len(scopes) > 0 {
			return scopePolicy{}, fmt.Errorf("scoped %s entries require external-oauth", field)
		}
		policy.rules[name] = scopes
		if resource && strings.Contains(name, "{") {
			rule, err := compileResourceScopeRule(name, scopes)
			if err != nil {
				return scopePolicy{}, fmt.Errorf("invalid resource template in %s", field)
			}
			policy.templates = append(policy.templates, rule)
		}
	}
	return policy, nil
}

func validPolicyName(name string) bool {
	return name != "" && !controlPattern.MatchString(name)
}

func compileResourceScopeRule(pattern string, scopes []string) (resourceScopeRule, error) {
	if pattern == "" || controlPattern.MatchString(pattern) {
		return resourceScopeRule{}, errors.New("invalid resource URI")
	}
	locations := variablePattern.FindAllStringSubmatchIndex(pattern, -1)
	expanded := variablePattern.ReplaceAllString(pattern, "placeholder")
	if strings.ContainsAny(expanded, "{}") {
		return resourceScopeRule{}, errors.New("invalid resource URI template")
	}
	parsed, err := url.Parse(expanded)
	if err != nil || parsed.Scheme == "" || parsed.User != nil || parsed.Fragment != "" || strings.Contains(pattern, "#") {
		return resourceScopeRule{}, errors.New("resource policy keys must be absolute URIs without credentials or fragments")
	}
	if len(locations) == 0 {
		return resourceScopeRule{pattern: pattern, scopes: scopes}, nil
	}
	var expression strings.Builder
	variables := make([]string, 0, len(locations))
	expression.WriteString("^")
	position := 0
	for _, location := range locations {
		expression.WriteString(regexp.QuoteMeta(pattern[position:location[0]]))
		expression.WriteString("([^/?#]+)")
		variables = append(variables, pattern[location[2]:location[3]])
		position = location[1]
	}
	expression.WriteString(regexp.QuoteMeta(pattern[position:]))
	expression.WriteString("$")
	matcher, err := regexp.Compile(expression.String())
	if err != nil {
		return resourceScopeRule{}, errors.New("invalid resource URI template")
	}
	return resourceScopeRule{pattern: pattern, variables: variables, scopes: scopes, matcher: matcher}, nil
}

func (p scopePolicy) requiredScopes(identifier string) ([]string, bool) {
	if required, exists := p.rules[identifier]; exists {
		return required, true
	}
	if !p.configured {
		return []string{}, true
	}
	matched := false
	var required []string
	for _, rule := range p.templates {
		if rule.matcher != nil && rule.matcher.MatchString(identifier) {
			matched = true
			required = append(required, rule.scopes...)
		}
	}
	if !matched {
		return nil, false
	}
	return uniqueScopes(required), true
}

func (a *authenticator) accessFailure(policy scopePolicy, identifier, kind string, ctx *authContext) *authFailure {
	required, listed := policy.requiredScopes(identifier)
	if !listed {
		return &authFailure{status: http.StatusForbidden, error: kind + "_access_denied"}
	}
	if a.mode == "open" {
		return nil
	}
	for _, scope := range required {
		if ctx == nil {
			return &authFailure{status: http.StatusForbidden, error: "insufficient_scope", scopes: uniqueScopes(append(append([]string{}, a.baseScopes...), required...))}
		}
		if _, ok := ctx.scopes[scope]; !ok {
			return &authFailure{status: http.StatusForbidden, error: "insufficient_scope", scopes: uniqueScopes(append(append([]string{}, a.baseScopes...), required...))}
		}
	}
	return nil
}

func validateHTTPSURL(value string) error {
	parsed, err := url.Parse(value)
	if err != nil || parsed.Scheme != "https" || parsed.Hostname() == "" || parsed.User != nil ||
		parsed.RawQuery != "" || parsed.ForceQuery || parsed.Fragment != "" || strings.Contains(value, "#") {
		return errors.New("OAuth URLs must be HTTPS without credentials, query, or fragment")
	}
	if port := parsed.Port(); port != "" {
		portNumber, err := strconv.Atoi(port)
		if err != nil || portNumber > 65535 {
			return errors.New("OAuth URLs must use a valid port")
		}
	}
	return nil
}

func validateScopeList(scopes []string) error {
	for _, scope := range scopes {
		if scope == "" {
			return errors.New("OAuth scopes must be nonempty ASCII tokens")
		}
		for _, character := range []byte(scope) {
			if character < 0x21 || character > 0x7e || character == '"' || character == '\\' {
				return errors.New("OAuth scopes must be nonempty ASCII tokens")
			}
		}
	}
	return nil
}

func (a *authenticator) authenticate(authorization string) (*authContext, *authFailure) {
	if a.mode == "open" {
		return &authContext{scopes: map[string]struct{}{}, resource: a.resource}, nil
	}
	match := bearerPattern.FindStringSubmatch(authorization)
	if len(match) != 2 || len(match[1]) > maxBearerTokenSize {
		return nil, &authFailure{status: http.StatusUnauthorized, error: "authentication_required", scopes: a.baseScopes}
	}
	claims, err := a.keyResolver.verify(match[1], a.issuer, a.resource)
	if err != nil {
		return nil, &authFailure{status: http.StatusUnauthorized, error: "invalid_token", scopes: a.baseScopes}
	}
	granted := make(map[string]struct{})
	var scopeClaim string
	if value, ok := claims["scope"]; ok {
		_ = json.Unmarshal(value, &scopeClaim)
	}
	for _, scope := range strings.Split(scopeClaim, " ") {
		if scope != "" {
			granted[scope] = struct{}{}
		}
	}
	for _, scope := range a.baseScopes {
		if _, ok := granted[scope]; !ok {
			return nil, &authFailure{status: http.StatusForbidden, error: "insufficient_scope", scopes: a.baseScopes}
		}
	}
	var subject, clientID string
	_ = json.Unmarshal(claims["sub"], &subject)
	_ = json.Unmarshal(claims["client_id"], &clientID)
	expiresAt, _ := numericDate(claims["exp"])
	return &authContext{scopes: granted, subject: subject, clientID: clientID, resource: canonicalResourceIdentity(a.resource), expiresAt: expiresAt}, nil
}

func canonicalResourceIdentity(raw string) string {
	parsed, err := url.Parse(raw)
	if err != nil {
		return raw
	}
	host := strings.ToLower(parsed.Hostname())
	if strings.Contains(host, ":") && !strings.HasPrefix(host, "[") {
		host = "[" + host + "]"
	}
	if port := parsed.Port(); port != "" && port != "443" {
		host += ":" + port
	}
	parsed.Scheme = strings.ToLower(parsed.Scheme)
	parsed.Host = host
	if parsed.Path == "" {
		parsed.Path = "/"
	}
	return parsed.String()
}

func (a *authenticator) visibleTools(ctx *authContext) []map[string]any {
	definitions := mcpRuntimeCatalog.list("tool")
	visible := make([]map[string]any, 0, len(definitions))
	for _, tool := range definitions {
		name, _ := tool["name"].(string)
		if a.toolAllowed(name, ctx) == nil {
			visible = append(visible, tool)
		}
	}
	return visible
}

func (a *authenticator) toolAllowed(name string, ctx *authContext) *authFailure {
	return a.accessFailure(a.toolPolicy, name, "tool", ctx)
}

func (a *authenticator) resourceAllowed(uri string, ctx *authContext) *authFailure {
	return a.accessFailure(a.resourcePolicy, uri, "resource", ctx)
}

func (a *authenticator) promptAllowed(name string, ctx *authContext) *authFailure {
	return a.accessFailure(a.promptPolicy, name, "prompt", ctx)
}

func (a *authenticator) completionReferenceAllowed(referenceType, identifier string, ctx *authContext) *authFailure {
	switch referenceType {
	case "ref/prompt":
		return a.promptAllowed(identifier, ctx)
	case "ref/resource":
		return a.resourceAllowed(identifier, ctx)
	default:
		return nil
	}
}

func (a *authenticator) visibleResources(ctx *authContext) []map[string]any {
	definitions := mcpRuntimeCatalog.list("resource")
	visible := make([]map[string]any, 0, len(definitions))
	for _, resource := range definitions {
		uri, _ := resource["uri"].(string)
		if uri == "" {
			continue
		}
		if a.resourceAllowed(uri, ctx) == nil {
			visible = append(visible, resource)
		}
	}
	return visible
}

func (a *authenticator) visibleResourceTemplates(ctx *authContext) []map[string]any {
	definitions := mcpRuntimeCatalog.list("resource")
	visible := make([]map[string]any, 0, len(definitions))
	for _, resource := range definitions {
		pattern, _ := resource["uriTemplate"].(string)
		if pattern == "" {
			continue
		}
		if a.resourceAllowed(pattern, ctx) == nil {
			visible = append(visible, resource)
		}
	}
	return visible
}

func (a *authenticator) visiblePrompts(ctx *authContext) []map[string]any {
	definitions := mcpRuntimeCatalog.list("prompt")
	visible := make([]map[string]any, 0, len(definitions))
	for _, prompt := range definitions {
		name, _ := prompt["name"].(string)
		if a.promptAllowed(name, ctx) == nil {
			visible = append(visible, prompt)
		}
	}
	return visible
}

func uniqueScopes(scopes []string) []string {
	seen := make(map[string]struct{}, len(scopes))
	unique := make([]string, 0, len(scopes))
	for _, scope := range scopes {
		if _, ok := seen[scope]; ok {
			continue
		}
		seen[scope] = struct{}{}
		unique = append(unique, scope)
	}
	return unique
}

func (a *authenticator) metadata() map[string]any {
	if a.mode != "external-oauth" {
		return nil
	}
	scopes := append([]string{}, a.baseScopes...)
	for _, policy := range []scopePolicy{a.toolPolicy, a.resourcePolicy, a.promptPolicy} {
		for _, entry := range policy.rules {
			scopes = append(scopes, entry...)
		}
	}
	scopes = uniqueScopes(scopes)
	sort.Strings(scopes)
	return map[string]any{
		"resource":                 a.resource,
		"authorization_servers":    []string{a.issuer},
		"scopes_supported":         scopes,
		"bearer_methods_supported": []string{"header"},
	}
}

func (a *authenticator) writeFailure(w http.ResponseWriter, failure *authFailure) {
	if failure == nil {
		return
	}
	if a.mode == "external-oauth" && !strings.HasSuffix(failure.error, "_access_denied") {
		scopes := failure.scopes
		if len(scopes) == 0 {
			scopes = a.baseScopes
		}
		challenge := fmt.Sprintf(`Bearer resource_metadata=%q, scope=%q`, a.metadataURL, strings.Join(scopes, " "))
		if failure.error != "" && failure.error != "authentication_required" {
			challenge += fmt.Sprintf(`, error=%q`, failure.error)
		}
		w.Header().Set("WWW-Authenticate", challenge)
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, failure.status, map[string]string{"error": failure.error})
}

type jwkResolver struct {
	url         string
	client      *http.Client
	mu          sync.Mutex
	keys        []jsonWebKey
	loadedAt    time.Time
	lastRefresh time.Time
}

type jsonWebKeySet struct {
	Keys []jsonWebKey `json:"keys"`
}

type jsonWebKey struct {
	Kty string `json:"kty"`
	Kid string `json:"kid"`
	Use string `json:"use"`
	Alg string `json:"alg"`
	Crv string `json:"crv"`
	N   string `json:"n"`
	E   string `json:"e"`
	X   string `json:"x"`
	Y   string `json:"y"`
}

func newJWKResolver(jwksURL string) *jwkResolver {
	return &jwkResolver{
		url: jwksURL,
		client: &http.Client{
			Timeout: 5 * time.Second,
			CheckRedirect: func(request *http.Request, via []*http.Request) error {
				if request.URL.Scheme != "https" || len(via) >= 3 {
					return errors.New("unsafe JWKS redirect")
				}
				return nil
			},
		},
	}
}

func (r *jwkResolver) verify(token, issuer, audience string) (map[string]json.RawMessage, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 || len(token) > maxBearerTokenSize {
		return nil, errors.New("invalid JWT")
	}
	headerBytes, err := base64.RawURLEncoding.Strict().DecodeString(parts[0])
	if err != nil {
		return nil, errors.New("invalid JWT header")
	}
	var header struct {
		Algorithm string          `json:"alg"`
		KeyID     string          `json:"kid"`
		Critical  []string        `json:"crit"`
		B64       json.RawMessage `json:"b64"`
	}
	if err := json.Unmarshal(headerBytes, &header); err != nil || len(header.Critical) > 0 ||
		(header.Algorithm != "RS256" && header.Algorithm != "ES256") {
		return nil, errors.New("unsupported JWT header")
	}
	if len(header.B64) > 0 {
		var unencodedPayload bool
		if json.Unmarshal(header.B64, &unencodedPayload) != nil || !unencodedPayload {
			return nil, errors.New("unsupported JWT payload encoding")
		}
	}
	key, err := r.resolve(header.KeyID, header.Algorithm)
	if err != nil {
		return nil, err
	}
	signature, err := base64.RawURLEncoding.Strict().DecodeString(parts[2])
	if err != nil {
		return nil, errors.New("invalid JWT signature")
	}
	digest := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	switch publicKey := key.(type) {
	case *rsa.PublicKey:
		if header.Algorithm != "RS256" || rsa.VerifyPKCS1v15(publicKey, crypto.SHA256, digest[:], signature) != nil {
			return nil, errors.New("invalid JWT signature")
		}
	case *ecdsa.PublicKey:
		if header.Algorithm != "ES256" || len(signature) != 64 {
			return nil, errors.New("invalid JWT signature")
		}
		rValue := new(big.Int).SetBytes(signature[:32])
		sValue := new(big.Int).SetBytes(signature[32:])
		if !ecdsa.Verify(publicKey, digest[:], rValue, sValue) {
			return nil, errors.New("invalid JWT signature")
		}
	default:
		return nil, errors.New("unsupported JWT key")
	}
	payloadBytes, err := base64.RawURLEncoding.Strict().DecodeString(parts[1])
	if err != nil {
		return nil, errors.New("invalid JWT claims")
	}
	var claims map[string]json.RawMessage
	if err := json.Unmarshal(payloadBytes, &claims); err != nil || claims == nil {
		return nil, errors.New("invalid JWT claims")
	}
	var tokenIssuer, subject string
	if json.Unmarshal(claims["iss"], &tokenIssuer) != nil || tokenIssuer != issuer ||
		json.Unmarshal(claims["sub"], &subject) != nil || subject == "" {
		return nil, errors.New("invalid JWT identity")
	}
	if !hasAudience(claims["aud"], audience) {
		return nil, errors.New("invalid JWT audience")
	}
	expires, ok := numericDate(claims["exp"])
	if !ok || float64(time.Now().UnixNano())/float64(time.Second) >= expires {
		return nil, errors.New("expired or missing JWT expiration")
	}
	if rawNotBefore, exists := claims["nbf"]; exists {
		notBefore, valid := numericDate(rawNotBefore)
		if !valid || float64(time.Now().UnixNano())/float64(time.Second) < notBefore {
			return nil, errors.New("invalid JWT activation time")
		}
	}
	return claims, nil
}

func hasAudience(raw json.RawMessage, expected string) bool {
	if len(raw) == 0 {
		return false
	}
	var single string
	if json.Unmarshal(raw, &single) == nil {
		return single == expected
	}
	var entries []string
	if json.Unmarshal(raw, &entries) != nil {
		return false
	}
	for _, entry := range entries {
		if entry == expected {
			return true
		}
	}
	return false
}

func numericDate(raw json.RawMessage) (float64, bool) {
	var number json.Number
	if len(raw) == 0 || json.Unmarshal(raw, &number) != nil {
		return 0, false
	}
	value, err := number.Float64()
	return value, err == nil && !math.IsNaN(value) && !math.IsInf(value, 0)
}

func (r *jwkResolver) resolve(kid, algorithm string) (crypto.PublicKey, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	now := time.Now()
	refresh := false
	if r.loadedAt.IsZero() {
		// A failed fetch still gets a cooldown, so unauthenticated traffic
		// cannot turn every bad token into a JWKS request.
		refresh = r.lastRefresh.IsZero() || now.Sub(r.lastRefresh) >= 30*time.Second
	} else if now.Sub(r.loadedAt) >= 5*time.Minute {
		refresh = r.lastRefresh.IsZero() || now.Sub(r.lastRefresh) >= 30*time.Second
	} else {
		if key, ok := findJWK(r.keys, kid, algorithm); ok {
			return key, nil
		}
		// Refresh unknown key IDs for key rotation, with a cooldown to prevent
		// arbitrary token headers from turning verification into a fetch loop.
		refresh = now.Sub(r.lastRefresh) >= 30*time.Second
	}
	if !refresh {
		return nil, errors.New("unknown JWT key")
	}
	keys, err := r.fetch()
	r.lastRefresh = now
	if err != nil {
		return nil, err
	}
	r.keys = keys
	r.loadedAt = now
	if key, ok := findJWK(r.keys, kid, algorithm); ok {
		return key, nil
	}
	return nil, errors.New("unknown JWT key")
}

func (r *jwkResolver) fetch() ([]jsonWebKey, error) {
	request, err := http.NewRequest(http.MethodGet, r.url, nil)
	if err != nil {
		return nil, errors.New("could not request JWKS")
	}
	request.Header.Set("Accept", "application/json")
	response, err := r.client.Do(request)
	if err != nil {
		return nil, errors.New("could not fetch JWKS")
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK || response.Request.URL.Scheme != "https" {
		return nil, errors.New("could not fetch JWKS")
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, maxJWKSBytes+1))
	if err != nil || len(data) > maxJWKSBytes {
		return nil, errors.New("invalid JWKS response")
	}
	var set jsonWebKeySet
	if err := json.Unmarshal(data, &set); err != nil || set.Keys == nil {
		return nil, errors.New("invalid JWKS response")
	}
	return set.Keys, nil
}

func findJWK(keys []jsonWebKey, kid, algorithm string) (crypto.PublicKey, bool) {
	var selected *jsonWebKey
	for index := range keys {
		key := &keys[index]
		if key.Use != "" && key.Use != "sig" || key.Alg != "" && key.Alg != algorithm || kid != "" && key.Kid != kid {
			continue
		}
		if selected != nil {
			return nil, false
		}
		selected = key
	}
	if selected == nil {
		return nil, false
	}
	switch selected.Kty {
	case "RSA":
		if algorithm != "RS256" {
			return nil, false
		}
		modulus, errN := base64.RawURLEncoding.DecodeString(selected.N)
		exponent, errE := base64.RawURLEncoding.DecodeString(selected.E)
		if errN != nil || errE != nil || len(exponent) == 0 || len(exponent) > 4 || len(modulus) > 1024 {
			return nil, false
		}
		exponentValue := new(big.Int).SetBytes(exponent)
		if !exponentValue.IsInt64() || exponentValue.Int64() < 3 || exponentValue.Int64()%2 == 0 {
			return nil, false
		}
		modulusValue := new(big.Int).SetBytes(modulus)
		if modulusValue.BitLen() < 2048 || modulusValue.BitLen() > 8192 {
			return nil, false
		}
		if modulusValue.Bit(0) != 1 {
			return nil, false
		}
		return &rsa.PublicKey{N: modulusValue, E: int(exponentValue.Int64())}, true
	case "EC":
		if algorithm != "ES256" || selected.Crv != "P-256" {
			return nil, false
		}
		x, errX := base64.RawURLEncoding.DecodeString(selected.X)
		y, errY := base64.RawURLEncoding.DecodeString(selected.Y)
		if errX != nil || errY != nil || len(x) != 32 || len(y) != 32 {
			return nil, false
		}
		publicKey := &ecdsa.PublicKey{Curve: elliptic.P256(), X: new(big.Int).SetBytes(x), Y: new(big.Int).SetBytes(y)}
		if !publicKey.Curve.IsOnCurve(publicKey.X, publicKey.Y) {
			return nil, false
		}
		return publicKey, true
	default:
		return nil, false
	}
}
