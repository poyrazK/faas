package outbound

import (
	"context"
	"crypto/sha256"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// publicTokenRequest is a request through Caddy on the loopback hop, which
// pins the caller's address in X-Forwarded-For.
func publicTokenRequest(client, token string) *http.Request {
	r := httptest.NewRequest(http.MethodPost, Prefix+"openai/v1/chat/completions", strings.NewReader("{}"))
	r.RemoteAddr = "127.0.0.1:40000"
	r.Header.Set("X-Forwarded-For", client)
	r.Header.Set("Authorization", "Bearer "+token)
	return r
}

func randomShapedToken(i int) string {
	s := strconv.Itoa(i)
	return api.OutboundAIAccessTokenPrefix + strings.Repeat("z", 43-len(s)) + s
}

// A network that keeps presenting unknown tokens gets 429 without a database
// lookup once it has used its budget; other networks are unaffected (bug
// hunt H82).
func TestPublicHandlerLimitsUnknownTokensPerNetwork(t *testing.T) {
	tokens := &countingTokens{StaticAccessTokens: StaticAccessTokens{}}
	now := time.Unix(1_800_000_000, 0)
	public := &PublicHandler{Handler: &Handler{}, Tokens: tokens, Now: func() time.Time { return now }}
	serve := func(client string, i int) int {
		rec := httptest.NewRecorder()
		public.ServeHTTP(rec, publicTokenRequest(client, randomShapedToken(i)))
		return rec.Code
	}
	for i := range api.OutboundAIAccessTokenGuessesPerWindow {
		// Two addresses of one /24 share the budget.
		client := "203.0.113." + strconv.Itoa(1+i%2)
		if code := serve(client, i); code != http.StatusUnauthorized {
			t.Fatalf("guess %d = %d, want 401", i, code)
		}
	}
	before := tokens.lookups.Load()
	if code := serve("203.0.113.77", 9_999); code != http.StatusTooManyRequests {
		t.Fatalf("guess past the budget = %d, want 429", code)
	}
	if tokens.lookups.Load() != before {
		t.Fatal("a limited network's token was looked up")
	}
	if code := serve("198.51.100.9", 10_000); code != http.StatusUnauthorized {
		t.Fatalf("another network = %d, want 401", code)
	}
	now = now.Add(api.OutboundAIAccessTokenGuessWindow)
	if code := serve("203.0.113.1", 10_001); code != http.StatusUnauthorized {
		t.Fatalf("after the window = %d, want 401", code)
	}
}

// A full cache loses a tenth of its entries, not all of them, so a burst of
// new tokens cannot turn every cached caller's next request into a lookup.
func TestPublicTokenCacheIsTrimmedNotCleared(t *testing.T) {
	public := &PublicHandler{Tokens: everyTokenKnown{}}
	ctx := context.Background()
	for i := range publicTokenCacheMax + 1 {
		if _, err := public.lookup(ctx, sha256.Sum256([]byte(randomShapedToken(i)))); err != nil {
			t.Fatal(err)
		}
	}
	if got := len(public.known); got < publicTokenCacheMax*8/10 {
		t.Fatalf("known cache after overflow = %d entries, want about %d", got, publicTokenCacheMax*9/10)
	}
}

func TestAccessTokenGuessLimiterEvictsTheFewestFailures(t *testing.T) {
	var l accessTokenGuessLimiter
	now := time.Unix(1_800_000_000, 0)
	for range api.OutboundAIAccessTokenGuessesPerWindow {
		l.fail("busy", now)
	}
	for i := range api.OutboundAIAccessTokenGuessClientsMax + 10 {
		l.fail("new-"+strconv.Itoa(i), now)
	}
	if !l.blocked("busy", now) {
		t.Fatal("a flood of new networks reset the busy guesser")
	}
	if len(l.clients) > api.OutboundAIAccessTokenGuessClientsMax {
		t.Fatalf("tracked networks = %d, want at most %d", len(l.clients), api.OutboundAIAccessTokenGuessClientsMax)
	}
}

// everyTokenKnown accepts any token.
type everyTokenKnown struct{ StaticAccessTokens }

func (everyTokenKnown) AIAccessToken(context.Context, [32]byte) (AIAccessToken, error) {
	return AIAccessToken{ID: "t", ExpiresAt: time.Now().Add(time.Hour)}, nil
}
