package faas

import (
	"context"
	"encoding/base64"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestDevBridgePropagationDestinationBoundaries(t *testing.T) {
	token := base64.RawURLEncoding.EncodeToString(make([]byte, 32))
	authority := (DevBridgeRequestContext{AccountID: "account", SessionID: token, Token: token}).Encode()
	transport := DevBridgePropagationTransport{Base: roundTripperFunc(func(r *http.Request) (*http.Response, error) {
		want := ""
		if r.URL.Hostname() == "payments.svc.gregale" {
			want = authority
		}
		if r.Header.Get(DevBridgeContextHeader) != want {
			t.Errorf("context escaped destination scope: %s", r.URL.Hostname())
		}
		for key := range r.Header {
			if strings.HasPrefix(strings.ToLower(key), "x-gregale-dev-bridge-") {
				t.Error("attachment authority forwarded")
			}
		}
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("ok"))}, nil
	})}
	handler := DevBridgePropagationMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		for _, host := range []string{"payments.svc.gregale", "api.stripe.com", "extra.payments.svc.gregale"} {
			request, _ := http.NewRequestWithContext(r.Context(), "GET", "http://"+host+"/", nil)
			request.Header["x-gregale-dev-bridge-token"] = []string{"attachment-secret"}
			response, err := transport.RoundTrip(request)
			if err != nil {
				t.Fatal(err)
			}
			_ = response.Body.Close()
		}
	}))
	request := httptest.NewRequest("GET", "http://frontend/", nil)
	request.Header.Set(DevBridgeContextHeader, authority)
	handler.ServeHTTP(httptest.NewRecorder(), request)
}

func TestDevBridgeReplayRejectsRedirect(t *testing.T) {
	var received atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { received.Add(1); w.WriteHeader(200) }))
	defer target.Close()
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, target.URL, 307) }))
	defer source.Close()
	client, err := NewClient(source.URL, "api-key")
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.ReplayDevBridgeWebhook(context.Background(), "session", ReplayDevBridgeWebhookRequest{InvocationID: "receipt", RequestToken: "secret", IdempotencyKey: "once"})
	if err == nil || received.Load() != 0 {
		t.Fatalf("redirect followed: requests=%d err=%v", received.Load(), err)
	}
}

func TestDevBridgeCreationIsNotAutomaticallyRetried(t *testing.T) {
	var attempts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { attempts.Add(1); w.WriteHeader(503) }))
	defer server.Close()
	client, err := NewClient(server.URL, "api-key", WithRetry(2, time.Millisecond))
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.CreateDevBridge(context.Background(), CreateDevBridgeRequest{App: "payments", Environment: "development", DeveloperID: "alice"})
	if err == nil || attempts.Load() != 1 {
		t.Fatalf("creation retried: attempts=%d err=%v", attempts.Load(), err)
	}
}
