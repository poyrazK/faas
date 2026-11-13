package dataapiops

import (
	"context"
	"github.com/onebox-faas/faas/pkg/state"
	"io"
	"net/http"
	"strings"
	"testing"
)

type transport func(*http.Request) (*http.Response, error)

func (f transport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func TestProbeBoundsAndRedactsRuntimeObservations(t *testing.T) {
	const good = `{"version":1,"live":true,"ready":true,"runtime_version":"aaaaaaaaaaaaaaaa","checks":{"engine":"reachable","sql":"ready","database":"reachable","refresh":"disabled"},"admission":{"active":1,"queued":2,"capacity":2}}`
	for _, tc := range []struct{ body, want string }{{good, "ready"}, {strings.Replace(good, `"capacity":2`, `"capacity":200`, 1), "unknown"}, {strings.Repeat("x", 9000), "unknown"}, {`{"error":"private details"}`, "unknown"}} {
		c := &http.Client{Transport: transport(func(r *http.Request) (*http.Response, error) {
			if len(r.Header.Values("Authorization")) > 0 || r.URL.String() != "https://app.example/healthz" {
				t.Fatal("unsafe probe")
			}
			return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(tc.body)), Header: make(http.Header)}, nil
		})}
		got := Probe(context.Background(), c, "https://app.example", "bbbbbbbbbbbbbbbb")
		if got.State != tc.want {
			t.Fatalf("got %+v", got)
		}
		if got.State == "ready" && got.RuntimeState != "update available" {
			t.Fatal(got)
		}
		if got := Probe(context.Background(), c, "http://127.0.0.1", ""); got.State != "unknown" {
			t.Fatal(got)
		}
	}
}
func TestSampleKeepsLatencyAndHTTPFailuresSeparateFromHealth(t *testing.T) {
	latencies := []int{1, 10, 50, 100, 200}
	events := []state.LogEvent{}
	for i := range latencies {
		events = append(events, state.LogEvent{Source: state.LogEventSourceHTTP, Status: 200, LatencyMS: &latencies[i]})
	}
	events[0].Status = 429
	events[1].Status = 503
	got := Summarize(events, true)
	if got.Requests != 5 || got.RateLimited != 1 || got.Overloaded != 1 || got.Errors != 1 || got.P95 == nil || *got.P95 != 200 || !got.Partial {
		t.Fatal(got)
	}
	if Summarize(nil, false).P95 != nil {
		t.Fatal("missing latency became zero")
	}
}
