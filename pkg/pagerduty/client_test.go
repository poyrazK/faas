package pagerduty

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

type localTransport struct {
	base   http.RoundTripper
	target *url.URL
	check  func(*http.Request)
}

func (r localTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if r.check != nil {
		r.check(req)
	}
	copy := req.Clone(req.Context())
	u := *req.URL
	u.Scheme = r.target.Scheme
	u.Host = r.target.Host
	copy.URL = &u
	return r.base.RoundTrip(copy)
}
func TestMCPReadinessPagerDutyTransport(t *testing.T) {
	at := time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC)
	incident := "00000000-0000-0000-0000-000000000001"
	event := Event{IncidentID: incident, EventID: incident, Kind: "alert.fired", RuleName: strings.Repeat("界", 600), AppID: incident, RuleID: incident, OccurredAt: at}
	key := strings.Repeat("a", 32)
	var calls atomic.Int32
	var mode atomic.Value
	mode.Store("accepted")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.URL.Path != "/v2/enqueue" || r.Method != "POST" || r.Header.Get("Content-Type") != "application/json" || r.Header.Get("X-Faas-Alert-Signature") != "" {
			t.Error("wrong request", r.Method, r.URL)
		}
		var body envelope
		if e := json.NewDecoder(r.Body).Decode(&body); e != nil {
			t.Error(e)
		}
		if body.RoutingKey != key || body.DedupKey != "gregale:mcp:"+incident {
			t.Error("lost integration or incident identity")
		}
		if body.EventAction == "resolve" {
			if body.EventAction != "resolve" || body.Payload != nil {
				t.Error(body.EventAction)
			}
		} else if body.EventAction != "trigger" || body.Payload == nil || len(body.Payload.Summary) > api.MCPReadinessPagerDutySummaryMaxBytes {
			t.Error("invalid trigger")
		}
		switch mode.Load().(string) {
		case "accepted":
			w.WriteHeader(202)
			_ = json.NewEncoder(w).Encode(map[string]string{"status": "success", "dedup_key": body.DedupKey})
		case "wrong-key":
			w.WriteHeader(202)
			_, _ = fmt.Fprint(w, `{"status":"success","dedup_key":"wrong"}`)
		case "oversize":
			w.WriteHeader(202)
			_, _ = fmt.Fprint(w, strings.Repeat("x", api.MCPReadinessPagerDutyResponseMaxBytes+1))
		case "rate-limit":
			w.Header().Set("Retry-After", "999999999")
			w.WriteHeader(429)
			_, _ = fmt.Fprint(w, key)
		case "server-error":
			w.Header().Set("Retry-After", at.Add(time.Hour).Format(http.TimeFormat))
			w.WriteHeader(503)
		case "redirect":
			w.Header().Set("Location", serverRedirectURL)
			w.WriteHeader(307)
		default:
			w.WriteHeader(400)
		}
	}))
	defer server.Close()
	target, _ := url.Parse(server.URL)
	client := New(&http.Client{Transport: localTransport{base: server.Client().Transport, target: target}})
	now := func() time.Time { return at }
	for _, kind := range []string{"alert.fired", "alert.fired", "alert.resolved"} {
		event.Kind = kind
		r := client.Send(t.Context(), key, api.MCPReadinessPagerDutyEndpoint, event, now)
		if !r.Accepted || r.StatusCode != 202 {
			t.Fatal(r)
		}
	}
	event.Kind = "alert.fired"
	for _, v := range []struct {
		mode, failure string
		code          int
	}{{"wrong-key", "invalid_acknowledgement", 202}, {"oversize", "invalid_acknowledgement", 202}, {"rate-limit", "provider_rejected", 429}, {"server-error", "provider_rejected", 503}, {"redirect", "provider_rejected", 307}, {"bad-key", "provider_rejected", 400}} {
		mode.Store(v.mode)
		before := calls.Load()
		r := client.Send(t.Context(), key, api.MCPReadinessPagerDutyEndpoint, event, now)
		if r.Accepted || r.Failure != v.failure || r.StatusCode != v.code || calls.Load() != before+1 {
			t.Fatal(v, r, calls.Load())
		}
		if v.mode == "rate-limit" && (r.RetryAfter == nil || r.RetryAfter.Sub(at) != 24*time.Hour) {
			t.Fatal(r)
		}
		if v.mode == "server-error" && (r.RetryAfter == nil || r.RetryAfter.Sub(at) != time.Hour) {
			t.Fatal(r)
		}
		if strings.Contains(fmt.Sprint(r), key) {
			t.Fatal("provider response leaked credential")
		}
	}
	before := calls.Load()
	event.Kind = "unknown"
	if r := client.Send(t.Context(), key, api.MCPReadinessPagerDutyEndpoint, event, now); r.Failure != "invalid_event" || calls.Load() != before {
		t.Fatal(r)
	}
}

const serverRedirectURL = "http://127.0.0.1:1/credential-leak"

func TestMCPReadinessPagerDutyRegionalEndpoints(t *testing.T) {
	incident := "00000000-0000-0000-0000-000000000001"
	event := Event{IncidentID: incident, EventID: incident, Kind: "alert.fired", OccurredAt: time.Now()}
	key := strings.Repeat("a", 32)
	var calls int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body envelope
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		w.WriteHeader(202)
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "success", "dedup_key": body.DedupKey})
	}))
	defer server.Close()
	target, _ := url.Parse(server.URL)
	for _, endpoint := range []string{api.MCPReadinessPagerDutyEndpoint, api.MCPReadinessPagerDutyEUEndpoint} {
		client := New(&http.Client{Transport: localTransport{base: server.Client().Transport, target: target, check: func(r *http.Request) {
			calls++
			if r.URL.String() != endpoint {
				t.Error("wrong regional endpoint", r.URL)
			}
		}}})
		for _, kind := range []string{"alert.fired", "alert.resolved"} {
			event.Kind = kind
			if r := client.Send(t.Context(), key, endpoint, event, time.Now); !r.Accepted {
				t.Fatal(r)
			}
		}
		for _, invalid := range []string{"", "eu", "http://events.eu.pagerduty.com/v2/enqueue", "https://events.eu.pagerduty.com/v2/enqueue?key=leak", "https://events.eu.pagerduty.com.evil.example/v2/enqueue", "http://127.0.0.1/v2/enqueue"} {
			before := calls
			if r := client.Send(t.Context(), key, invalid, event, time.Now); r.Failure != "invalid_event" || calls != before {
				t.Fatal("invalid endpoint reached transport", invalid, r)
			}
		}
	}
	if calls != 4 {
		t.Fatal(calls)
	}
}

func TestMCPReadinessPagerDutyRetryAfter(t *testing.T) {
	at := time.Now().UTC()
	for _, s := range []string{"-1", "0", "garbage", at.Add(-time.Hour).Format(http.TimeFormat)} {
		if retryAfter(s, at) != nil {
			t.Fatal(s)
		}
	}
}
