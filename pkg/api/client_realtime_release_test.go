package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestRealtimeReleaseSDKGroupRequestAndTypedResponse(t *testing.T) {
	// adr: 1111 — path escaping and typed group request/response preserve the REST contract.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.EscapedPath() != "/v1/apps/my%20app/realtime/endpoints/endpoint/channels/order%2Fevents/groups" {
			t.Errorf("unexpected method/path: %s %s", r.Method, r.URL.EscapedPath())
		}
		var input RealtimeConsumerGroupCreateRequest
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil || input.Name != "workers" || input.AfterSequence != "0" {
			t.Errorf("unexpected group request: %v %+v", err, input)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"group_id":"group-1","name":"workers","channel":"order/events","assigned_sequence":"0"}`))
	}))
	defer server.Close()
	out, err := NewClient(server.URL, "account-fixture").CreateRealtimeConsumerGroup(context.Background(), "my app", "endpoint", "order/events", RealtimeConsumerGroupCreateRequest{Name: "workers", AfterSequence: "0"})
	if err != nil || out.GroupID != "group-1" || out.Name != "workers" {
		t.Fatalf("group result: %+v %v", out, err)
	}
}

func TestRealtimeReleaseSDKPublisherTTLAndRetryIdentity(t *testing.T) {
	// adr: 1111 — scoped publish preserves caller identity and explicit zero TTL.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer grp_fixture" || r.Header.Get("Idempotency-Key") != "stable-key" {
			t.Error("scoped auth or idempotency key changed")
		}
		var input map[string]any
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil || input["ttl_seconds"] != float64(0) || input["event_type"] != "job.updated" {
			t.Errorf("unexpected publish: %v %+v", err, input)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"sequence":"42","durable":true,"replayed":true,"partial":false}`))
	}))
	defer server.Close()
	ttl := 0
	key := "stable-key"
	out, err := NewClient(server.URL, "grp_fixture").PublishWithRealtimePublisherCredential(context.Background(), "endpoint", "jobs", RealtimePublisherMessageRequest{TTLSeconds: &ttl, EventType: "job.updated", DataBase64: "e30="}, &PublishWithRealtimePublisherCredentialOptions{IdempotencyKey: &key})
	if err != nil || out.Sequence != "42" || !out.Durable || !out.Replayed {
		t.Fatalf("publish result: %+v %v", out, err)
	}
	_, err = NewClient(server.URL, "grp_fixture").PublishWithRealtimePublisherCredential(context.Background(), "endpoint", "jobs", RealtimePublisherMessageRequest{EventType: "job.updated", DataBase64: "e30="}, nil)
	if err == nil || !strings.Contains(err.Error(), "Idempotency-Key is required") {
		t.Fatalf("missing stable key accepted: %v", err)
	}
}

func TestRealtimeReleaseSDKScopedRedirectAndCookieIsolation(t *testing.T) {
	// adr: 1111 — scoped credentials never follow redirects or inherit account cookies.
	reached := false
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { reached = true; w.WriteHeader(http.StatusOK) }))
	defer target.Close()
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Cookie") != "" {
			t.Error("account cookie inherited by scoped operation")
		}
		http.Redirect(w, r, target.URL, http.StatusFound)
	}))
	defer source.Close()
	client := NewClient(source.URL, "grw_fixture")
	jar, _ := cookiejar.New(nil)
	origin, _ := url.Parse(source.URL)
	jar.SetCookies(origin, []*http.Cookie{{Name: "account_session", Value: "fixture"}})
	client.HTTPClient().Jar = jar
	_, err := client.GetRealtimeWorkerGroup(context.Background(), "group")
	if err == nil || reached {
		t.Fatalf("scoped redirect followed: reached=%v err=%v", reached, err)
	}
}

func TestRealtimeReleaseSDKDeadLetterExportStreamsJSONL(t *testing.T) {
	// adr: 1111 — JSONL exports are streamed, not decoded as one JSON value.
	const lines = "{\"sequence\":\"1\"}\n{\"sequence\":\"2\"}\n"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/x-ndjson")
		_, _ = w.Write([]byte(lines))
	}))
	defer server.Close()
	var out bytes.Buffer
	count, err := NewClient(server.URL, "account-fixture").ExportRealtimeConsumerGroupDeadLetters(context.Background(), "app", "endpoint", "channel", "group", nil, &out)
	if err != nil || count != int64(len(lines)) || out.String() != lines {
		t.Fatalf("export mismatch: %d %v %q", count, err, out.String())
	}
}
