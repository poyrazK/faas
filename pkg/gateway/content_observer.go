package gateway

// Learned cacheability (ADR-1188). For anonymous GET requests on routes
// without a cache rule, gatewayd-internal hashes the response body with a
// per-process key and compares it with the previous response for the same
// URL. Only the outcome leaves the gateway, on the request telemetry row:
// never the body or its hash. The route advisor turns the outcomes into a
// measured safe max-age for a cache rule.
//
// On a route with a cache rule (ADR-1199) the same comparison runs on each
// body the cache stores: a refill. A body that changed within the rule's
// max-age is stale; an unchanged one reports how long the body has stayed the
// same, the evidence that a longer max-age would be safe.

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
	key   string
	sum   uint64
	seen  time.Time
	since time.Time // when this body was first seen
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
	return o.record(key, sum, now, func(e *contentEntry, same bool, gap time.Duration) string {
		return api.ContentOutcomeFor(same, gap)
	})
}

// observeRefill records a body stored under a cache rule with maxAge. A
// changed body stored within the rule's max-age (plus a small lag) is stale:
// the previous copy was very probably served after the content changed. An
// unchanged body is bucketed by how long it has stayed the same, since every
// refill in between matched it.
func (o *contentObserver) observeRefill(key string, sum uint64, now time.Time, maxAge time.Duration) string {
	return o.record(key, sum, now, func(e *contentEntry, same bool, gap time.Duration) string {
		switch {
		case same:
			return api.ContentOutcomeFor(true, now.Sub(e.since))
		case api.ContentRefillStale(gap, maxAge):
			return api.ContentOutcomeStale
		default:
			return api.ContentOutcomeFor(false, gap)
		}
	})
}

// record stores sum as the latest body for key and classifies it with
// classify against the previous entry, which it sees before the update.
func (o *contentObserver) record(key string, sum uint64, now time.Time, classify func(e *contentEntry, same bool, gap time.Duration) string) string {
	o.mu.Lock()
	defer o.mu.Unlock()
	if el, ok := o.entries[key]; ok {
		e := el.Value.(*contentEntry)
		same := e.sum == sum
		outcome := classify(e, same, now.Sub(e.seen))
		if !same {
			e.sum, e.since = sum, now
		}
		e.seen = now
		o.order.MoveToFront(el)
		return outcome
	}
	o.entries[key] = o.order.PushFront(&contentEntry{key: key, sum: sum, seen: now, since: now})
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
	t := &contentTee{ResponseWriter: w, observer: observer, status: http.StatusOK, key: contentKey(appID, r, "")}
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

// contentKey identifies a URL's response: app, host, request URI and every
// Accept-Encoding value, plus the cache rule's vary hash when it has one.
func contentKey(appID string, r *http.Request, vary string) string {
	return appID + "\x00" + hostname(r.Host) + "\x00" + r.URL.RequestURI() + "\x00" +
		strings.ToLower(strings.Join(r.Header.Values("Accept-Encoding"), ", ")) + vary
}

// refillObserver classifies the body a cache rule's miss stores (ADR-1199).
// The cache writer has buffered the whole body by the time telemetry asks.
type refillObserver struct {
	cw       *cacheWriter
	observer *contentObserver
	key      string
	maxAge   time.Duration
	done     bool
	outcome  string
}

func newRefillObserver(cw *cacheWriter, observer *contentObserver, appID string, r *http.Request, rule *EdgeRuleCacheResolved) *refillObserver {
	vary := ""
	if len(rule.VaryOn) > 0 {
		sum := computeVaryHash(r, rule.VaryOn)
		vary = "\x00" + string(sum[:])
	}
	return &refillObserver{cw: cw, observer: observer, key: contentKey(appID, r, vary), maxAge: time.Duration(rule.MaxAgeSeconds) * time.Second}
}

func (o *refillObserver) finish(now time.Time) string {
	if o.done {
		return o.outcome
	}
	o.done = true
	if !o.cw.shouldStore() {
		o.outcome = api.ContentOutcomeUncacheable
		return o.outcome
	}
	var hash maphash.Hash
	hash.SetSeed(o.observer.seed)
	_, _ = hash.Write(o.cw.buf.Bytes())
	o.outcome = o.observer.observeRefill(o.key, hash.Sum64(), now, o.maxAge)
	return o.outcome
}

// contentFinisher classifies a completed response once.
type contentFinisher interface{ finish(now time.Time) string }

type contentTeeKey struct{}

func withContentTee(ctx context.Context, t contentFinisher) context.Context {
	return context.WithValue(ctx, contentTeeKey{}, t)
}

// contentOutcome is the telemetry outcome for a request: empty unless the
// response came from the app and its body was observed.
func contentOutcome(ctx context.Context, target Target, now time.Time) string {
	t, _ := ctx.Value(contentTeeKey{}).(contentFinisher)
	if t == nil || (target.DeploymentID == "" && target.InstanceID == "") {
		return ""
	}
	return t.finish(now)
}
