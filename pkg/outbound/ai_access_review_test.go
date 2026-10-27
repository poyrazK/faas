package outbound

import (
	"context"
	"crypto/sha256"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestSuspendedAccountIntegrationsRefuseCalls(t *testing.T) {
	for _, tc := range []struct {
		status  string
		refused bool
	}{{"suspended", true}, {"deleted_pending", true}, {"past_due", false}, {"active", false}} {
		integration := phase9Integration(t, "integration-1", "https://api.openai.com", api.OutboundAIProviderOpenAI, uuid.NewString(), uuid.NewString())
		integration.AccountStatus = tc.status
		resolver, err := NewStaticResolver([]Integration{integration})
		if err != nil {
			t.Fatal(err)
		}
		handler, err := NewHandler(resolver, NewMemoryBackend(), http.DefaultClient)
		if err != nil {
			t.Fatal(err)
		}
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/i/integration-1/v1/models", nil))
		if refused := strings.Contains(rec.Body.String(), "outbound_account_suspended"); refused != tc.refused || (refused && rec.Code != http.StatusForbidden) {
			t.Fatalf("%s: %d %s; refused = %t, want %t", tc.status, rec.Code, rec.Body.String(), refused, tc.refused)
		}
	}
}

func TestPublicTokenLookupKeepsKnownTokensUnderUnknownFlood(t *testing.T) {
	secret := api.OutboundAIAccessTokenPrefix + strings.Repeat("a", 43)
	tokens := &countingTokens{StaticAccessTokens: StaticAccessTokens{secret: {ID: "t-1", ExpiresAt: time.Now().Add(time.Hour)}}}
	public := &PublicHandler{Tokens: tokens}
	ctx := context.Background()
	if _, err := public.lookup(ctx, sha256.Sum256([]byte(secret))); err != nil {
		t.Fatal(err)
	}
	for i := range publicTokenCacheMax + 1 {
		if _, err := public.lookup(ctx, sha256.Sum256([]byte("unknown-"+strconv.Itoa(i)))); !errors.Is(err, ErrAIAccessTokenNotFound) {
			t.Fatalf("unknown token %d: %v", i, err)
		}
	}
	before := tokens.lookups.Load()
	if _, err := public.lookup(ctx, sha256.Sum256([]byte(secret))); err != nil || tokens.lookups.Load() != before {
		t.Fatalf("known token after a flood of unknown ones: %v, %d new lookups; want it still cached", err, tokens.lookups.Load()-before)
	}
}

// blockingTokens holds every lookup until release closes.
type blockingTokens struct {
	StaticAccessTokens
	started chan struct{}
	release chan struct{}
}

func (b *blockingTokens) AIAccessToken(ctx context.Context, hash [32]byte) (AIAccessToken, error) {
	b.started <- struct{}{}
	<-b.release
	return b.StaticAccessTokens.AIAccessToken(ctx, hash)
}

func TestPublicTokenLookupsAreBounded(t *testing.T) {
	tokens := &blockingTokens{StaticAccessTokens: StaticAccessTokens{}, started: make(chan struct{}, api.OutboundAIAccessTokenLookupsMax),
		release: make(chan struct{})}
	public := &PublicHandler{Tokens: tokens}
	ctx := context.Background()
	var wg sync.WaitGroup
	for i := range api.OutboundAIAccessTokenLookupsMax {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = public.lookup(ctx, sha256.Sum256([]byte("slow-"+strconv.Itoa(i))))
		}()
	}
	for range api.OutboundAIAccessTokenLookupsMax {
		<-tokens.started
	}
	if _, err := public.lookup(ctx, sha256.Sum256([]byte("one-more"))); !errors.Is(err, errAccessTokenLookupsBusy) {
		t.Fatalf("lookup past the bound = %v; want busy", err)
	}
	close(tokens.release)
	wg.Wait()
}
