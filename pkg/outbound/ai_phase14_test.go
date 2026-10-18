package outbound

// adr: 1436 — AI calls name usage tags in X-Gregale-AI-Tags; settlement
// rolls usage up per tag with a bounded number of tags, the request log and
// export line carry them, and exact tags' daily cost limits refuse calls at
// admission.

import (
	"io"
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

func TestParseOutboundAITags(t *testing.T) {
	for _, tc := range []struct {
		header string
		want   string
		ok     bool
	}{
		{"feature=search", "feature=search", true},
		{" tier=free , feature=search ", "feature=search,tier=free", true},
		{"env=prod,region=eu-west-1,customer=acme:42,team=ml/core,v=1.2", "customer=acme:42,env=prod,region=eu-west-1,team=ml/core,v=1.2", true},
		{"a=1,b=2,c=3,d=4,e=5,f=6", "", false},
		{"feature=search,feature=chat", "", false},
		{"Feature=search", "", false},
		{"1st=x", "", false},
		{"feature=", "", false},
		{"feature=with space", "", false},
		{"feature=~other", "", false},
		{"feature", "", false},
		{"", "", false},
		{strings.Repeat("k", 33) + "=v", "", false},
		{"k=" + strings.Repeat("v", 65), "", false},
	} {
		tags, ok := api.ParseOutboundAITags(tc.header)
		if ok != tc.ok || strings.Join(tags, ",") != tc.want {
			t.Errorf("%q = %q, %v; want %q, %v", tc.header, tags, ok, tc.want, tc.ok)
		}
	}
}

// tagFixture serves a fixed chat completion to a tagged integration and
// records the headers the provider saw.
type tagFixture struct {
	handler *Handler
	backend *MemoryBackend
	token   AIAccessToken
	mu      sync.Mutex
	headers []http.Header
}

func newTagFixture(t *testing.T, limits map[string]int64) *tagFixture {
	t.Helper()
	f := &tagFixture{}
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.headers = append(f.headers, r.Header.Clone())
		f.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"model":"gpt-5","choices":[{"message":{"content":"ok"}}],"usage":{"prompt_tokens":1000,"completion_tokens":500}}`)
	}))
	t.Cleanup(upstream.Close)
	account, app := uuid.NewString(), uuid.NewString()
	integration := phase9Integration(t, "llm", upstream.URL, api.OutboundAIProviderOpenAI, account, app)
	integration.AITagDailyCostLimits = limits
	integration.AIRequestLogEnabled, integration.AILogExport = true, true
	f.handler, f.backend = phase9Handler(t, upstream.Client(), integration)
	f.token = AIAccessToken{ID: uuid.NewString(), AccountID: account, AppID: app, ExpiresAt: time.Now().Add(time.Hour)}
	return f
}

func (f *tagFixture) call(tags ...string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(http.MethodPost, "/i/llm/v1/chat/completions", strings.NewReader(`{"model":"gpt-5","max_tokens":100,"messages":[]}`))
	request.Header.Set("Content-Type", "application/json")
	for _, tag := range tags {
		request.Header.Add(api.OutboundAITagsHeader, tag)
	}
	request = request.WithContext(withAccessToken(request.Context(), f.token))
	recorder := httptest.NewRecorder()
	f.handler.ServeHTTP(recorder, request)
	return recorder
}

func TestTaggedCallsRollUpPerTag(t *testing.T) {
	f := newTagFixture(t, nil)
	if got := f.call("tier=free, feature=search"); got.Code != http.StatusOK {
		t.Fatalf("tagged call = %d %s", got.Code, got.Body.String())
	}
	if got := f.call("feature=search"); got.Code != http.StatusOK {
		t.Fatalf("second call = %d", got.Code)
	}
	search, ok := f.backend.AITagUsage("llm", "feature=search")
	free, freeOK := f.backend.AITagUsage("llm", "tier=free")
	if !ok || !freeOK || search.Requests != 2 || free.Requests != 1 || search.InputTokens != 2000 || search.CostMillicents != 2*free.CostMillicents ||
		free.CostMillicents <= 0 {
		t.Fatalf("tag rows = %+v %+v", search, free)
	}
	for _, header := range f.headers {
		if header.Get(api.OutboundAITagsHeader) != "" {
			t.Fatal("the tags header reached the provider")
		}
	}
	logged := f.backend.AIRequestLog("llm")
	if len(logged) != 2 || strings.Join(logged[0].Tags, ",") != "feature=search,tier=free" {
		t.Fatalf("request log = %+v", logged)
	}
	exports := f.backend.AILogExports()
	if len(exports) != 2 || !strings.Contains(exports[0].Line, `"tags":{"feature":"search","tier":"free"}`) ||
		exports[0].Attributes["gregale.ai.tags"] != "feature=search,tier=free" {
		t.Fatalf("export = %+v", exports)
	}
	for _, header := range [][]string{{"feature=search,feature=chat"}, {"feature=search", "tier=free"}, {"BAD"}} {
		got := f.call(header...)
		if got.Code != http.StatusBadRequest || !strings.Contains(got.Body.String(), "outbound_ai_tags_invalid") {
			t.Fatalf("%q = %d %s", header, got.Code, got.Body.String())
		}
	}
	if len(f.headers) != 2 {
		t.Fatalf("refused calls reached the provider: %d calls", len(f.headers))
	}
}

func TestTagDailyCostLimits(t *testing.T) {
	// A call reserves about 213 millicents and settles 2,000: the limit
	// admits the first call and refuses the next.
	f := newTagFixture(t, map[string]int64{"feature=search": 1_000})
	if got := f.call("feature=search"); got.Code != http.StatusOK {
		t.Fatalf("first call = %d %s", got.Code, got.Body.String())
	}
	got := f.call("feature=search,tier=free")
	if got.Code != http.StatusTooManyRequests || got.Header().Get("X-Gregale-Outbound-Rejection") != ReasonTagDailyCostLimit {
		t.Fatalf("over the tag limit = %d %v %s", got.Code, got.Header(), got.Body.String())
	}
	if retry, err := strconv.Atoi(got.Header().Get("Retry-After")); err != nil || retry < 1 || retry > 86_400 {
		t.Fatalf("Retry-After = %q", got.Header().Get("Retry-After"))
	}
	if got := f.call("feature=chat"); got.Code != http.StatusOK {
		t.Fatalf("another tag = %d", got.Code)
	}
	if got := f.call(); got.Code != http.StatusOK {
		t.Fatalf("an untagged call = %d", got.Code)
	}
}

func TestTagRowsOverflowPerKey(t *testing.T) {
	state := &memoryState{tagUsage: map[string]AIUserUsageTotals{}, tagLimits: map[string]int64{"team=core": 1}}
	for index := range api.OutboundAITagsPerDay {
		state.tagUsage["seed="+strconv.Itoa(index)] = AIUserUsageTotals{Requests: 1}
	}
	record := AIUsageRecord{Usage: AIUsage{InputTokens: 3}, CostMillicents: 7}
	state.recordTagUsage("feature=new", record)
	state.recordTagUsage("team=core", record)
	if _, own := state.tagUsage["feature=new"]; own || state.tagUsage["feature=~other"].CostMillicents != 7 {
		t.Fatalf("a tag past the cap did not overflow: %+v", state.tagUsage["feature=~other"])
	}
	if state.tagUsage["team=core"].Requests != 1 {
		t.Fatal("a limited tag overflowed")
	}
}
