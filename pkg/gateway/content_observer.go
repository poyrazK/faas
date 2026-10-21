package gateway

// Learned cacheability (ADR-1188). For anonymous GET requests on routes
// without a cache rule, gatewayd-internal hashes the response body with a
// per-process key and compares it with the previous response for the same
// URL. Only the outcome leaves the gateway, on the request telemetry row:
// never the body or its hash. The route advisor turns the outcomes into a
// measured safe max-age for a cache rule.

import (
	"container/list"
	"context"
	"hash/maphash"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	authmw "github.com/onebox-faas/faas/pkg/auth/middleware"
)

// contentObserver remembers the last body hash per URL, bounded by an LRU.
// A hash is only meaningful inside one process: the seed is random.
type contentObserver struct {
	mu      sync.Mutex
	seed    maphash.Seed
	max     int
	entries map[string]*list.Element
	order   *list.List // front is most recently observed
}

type contentEntry struct {
	key  string
	sum  uint64
	seen time.Time
}

func newContentObserver(max int) *contentObserver {
	return &contentObserver{seed: maphash.MakeSeed(), max: max, entries: map[string]*list.Element{}, order: list.New()}
}

// observe records sum as the latest response for key and classifies it
// against the previous one: identical or changed, bucketed by how long ago
// that previous response was seen. A cache rule whose max-age exceeds the gap
// would have served the previous body, correctly when identical and stale
// when changed.
func (o *contentObserver) observe(key string, sum uint64, now time.Time) string {
	o.mu.Lock()
	defer o.mu.Unlock()
	if el, ok := o.entries[key]; ok {
		e := el.Value.(*contentEntry)
		gap := now.Sub(e.seen)
		same := e.sum == sum
		e.sum, e.seen = sum, now
		o.order.MoveToFront(el)
		return api.ContentOutcomeFor(same, gap)
	}
	o.entries[key] = o.order.PushFront(&contentEntry{key: key, sum: sum, seen: now})
	for o.order.Len() > o.max {
		oldest := o.order.Back()
		o.order.Remove(oldest)
		delete(o.entries, oldest.Value.(*contentEntry).key)
	}
	return api.ContentOutcomeFirst
}

// contentObservable reports whether a request's response may be compared:
// an anonymous, whole-body GET the edge cache could serve.
func contentObservable(r *http.Request) bool {
	if r.Method != http.MethodGet || hasCacheCredentials(r) ||
		r.Header.Get("Range") != "" || r.Header.Get("Upgrade") != "" ||
		strings.Contains(strings.ToLower(r.Header.Get("Accept")), "text/event-stream") {
		return false
	}
	if _, ok := authmw.ConsumerFromContext(r); ok {
		return false
	}
	return authenticatedFrom(r.Context()).PlatformTenantID == ""
}

// contentTee hashes the response body on its way to the client. It never
// buffers: past ResponseCachePerEntryMaxBytes the body is too large for the
// edge cache and hashing stops.
type contentTee struct {
	http.ResponseWriter
	observer *contentObserver
	key      string
	hash     maphash.Hash
	size     int64
	over     bool
	failed   bool
	status   int
	headerOK bool
	wrote    bool
	header   http.Header
	done     bool
	outcome  string
}

func newContentTee(w http.ResponseWriter, observer *contentObserver, appID string, r *http.Request) *contentTee {
	t := &contentTee{ResponseWriter: w, observer: observer, status: http.StatusOK,
		key: appID + "\x00" + hostname(r.Host) + "\x00" + r.URL.RequestURI() + "\x00" + strings.ToLower(strings.Join(r.Header.Values("Accept-Encoding"), ", "))}
	t.hash.SetSeed(observer.seed)
	return t
}

// Unwrap preserves http.ResponseController access through the tee.
func (t *contentTee) Unwrap() http.ResponseWriter { return t.ResponseWriter }

// ProblemHTMLRequest preserves browser error negotiation through the tee.
func (t *contentTee) ProblemHTMLRequest() *http.Request {
	if provider, ok := t.ResponseWriter.(interface{ ProblemHTMLRequest() *http.Request }); ok {
		return provider.ProblemHTMLRequest()
	}
	return nil
}

func (t *contentTee) WriteHeader(code int) {
	if !t.headerOK && !isInformationalStatus(code) {
		t.status, t.headerOK = code, true
		t.header = t.ResponseWriter.Header().Clone()
	}
	t.ResponseWriter.WriteHeader(code)
}

func (t *contentTee) Write(b []byte) (int, error) {
	if !t.headerOK {
		t.WriteHeader(http.StatusOK)
	}
	if !t.over && !t.failed {
		if t.size+int64(len(b)) > ResponseCachePerEntryMaxBytes {
			t.over = true
		} else {
			_, _ = t.hash.Write(b)
			t.size += int64(len(b))
		}
	}
	t.wrote = true
	n, err := t.ResponseWriter.Write(b)
	// An incomplete response cannot establish that caching would have served
	// the same body. A later successful write must not clear this failure.
	if err != nil || n != len(b) {
		t.failed = true
	}
	return n, err
}

func (t *contentTee) Flush() {
	if !t.headerOK {
		t.WriteHeader(http.StatusOK)
	}
	if f, ok := t.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// finish classifies the completed response once. Responses the edge cache
// would refuse to store are uncacheable and leave the observer untouched.
func (t *contentTee) finish(now time.Time) string {
	if t.done {
		return t.outcome
	}
	t.done = true
	if !t.storable() {
		t.outcome = api.ContentOutcomeUncacheable
		return t.outcome
	}
	t.outcome = t.observer.observe(t.key, t.hash.Sum64(), now)
	return t.outcome
}

// storable uses the cache's eligibility predicate with no vary_on rule, so
// cache recommendations honor the same status, cookie and header vetoes.
func (t *contentTee) storable() bool {
	if t.header == nil {
		return false
	}
	return (&cacheWriter{
		header: t.header, status: t.status,
		wroteBody: t.wrote, bypass: t.over || t.failed,
	}).shouldStore()
}

type contentTeeKey struct{}

func withContentTee(ctx context.Context, t *contentTee) context.Context {
	return context.WithValue(ctx, contentTeeKey{}, t)
}

// contentOutcome is the telemetry outcome for a request: empty unless the
// response came from the app and its body was observed.
func contentOutcome(ctx context.Context, target Target, now time.Time) string {
	t, _ := ctx.Value(contentTeeKey{}).(*contentTee)
	if t == nil || (target.DeploymentID == "" && target.InstanceID == "") {
		return ""
	}
	return t.finish(now)
}
