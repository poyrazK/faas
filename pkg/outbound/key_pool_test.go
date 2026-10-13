package outbound

// adr: 991 — extra provider keys rotate per call and on 429s.

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

type fakeKeyPool struct{ keys []ManagedKey }

func (f fakeKeyPool) Authorization(context.Context, string) (string, error) {
	return f.keys[0].Value, nil
}

func (f fakeKeyPool) Authorizations(context.Context, string) ([]ManagedKey, error) {
	return append([]ManagedKey(nil), f.keys...), nil
}

type keyPoolFixture struct {
	handler *Handler
	backend *MemoryBackend
	mu      sync.Mutex
	used    []string
	limited map[string]bool
}

func newKeyPoolFixture(t *testing.T, limited ...string) *keyPoolFixture {
	t.Helper()
	f := &keyPoolFixture{limited: map[string]bool{}}
	for _, key := range limited {
		f.limited[key] = true
	}
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.ReadAll(r.Body)
		key := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		f.mu.Lock()
		f.used = append(f.used, key)
		f.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		if f.limited[key] {
			w.Header().Set("Retry-After", "60")
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		_, _ = io.WriteString(w, `{"model":"m","usage":{"prompt_tokens":1,"completion_tokens":1}}`)
	}))
	t.Cleanup(upstream.Close)
	integration, err := NewIntegration("integration-1", upstream.URL, "unused", []string{"app-1"}, 100, 10, 10, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	integration.ProviderAuthMode, integration.CredentialSource = ProviderAuthManaged, CredentialSourceCustomerSealed
	integration.AllowedMethods, integration.AllowedPathPrefixes = []string{http.MethodPost}, []string{"/v1"}
	integration.AIProvider = api.OutboundAIProviderOpenAI
	resolver, err := NewStaticResolver([]Integration{integration})
	if err != nil {
		t.Fatal(err)
	}
	f.backend = NewMemoryBackend()
	f.handler, err = NewHandler(resolver, f.backend, upstream.Client())
	if err != nil {
		t.Fatal(err)
	}
	f.handler.CredentialResolver = fakeKeyPool{keys: []ManagedKey{{ID: PrimaryManagedKeyID, Value: "Bearer a"}, {ID: "k2", Value: "Bearer b"}}}
	return f
}

func (f *keyPoolFixture) call() int {
	req := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:2774/i/integration-1/v1/chat/completions", strings.NewReader(`{"max_tokens":5}`))
	req.Header.Set("Content-Type", "application/json")
	req = req.WithContext(withRelayIdentity(context.Background(), WorkloadIdentity{AccountID: "account-1", AppID: "app-1", InstanceID: "i-1"}))
	recorder := httptest.NewRecorder()
	f.handler.ServeHTTP(recorder, req)
	return recorder.Code
}

func (f *keyPoolFixture) keysUsed() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return strings.Join(f.used, ",")
}

func TestKeyPoolRotatesAwayFromARateLimitedKey(t *testing.T) {
	f := newKeyPoolFixture(t, "a")
	for i := 0; i < 3; i++ {
		if code := f.call(); code != http.StatusOK {
			t.Fatalf("call %d = %d (keys %s)", i, code, f.keysUsed())
		}
	}
	// Call 1 rotates a→b, call 2 starts at b, call 3 skips the cooling a.
	if got := f.keysUsed(); got != "a,b,b,b" {
		t.Fatalf("keys used = %s", got)
	}
	if gate, err := f.backend.AllowProviderRequest(context.Background(), "integration-1", 0); err != nil || !gate.Allowed {
		t.Fatalf("a single key's 429 cooled the whole integration: %+v %v", gate, err)
	}
}

func TestKeyPoolExhaustedFallsBackToTheIntegrationCooldown(t *testing.T) {
	f := newKeyPoolFixture(t, "a", "b")
	if code := f.call(); code != http.StatusTooManyRequests {
		t.Fatalf("all keys limited = %d", code)
	}
	if got := f.keysUsed(); got != "a,b" {
		t.Fatalf("keys used = %s", got)
	}
	if gate, _ := f.backend.AllowProviderRequest(context.Background(), "integration-1", 0); gate.Allowed {
		t.Fatal("the integration cooldown was not recorded once every key was limited")
	}
}

func TestKeyPoolOrderKeepsCoolingKeysLast(t *testing.T) {
	var pools keyPoolState
	keys := []ManagedKey{{ID: "a"}, {ID: "b"}, {ID: "c"}}
	now := time.Unix(1_000, 0)
	pools.cool("i", "a", now.Add(time.Minute))
	pools.cool("i", "b", now.Add(time.Second))
	if got := pools.order("i", keys, now, ""); got[0].ID != "c" || got[1].ID != "b" || got[2].ID != "a" {
		t.Fatalf("order = %v", got)
	}
	if got := pools.order("i", keys, now.Add(2*time.Minute), ""); len(got) != 3 || got[0].ID != "b" {
		t.Fatalf("after cooldowns, round robin = %v", got)
	}
}

// adr: 994 — an affinity key keeps a session on one key, skips a cooling
// key, and removing a key moves only the sessions that were on it.
func TestKeyPoolAffinityIsStable(t *testing.T) {
	var pools keyPoolState
	keys := []ManagedKey{{ID: "a"}, {ID: "b"}, {ID: "c"}, {ID: "d"}}
	now := time.Unix(1_000, 0)
	first := pools.order("i", keys, now, "session-1")[0].ID
	for i := 0; i < 5; i++ {
		if got := pools.order("i", keys, now, "session-1")[0].ID; got != first {
			t.Fatalf("affinity moved from %s to %s", first, got)
		}
	}
	spread := map[string]bool{}
	for i := 0; i < 64; i++ {
		spread[pools.order("i", keys, now, "user-"+strconv.Itoa(i))[0].ID] = true
	}
	if len(spread) < 3 {
		t.Fatalf("64 sessions used only keys %v", spread)
	}
	pools.cool("i", first, now.Add(time.Minute))
	if cooled := pools.order("i", keys, now, "session-1"); cooled[0].ID == first || cooled[len(cooled)-1].ID != first {
		t.Fatalf("a cooling key stayed first: %v", cooled)
	}
	var others []ManagedKey
	for _, key := range keys {
		if key.ID != first {
			others = append(others, key)
		}
	}
	for i := 0; i < 64; i++ {
		affinity := "user-" + strconv.Itoa(i)
		before := pools.order("j", keys, now, affinity)[0].ID
		if after := pools.order("j", others, now, affinity)[0].ID; before != first && after != before {
			t.Fatalf("removing %s moved %s from %s to %s", first, affinity, before, after)
		}
	}
}
