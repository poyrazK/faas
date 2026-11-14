package synthetics

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestURLUsesTheAppsOwnHost(t *testing.T) {
	c := state.RunnableSyntheticCheck{SyntheticCheck: state.SyntheticCheck{Path: "/healthz?deep=1"}, AppSlug: "shop"}
	if got := URL(c, "apps.example.com"); got != "https://shop.apps.example.com/healthz?deep=1" {
		t.Fatalf("URL = %q", got)
	}
	if got := URL(c, "unset"); got != "https://shop.gregale.dev/healthz?deep=1" {
		t.Fatalf("default URL = %q", got)
	}
}

func TestDue(t *testing.T) {
	now := time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)
	c := state.RunnableSyntheticCheck{SyntheticCheck: state.SyntheticCheck{IntervalSeconds: 300}}
	if !Due(c, now) {
		t.Error("a never-run check is due")
	}
	c.LastRunAt = now.Add(-299 * time.Second)
	if Due(c, now) {
		t.Error("a check run 299 s ago on a 300 s interval is not due")
	}
	c.LastRunAt = now.Add(-300 * time.Second)
	if !Due(c, now) {
		t.Error("a check whose interval has elapsed is due")
	}
}

func TestStatusOK(t *testing.T) {
	for _, tt := range []struct {
		got, expected int
		ok            bool
	}{{200, 0, true}, {204, 0, true}, {301, 0, false}, {500, 0, false}, {301, 301, true}, {200, 204, false}} {
		if StatusOK(tt.got, tt.expected) != tt.ok {
			t.Errorf("StatusOK(%d, %d) != %v", tt.got, tt.expected, tt.ok)
		}
	}
}

func TestClassify(t *testing.T) {
	tests := []struct {
		err  error
		want string
	}{
		{context.DeadlineExceeded, ErrorTimeout},
		{&url.Error{Op: "Get", Err: &net.DNSError{Err: "no such host", Name: "x"}}, ErrorDNS},
		{&url.Error{Op: "Get", Err: &net.OpError{Op: "dial", Err: errors.New("refused")}}, ErrorConnect},
		{errors.New("boom"), ErrorOther},
	}
	for _, tt := range tests {
		if got := Classify(tt.err); got != tt.want {
			t.Errorf("Classify(%v) = %q, want %q", tt.err, got, tt.want)
		}
	}
}

// probeRunner serves the test cases from a TLS server; the client is
// rewritten to dial it whatever host the check URL names.
func probeRunner(t *testing.T, handler http.HandlerFunc) (*Runner, *state.MemStore, state.App) {
	t.Helper()
	srv := httptest.NewTLSServer(handler)
	t.Cleanup(srv.Close)
	client := srv.Client()
	tr := client.Transport.(*http.Transport).Clone()
	tr.DialContext = func(ctx context.Context, network, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, network, srv.Listener.Addr().String())
	}
	tr.TLSClientConfig.InsecureSkipVerify = true //nolint:gosec // test server certificate
	client.Transport = tr
	client.CheckRedirect = NewClient().CheckRedirect
	store := state.NewMemStore()
	acct, err := store.CreateAccount(context.Background(), "syn@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(context.Background(), state.App{AccountID: acct.ID, Slug: "shop", Type: state.AppTypeApp, RAMMB: 512, MaxConcurrency: 1, IdleTimeoutS: 300})
	if err != nil {
		t.Fatal(err)
	}
	return &Runner{Store: store, Client: client, AppsDomain: "apps.example.com", Concurrency: 4, PerAccount: 4}, store, app
}

func TestRunOnceRecordsOutcomes(t *testing.T) {
	r, store, app := probeRunner(t, func(w http.ResponseWriter, req *http.Request) {
		switch req.URL.Path {
		case "/ok":
			w.WriteHeader(http.StatusOK)
		case "/moved":
			http.Redirect(w, req, "https://elsewhere.example/", http.StatusFound)
		case "/slow":
			time.Sleep(1500 * time.Millisecond)
		default:
			w.WriteHeader(http.StatusInternalServerError)
		}
	})
	ctx := context.Background()
	mk := func(name, path string, expected, timeout int) state.SyntheticCheck {
		c, err := store.CreateSyntheticCheck(ctx, state.SyntheticCheck{AccountID: app.AccountID, AppID: app.ID, Name: name, Method: "GET", Path: path, ExpectedStatus: expected, TimeoutMS: timeout, IntervalSeconds: 300}, api.MaxSyntheticChecksPerApp)
		if err != nil {
			t.Fatal(err)
		}
		return c
	}
	want := map[string]state.SyntheticCheckRun{
		mk("ok", "/ok", 0, 5000).ID:          {OK: true, StatusCode: 200},
		mk("broken", "/err", 0, 5000).ID:     {StatusCode: 500, ErrorClass: ErrorStatus},
		mk("redirect", "/moved", 0, 5000).ID: {StatusCode: 302, ErrorClass: ErrorStatus},
		mk("slow", "/slow", 0, 1000).ID:      {ErrorClass: ErrorTimeout},
	}
	stats, err := r.RunOnce(ctx)
	if err != nil || stats.Due != 4 || stats.Started != 4 {
		t.Fatalf("stats = %+v, err %v", stats, err)
	}
	r.Wait()
	for id, w := range want {
		runs, _ := store.ListSyntheticCheckRuns(ctx, id, 10)
		if len(runs) != 1 {
			t.Fatalf("check %s: %d runs", id, len(runs))
		}
		got := runs[0]
		if got.OK != w.OK || got.StatusCode != w.StatusCode || got.ErrorClass != w.ErrorClass {
			t.Errorf("check %s: got %+v, want %+v", id, got, w)
		}
	}
	// Nothing is due again until the interval passes; this pass reports the
	// probes that finished since the last one.
	if stats, _ := r.RunOnce(ctx); stats.Due != 0 || stats.OK != 1 || stats.Failed != 3 {
		t.Fatalf("second pass = %+v; want nothing due and the first pass's outcomes", stats)
	}
	if !strings.HasPrefix(URL(state.RunnableSyntheticCheck{AppSlug: "shop", SyntheticCheck: state.SyntheticCheck{Path: "/"}}, r.AppsDomain), "https://shop.") {
		t.Fatal("probe host must be the app's own")
	}
}

// adr: 20261104142830 — a pass never waits for its probes, and one account
// holds at most PerAccount of the slots: its hanging app delays only its own
// checks, which then start most overdue first.
func TestRunOnceKeepsOneAccountFromHoldingEverySlot(t *testing.T) {
	release := make(chan struct{})
	r, store, app := probeRunner(t, func(w http.ResponseWriter, req *http.Request) {
		if req.URL.Path == "/hang" {
			<-release
		}
		w.WriteHeader(http.StatusOK)
	})
	r.Concurrency, r.PerAccount = 4, 2
	ctx := context.Background()
	other, err := store.CreateAccount(ctx, "other@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	otherApp, err := store.CreateApp(ctx, state.App{AccountID: other.ID, Slug: "other", Type: state.AppTypeApp, RAMMB: 512, MaxConcurrency: 1, IdleTimeoutS: 300})
	if err != nil {
		t.Fatal(err)
	}
	mk := func(a state.App, name, path string) state.SyntheticCheck {
		c, err := store.CreateSyntheticCheck(ctx, state.SyntheticCheck{AccountID: a.AccountID, AppID: a.ID, Name: name, Method: "GET", Path: path, TimeoutMS: 30000, IntervalSeconds: 300}, api.MaxSyntheticChecksPerApp)
		if err != nil {
			t.Fatal(err)
		}
		return c
	}
	for _, name := range []string{"a", "b", "c"} {
		mk(app, name, "/hang")
	}
	fine := mk(otherApp, "fine", "/ok")

	stats, err := r.RunOnce(ctx)
	if err != nil || stats.Due != 4 || stats.Started != 3 || stats.Deferred != 1 {
		t.Fatalf("first pass = %+v, %v; want two hanging probes, the other account's, and one deferred", stats, err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		runs, _ := store.ListSyntheticCheckRuns(ctx, fine.ID, 10)
		if len(runs) == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the other account's check did not finish while the hanging probes held their slots")
		}
		time.Sleep(20 * time.Millisecond)
	}
	// The hanging probes are not started twice, and the third check still
	// waits for its account's slot.
	if stats, _ := r.RunOnce(ctx); stats.Due != 1 || stats.Started != 0 || stats.Deferred != 1 || stats.OK != 1 {
		t.Fatalf("second pass = %+v; want the third check still deferred", stats)
	}
	close(release)
	r.Wait()
	if stats, _ := r.RunOnce(ctx); stats.Started != 1 {
		t.Fatalf("third pass = %+v; want the deferred check started once a slot freed", stats)
	}
	r.Wait()
}

// adr: 20261104142830 — every probe carries the cookie that keeps the edge
// cache from answering it.
func TestProbeCarriesTheCacheBypassCookie(t *testing.T) {
	var got string
	r, store, app := probeRunner(t, func(w http.ResponseWriter, req *http.Request) {
		if c, err := req.Cookie(api.SyntheticProbeCookieName); err == nil {
			got = c.Value
		}
		w.WriteHeader(http.StatusOK)
	})
	c, err := store.CreateSyntheticCheck(context.Background(), state.SyntheticCheck{AccountID: app.AccountID, AppID: app.ID, Name: "home", Method: "GET", Path: "/", TimeoutMS: 5000, IntervalSeconds: 300}, api.MaxSyntheticChecksPerApp)
	if err != nil {
		t.Fatal(err)
	}
	if run := r.Probe(context.Background(), state.RunnableSyntheticCheck{SyntheticCheck: c, AppSlug: app.Slug}); !run.OK || got != "1" {
		t.Fatalf("run = %+v, cookie %q; want an OK run carrying the probe cookie", run, got)
	}
}
