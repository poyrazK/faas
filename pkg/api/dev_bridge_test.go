package api

import (
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

func TestDevBridgeWebhookReplayDoesNotRedirectCredentials(t *testing.T) {
	var forwarded atomic.Int32
	destination := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { forwarded.Add(1); w.WriteHeader(200) }))
	defer destination.Close()
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, destination.URL, 307) }))
	defer source.Close()
	client := NewClient(source.URL, "account-api-key").SetCompletionCache(nil)
	_, err := client.ReplayDevBridgeWebhook(t.Context(), "session", ReplayDevBridgeWebhookRequest{InvocationID: "receipt", RequestToken: "scoped-secret", IdempotencyKey: "once"})
	if err == nil || forwarded.Load() != 0 {
		t.Fatalf("redirect followed: requests=%d err=%v", forwarded.Load(), err)
	}
}
