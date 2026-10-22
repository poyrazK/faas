package outbound

// adr: 20261019132139 — automatic prompt caching marks a prefix once any
// outboundd has seen it within the cache lifetime; a stale sighting does
// not count, and a failing backend leaves each process to its own memory.

import (
	"context"
	"errors"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/onebox-faas/faas/pkg/api"
)

// failingPrefixes is a backend whose shared prefixes are unavailable.
type failingPrefixes struct{ *MemoryBackend }

func (failingPrefixes) SeenAIPromptPrefix(context.Context, string, [32]byte, time.Duration) (bool, error) {
	return false, errors.New("prefix table unavailable")
}

type sharedPrefixFixture struct {
	replicas []*Handler
	metrics  *Metrics
	token    AIAccessToken
	request  string
}

// newSharedPrefixFixture serves one Anthropic integration from two
// handlers, as two outboundd replicas, over one backend.
func newSharedPrefixFixture(t *testing.T, backend func(*MemoryBackend) Backend) sharedPrefixFixture {
	t.Helper()
	upstream := newTLSUpstream(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"model":"claude-sonnet-5-5","content":[{"type":"text","text":"ok"}],"usage":{"input_tokens":10,"output_tokens":2}}`)
	})
	account, app := uuid.NewString(), uuid.NewString()
	integration := phase9Integration(t, "claude", upstream.URL, api.OutboundAIProviderAnthropic, account, app)
	integration.AIAutoPromptCache = true
	resolver, err := NewStaticResolver([]Integration{integration})
	if err != nil {
		t.Fatal(err)
	}
	memory := NewMemoryBackend()
	shared := backend(memory)
	metrics, err := NewMetrics(prometheus.NewRegistry())
	if err != nil {
		t.Fatal(err)
	}
	fixture := sharedPrefixFixture{metrics: metrics,
		token: AIAccessToken{ID: uuid.NewString(), AccountID: account, AppID: app, ExpiresAt: time.Now().Add(time.Hour)}}
	for range 2 {
		handler, err := NewHandler(resolver, shared, upstream.Client())
		if err != nil {
			t.Fatal(err)
		}
		handler.AIGatewayEnabled, handler.Metrics = true, metrics
		handler.CredentialResolver = &changingCredential{value: "Bearer provider-secret"}
		fixture.replicas = append(fixture.replicas, handler)
	}
	fixture.request = string(anthropicBody(t, map[string]any{"model": "claude-sonnet-5-5", "max_tokens": 100, "system": longText,
		"messages": []any{map[string]any{"role": "user", "content": "balance?"}}}))
	return fixture
}

func (f sharedPrefixFixture) call(t *testing.T, replica int) string {
	t.Helper()
	got := asApp(f.replicas[replica], http.MethodPost, "/i/claude/v1/messages", f.request, f.token)
	if got.Code != http.StatusOK {
		t.Fatalf("call on replica %d = %d %s", replica, got.Code, got.Body.String())
	}
	return got.Header().Get(AIPromptCacheHeader)
}

func (f sharedPrefixFixture) shared(result string) float64 {
	return testutil.ToFloat64(f.metrics.aiPromptPrefixShared.WithLabelValues("customer_managed", result))
}

func TestPromptPrefixSeenOnAnotherReplicaIsMarked(t *testing.T) {
	fixture := newSharedPrefixFixture(t, func(memory *MemoryBackend) Backend { return memory })
	if mark := fixture.call(t, 0); mark != "" {
		t.Fatalf("a first-seen prefix was marked: %q", mark)
	}
	// Replica 1 has never seen the prefix; replica 0 has.
	if mark := fixture.call(t, 1); mark != promptCacheMarkPrefix {
		t.Fatalf("second replica mark = %q", mark)
	}
	if fixture.shared(promptPrefixSharedFirst) != 1 || fixture.shared(promptPrefixSharedRepeated) != 1 {
		t.Fatalf("shared lookups: first %v repeated %v", fixture.shared(promptPrefixSharedFirst), fixture.shared(promptPrefixSharedRepeated))
	}
	// A replica's own repeat needs no lookup.
	if mark := fixture.call(t, 1); mark != promptCacheMarkPrefix || fixture.shared(promptPrefixSharedRepeated) != 1 {
		t.Fatalf("local repeat = %q, lookups %v", mark, fixture.shared(promptPrefixSharedRepeated))
	}
}

func TestStalePromptPrefixSightingIsNotARepeat(t *testing.T) {
	var memory *MemoryBackend
	fixture := newSharedPrefixFixture(t, func(m *MemoryBackend) Backend { memory = m; return m })
	fixture.call(t, 0)
	later := time.Now().Add((api.OutboundAIPromptCacheLifetimeSeconds + 60) * time.Second)
	memory.SetClock(func() time.Time { return later })
	if mark := fixture.call(t, 1); mark != "" {
		t.Fatalf("a sighting older than the cache lifetime marked the prefix: %q", mark)
	}
}

func TestPromptPrefixLookupFailureFallsBackToLocalMemory(t *testing.T) {
	fixture := newSharedPrefixFixture(t, func(memory *MemoryBackend) Backend { return failingPrefixes{memory} })
	fixture.call(t, 0)
	if mark := fixture.call(t, 1); mark != "" {
		t.Fatalf("a failed lookup marked the prefix: %q", mark)
	}
	if mark := fixture.call(t, 1); mark != promptCacheMarkPrefix {
		t.Fatalf("local repeat after a failed lookup = %q", mark)
	}
	if fixture.shared(promptPrefixSharedError) != 2 {
		t.Fatalf("lookup errors = %v", fixture.shared(promptPrefixSharedError))
	}
}

// failingPrefixes keeps the MemoryBackend's other contracts.
var _ AIUsageBackend = failingPrefixes{}

func TestPromptPrefixRefreshIsDueOncePerInterval(t *testing.T) {
	var prefixes promptPrefixes
	key := promptPrefixKey("i", "claude", []byte("system"))
	start := time.Now()
	if repeated, refresh := prefixes.repeated(key, start); repeated || refresh {
		t.Fatalf("first sight = %v %v", repeated, refresh)
	}
	if repeated, refresh := prefixes.repeated(key, start.Add(10*time.Second)); !repeated || refresh {
		t.Fatalf("early repeat = %v %v", repeated, refresh)
	}
	due := start.Add(api.OutboundAIPromptPrefixRefreshSeconds*time.Second + time.Second)
	if repeated, refresh := prefixes.repeated(key, due); !repeated || !refresh {
		t.Fatalf("due repeat = %v %v", repeated, refresh)
	}
	if _, refresh := prefixes.repeated(key, due.Add(time.Second)); refresh {
		t.Fatal("a refresh was due twice within the interval")
	}
}
