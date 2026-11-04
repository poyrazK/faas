package outbound

import (
	"context"
	"crypto/sha256"
	"errors"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// ErrAIAccessTokenNotFound reports an unknown, revoked or expired token.
var ErrAIAccessTokenNotFound = errors.New("outbound: AI access token not found")

// AIAccessToken is a live access token (ADR-1431). An empty integration set
// allows every AI integration bound to the app.
type AIAccessToken struct {
	ID             string
	AccountID      string
	AppID          string
	IntegrationIDs map[string]struct{}
	ExpiresAt      time.Time
}

// AIAccessTokenStore reads the token hashes apid writes.
type AIAccessTokenStore interface {
	AIAccessToken(ctx context.Context, hash [32]byte) (AIAccessToken, error)
	// TouchAIAccessToken records that a token was just used.
	TouchAIAccessToken(ctx context.Context, id string) error
}

type accessTokenPrincipalKey struct{}

func withAccessToken(ctx context.Context, token AIAccessToken) context.Context {
	return context.WithValue(ctx, accessTokenPrincipalKey{}, token)
}

func accessTokenFromContext(ctx context.Context) (AIAccessToken, bool) {
	token, ok := ctx.Value(accessTokenPrincipalKey{}).(AIAccessToken)
	return token, ok
}

// allows reports whether a token may call an integration: one of the
// account's customer AI integrations, listed by the token when it lists any.
func (t AIAccessToken) allows(integration Integration) bool {
	if integration.AccountID != t.AccountID || integration.OwnerKind != IntegrationOwnerCustomer ||
		integration.ProviderAuthMode != ProviderAuthManaged || integration.AIProvider == "" {
		return false
	}
	if len(t.IntegrationIDs) == 0 {
		return true
	}
	_, listed := t.IntegrationIDs[integration.ID]
	return listed
}

// accessTokenHeaders are where SDKs put their API key: OpenAI, Anthropic,
// Gemini and Azure OpenAI. A token is read from the first one present.
var accessTokenHeaders = []string{"Authorization", "X-Api-Key", "X-Goog-Api-Key", "Api-Key"}

// publicStrippedHeaders are headers no caller outside Gregale may send: the
// platform identities and the operator token path.
var publicStrippedHeaders = []string{WorkloadIdentityHeader, ExecutionIdentityHeader, WorkflowIdentityHeader, TokenHeader, AppHeader}

// accessTokenFromRequest reads a token from the SDK key headers.
func accessTokenFromRequest(r *http.Request) string {
	for _, name := range accessTokenHeaders {
		value := strings.TrimSpace(r.Header.Get(name))
		if name == "Authorization" {
			scheme, rest, ok := strings.Cut(value, " ")
			if !ok || !strings.EqualFold(scheme, "Bearer") {
				continue
			}
			value = strings.TrimSpace(rest)
		}
		if value != "" {
			return value
		}
	}
	return ""
}

// validAccessTokenShape checks a token's prefix and length before any
// lookup.
func validAccessTokenShape(token string) bool {
	if !strings.HasPrefix(token, api.OutboundAIAccessTokenPrefix) || len(token) != len(api.OutboundAIAccessTokenPrefix)+43 {
		return false
	}
	return strings.IndexFunc(token[len(api.OutboundAIAccessTokenPrefix):], func(r rune) bool {
		return (r < 'a' || r > 'z') && (r < 'A' || r > 'Z') && (r < '0' || r > '9') && r != '-' && r != '_'
	}) < 0
}

// PublicHandler serves callers outside Gregale that present an AI gateway
// access token (ADR-1431): only AI integration and route paths, as the
// token's app, with every key header removed before the provider credential
// is added. outboundd runs it on its optional public listener.
type PublicHandler struct {
	Handler *Handler
	Tokens  AIAccessTokenStore
	// Now is the clock; nil means time.Now.
	Now func() time.Time

	mu sync.Mutex
	// known and unknown cache lookups apart, so a flood of unknown tokens
	// never pushes out known ones; lookups bounds the database lookups of
	// uncached tokens (ADR 20261027132936).
	known   map[[32]byte]cachedAccessToken
	unknown map[[32]byte]time.Time
	lookups chan struct{}
	touched map[string]time.Time
}

// errAccessTokenLookupsBusy reports that too many uncached tokens are being
// looked up already; the caller retries.
var errAccessTokenLookupsBusy = errors.New("outbound: AI access token lookups are busy")

type cachedAccessToken struct {
	token   AIAccessToken
	expires time.Time
}

// publicTokenCacheMax bounds each lookup cache; a full one is cleared.
const publicTokenCacheMax = 10_000

func (p *PublicHandler) now() time.Time {
	if p.Now != nil {
		return p.Now()
	}
	return time.Now()
}

func (p *PublicHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	path := r.URL.EscapedPath()
	if !strings.HasPrefix(path, Prefix) && !strings.HasPrefix(path, RoutePrefix) {
		writeProblem(w, http.StatusNotFound, "outbound_integration_not_found", "Outbound integration not found", "")
		return
	}
	raw := accessTokenFromRequest(r)
	if !validAccessTokenShape(raw) {
		writeProblem(w, http.StatusUnauthorized, "outbound_access_token_invalid", "An AI gateway access token is required", "")
		return
	}
	token, err := p.lookup(r.Context(), sha256.Sum256([]byte(raw)))
	p.Handler.Metrics.ObserveAIAccessTokenLookup(accessTokenLookupResult(err))
	switch {
	case errors.Is(err, ErrAIAccessTokenNotFound):
		writeProblem(w, http.StatusUnauthorized, "outbound_access_token_invalid", "The AI gateway access token is unknown, revoked or expired", "")
		return
	case err != nil:
		writeProblem(w, http.StatusServiceUnavailable, "outbound_access_token_unavailable", "AI gateway access tokens are unavailable", "1")
		return
	}
	p.touch(r.Context(), token.ID)
	request := r.Clone(withAccessToken(r.Context(), token))
	for _, name := range append(append([]string{}, accessTokenHeaders...), publicStrippedHeaders...) {
		request.Header.Del(name)
	}
	request.URL.RawQuery = withoutQueryKey(request.URL.RawQuery, "key")
	p.Handler.ServeHTTP(w, request)
}

// lookup reads a token through a short cache, negative results included,
// so a revoked token works for at most OutboundAIAccessTokenCacheSeconds.
// At most OutboundAIAccessTokenLookupsMax uncached tokens are looked up at
// once.
func (p *PublicHandler) lookup(ctx context.Context, hash [32]byte) (AIAccessToken, error) {
	now := p.now()
	p.mu.Lock()
	if cached, ok := p.known[hash]; ok && now.Before(cached.expires) {
		p.mu.Unlock()
		if !now.Before(cached.token.ExpiresAt) {
			return AIAccessToken{}, ErrAIAccessTokenNotFound
		}
		return cached.token, nil
	}
	if expires, ok := p.unknown[hash]; ok && now.Before(expires) {
		p.mu.Unlock()
		return AIAccessToken{}, ErrAIAccessTokenNotFound
	}
	if p.lookups == nil {
		p.lookups = make(chan struct{}, api.OutboundAIAccessTokenLookupsMax)
	}
	lookups := p.lookups
	p.mu.Unlock()
	select {
	case lookups <- struct{}{}:
		defer func() { <-lookups }()
	default:
		return AIAccessToken{}, errAccessTokenLookupsBusy
	}
	token, err := p.Tokens.AIAccessToken(ctx, hash)
	if err != nil && !errors.Is(err, ErrAIAccessTokenNotFound) {
		return AIAccessToken{}, err
	}
	expires := now.Add(api.OutboundAIAccessTokenCacheSeconds * time.Second)
	p.mu.Lock()
	if err != nil {
		if p.unknown == nil || len(p.unknown) >= publicTokenCacheMax {
			p.unknown = make(map[[32]byte]time.Time)
		}
		p.unknown[hash] = expires
	} else {
		if p.known == nil || len(p.known) >= publicTokenCacheMax {
			p.known = make(map[[32]byte]cachedAccessToken)
		}
		p.known[hash] = cachedAccessToken{token: token, expires: expires}
	}
	p.mu.Unlock()
	if err != nil || !now.Before(token.ExpiresAt) {
		return AIAccessToken{}, ErrAIAccessTokenNotFound
	}
	return token, nil
}

// accessTokenLookupResult labels a lookup for the metric.
func accessTokenLookupResult(err error) string {
	switch {
	case err == nil:
		return "found"
	case errors.Is(err, ErrAIAccessTokenNotFound):
		return "unknown"
	case errors.Is(err, errAccessTokenLookupsBusy):
		return "busy"
	default:
		return "error"
	}
}

// touch records a token's use at most once a minute, off the request path.
func (p *PublicHandler) touch(ctx context.Context, id string) {
	now := p.now()
	p.mu.Lock()
	if p.touched == nil || len(p.touched) >= publicTokenCacheMax {
		p.touched = make(map[string]time.Time)
	}
	if last, ok := p.touched[id]; ok && now.Sub(last) < time.Minute {
		p.mu.Unlock()
		return
	}
	p.touched[id] = now
	p.mu.Unlock()
	go func() {
		touchCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		_ = p.Tokens.TouchAIAccessToken(touchCtx, id)
	}()
}

// withoutQueryKey removes one parameter, such as a Gemini ?key=, from a raw
// query and keeps the others as they were sent.
func withoutQueryKey(rawQuery, key string) string {
	if rawQuery == "" {
		return rawQuery
	}
	parts := strings.Split(rawQuery, "&")
	kept := parts[:0]
	for _, part := range parts {
		name, _, _ := strings.Cut(part, "=")
		if name != key {
			kept = append(kept, part)
		}
	}
	return strings.Join(kept, "&")
}
