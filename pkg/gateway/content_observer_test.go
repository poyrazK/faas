package gateway

// adr: 1188

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestContentObserverOutcomes(t *testing.T) {
	o := newContentObserver(10)
	t0 := time.Unix(1_800_000_000, 0)
	steps := []struct {
		sum  uint64
		at   time.Duration
		want string
	}{
		{1, 0, api.ContentOutcomeFirst},
		{1, 10 * time.Second, "same_60"},
		{2, 2*time.Minute + 10*time.Second, "changed_300"},
		{2, 2*time.Minute + 50*time.Second, "same_60"},
		{2, 50 * time.Minute, "same_3600"},
		{3, 3 * time.Hour, "changed_long"},
	}
	for i, step := range steps {
		if got := o.observe("k", step.sum, t0.Add(step.at)); got != step.want {
			t.Fatalf("step %d: outcome %q, want %q", i, got, step.want)
		}
	}
}

func TestContentObserverEvictsLeastRecentlySeen(t *testing.T) {
	o := newContentObserver(2)
	now := time.Unix(1_800_000_000, 0)
	o.observe("a", 1, now)
	o.observe("b", 1, now)
	o.observe("a", 1, now) // a is now the most recent
	o.observe("c", 1, now) // evicts b
	if got := o.observe("b", 1, now); got != api.ContentOutcomeFirst {
		t.Fatalf("b should have been evicted, got %q", got)
	}
	if got := o.observe("c", 1, now); got != "same_60" {
		t.Fatalf("c should still be remembered, got %q", got)
	}
}

func TestContentObservable(t *testing.T) {
	for name, tc := range map[string]struct {
		method string
		header map[string]string
		want   bool
	}{
		"anonymous get":  {method: "GET", want: true},
		"post":           {method: "POST"},
		"head":           {method: "HEAD"},
		"authorization":  {method: "GET", header: map[string]string{"Authorization": "Bearer x"}},
		"range":          {method: "GET", header: map[string]string{"Range": "bytes=0-10"}},
		"websocket":      {method: "GET", header: map[string]string{"Upgrade": "websocket"}},
		"server events":  {method: "GET", header: map[string]string{"Accept": "text/event-stream"}},
		"session cookie": {method: "GET", header: map[string]string{"Cookie": "session=abc"}},
	} {
		t.Run(name, func(t *testing.T) {
			r := httptest.NewRequest(tc.method, "/catalog?b=2&a=1", nil)
			for k, v := range tc.header {
				r.Header.Set(k, v)
			}
			if got := contentObservable(r); got != tc.want {
				t.Fatalf("contentObservable = %v, want %v", got, tc.want)
			}
		})
	}
}

// serveThroughTee writes one response through a tee and returns its outcome.
func serveThroughTee(o *contentObserver, path string, status int, header map[string]string, body string, now time.Time) string {
	r := httptest.NewRequest("GET", path, nil)
	tee := newContentTee(httptest.NewRecorder(), o, "app-1", r)
	for k, v := range header {
		tee.Header().Set(k, v)
	}
	tee.WriteHeader(status)
	_, _ = tee.Write([]byte(body))
	ctx := withContentTee(r.Context(), tee)
	return contentOutcome(ctx, Target{DeploymentID: "dep-1"}, now)
}

func TestContentTeeClassifiesResponses(t *testing.T) {
	o := newContentObserver(100)
	now := time.Unix(1_800_000_000, 0)
	if got := serveThroughTee(o, "/catalog?b=2&a=1", 200, nil, "v1", now); got != api.ContentOutcomeFirst {
		t.Fatalf("first = %q", got)
	}
	// The same request target repeats; other query orders are separate URLs.
	if got := serveThroughTee(o, "/catalog?b=2&a=1", 200, nil, "v1", now.Add(30*time.Second)); got != "same_60" {
		t.Fatalf("repeat = %q", got)
	}
	if got := serveThroughTee(o, "/catalog?b=2&a=1", 200, nil, "v2", now.Add(4*time.Minute)); got != "changed_300" {
		t.Fatalf("change = %q", got)
	}
	big := strings.Repeat("x", ResponseCachePerEntryMaxBytes+1)
	for name, tc := range map[string]struct {
		status int
		header map[string]string
		body   string
	}{
		"server error":  {status: 500, body: "boom"},
		"set-cookie":    {status: 200, header: map[string]string{"Set-Cookie": "a=b"}, body: "x"},
		"no-store":      {status: 200, header: map[string]string{"Cache-Control": "no-store"}, body: "x"},
		"private":       {status: 200, header: map[string]string{"Cache-Control": "private, max-age=60"}, body: "x"},
		"vary language": {status: 200, header: map[string]string{"Vary": "Accept-Language"}, body: "x"},
		"too large":     {status: 200, body: big},
	} {
		if got := serveThroughTee(o, "/"+strings.ReplaceAll(name, " ", "-"), tc.status, tc.header, tc.body, now); got != api.ContentOutcomeUncacheable {
			t.Fatalf("%s: outcome %q, want uncacheable", name, got)
		}
	}
	if got := serveThroughTee(o, "/identity", 200, map[string]string{"Vary": "Accept-Encoding"}, "x", now); got != api.ContentOutcomeFirst {
		t.Fatalf("an identity body varied only on Accept-Encoding is storable: %q", got)
	}
}

func TestContentOutcomeNeedsAnAppResponse(t *testing.T) {
	o := newContentObserver(10)
	r := httptest.NewRequest("GET", "/x", nil)
	tee := newContentTee(httptest.NewRecorder(), o, "app-1", r)
	_, _ = tee.Write([]byte("gateway problem"))
	ctx := withContentTee(r.Context(), tee)
	if got := contentOutcome(ctx, Target{}, time.Now()); got != "" {
		t.Fatalf("a gateway-written response must not be observed: %q", got)
	}
	if got := contentOutcome(r.Context(), Target{DeploymentID: "d"}, time.Now()); got != "" {
		t.Fatalf("no tee: %q", got)
	}
	// finish is idempotent: a second observe of the same request repeats it.
	if a, b := contentOutcome(ctx, Target{InstanceID: "i"}, time.Now()), contentOutcome(ctx, Target{InstanceID: "i"}, time.Now()); a != b || a != api.ContentOutcomeFirst {
		t.Fatalf("outcomes %q / %q", a, b)
	}
	if got := o.observe(tee.key, 0, time.Now()); got == api.ContentOutcomeFirst {
		t.Fatal("the request should have been recorded once")
	}
}

func TestCollapseKeepsContentOutcomesApart(t *testing.T) {
	base := RequestTelemetryRow{AccountID: uuid.New(), AppID: uuid.New(), Route: "GET /catalog", Method: "GET", Status: http.StatusOK, ReceivedAt: time.Unix(1_800_000_000, 0), Count: 1}
	same, changed := base, base
	same.ContentOutcome, changed.ContentOutcome = "same_60", "changed_60"
	out := collapseRequestTelemetry([]RequestTelemetryRow{same, same, changed})
	if len(out) != 2 || out[0].Count != 2 || out[0].ContentOutcome != "same_60" || out[1].ContentOutcome != "changed_60" {
		t.Fatalf("collapsed = %+v", out)
	}
}

func TestContentOutcomesVocabulary(t *testing.T) {
	outcomes := api.ContentOutcomes()
	if len(outcomes) != 10 || !api.ValidContentOutcome("") || !api.ValidContentOutcome("same_3600") || api.ValidContentOutcome("same_7") {
		t.Fatalf("outcomes = %v", outcomes)
	}
}

// TestServeHTTPRecordsContentOutcomes drives anonymous GETs through the real
// handler: the tee installs on a route without a cache rule and the telemetry
// rows carry the outcomes; an authenticated request is never observed.
func TestServeHTTPRecordsContentOutcomes(t *testing.T) {
	accountID, appID := uuid.NewString(), uuid.NewString()
	version := "v1"
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("catalog " + version))
	}))
	t.Cleanup(upstream.Close)
	backend := &fakeBackend{app: App{ID: appID, AccountID: accountID, Plan: api.PlanPro},
		host: "shop.example", upstream: upstream.Listener.Addr().String()}
	backend.AddTarget(Target{NodeID: upstream.Listener.Addr().String(), InstanceID: uuid.NewString(), DeploymentID: uuid.NewString()})
	h := NewHandlerWith(backend, NewMetrics(), nil)
	h.requestTelemetry = makeTestRecorder()
	get := func(header map[string]string) {
		t.Helper()
		r := httptest.NewRequest(http.MethodGet, "http://shop.example/catalog", nil)
		for k, v := range header {
			r.Header.Set(k, v)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != http.StatusOK {
			t.Fatalf("status %d: %s", w.Code, w.Body.String())
		}
	}
	get(nil)
	get(nil)
	version = "v2"
	get(nil)
	get(map[string]string{"Authorization": "Bearer token"})
	var outcomes []string
	for _, row := range h.requestTelemetry.DrainBatch(16) {
		outcomes = append(outcomes, row.ContentOutcome)
	}
	want := []string{api.ContentOutcomeFirst, "same_60", "changed_60", ""}
	if strings.Join(outcomes, ",") != strings.Join(want, ",") {
		t.Fatalf("outcomes = %q, want %q", outcomes, want)
	}
}
