package oci

import (
	"context"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
)

// The registry chooses the token realm, so an HTTPS registry must not be able
// to send the customer's Basic credential to a cleartext endpoint.
func TestFetchTokenRefusesCleartextRealmForCredentials(t *testing.T) {
	var requests atomic.Int32
	client := NewRegistryClient(WithHTTPClient(&http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		requests.Add(1)
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{"token":"t"}`))}, nil
	})}))
	auth := &BasicAuth{Username: "user", Password: "secret"}
	ctx := context.Background()

	if _, err := client.fetchToken(ctx, authChallenge{realm: "http://auth.example.com/token"}, auth); err == nil {
		t.Fatal("credentials were sent to a cleartext realm")
	}
	if requests.Load() != 0 {
		t.Fatalf("token requests = %d, want none", requests.Load())
	}
	if _, err := client.fetchToken(ctx, authChallenge{realm: "https://auth.example.com/token"}, auth); err != nil {
		t.Fatalf("https realm: %v", err)
	}
	// An anonymous token request carries no credential to protect.
	if _, err := client.fetchToken(ctx, authChallenge{realm: "http://auth.example.com/token"}, nil); err != nil {
		t.Fatalf("anonymous http realm: %v", err)
	}
	if requests.Load() != 2 {
		t.Fatalf("token requests = %d, want 2", requests.Load())
	}
}

func TestFetchTokenRealmCheckIgnoresPlainHTTPRegistries(t *testing.T) {
	client := NewRegistryClient(WithEndpoint("http", "registry.local"), WithHTTPClient(&http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(`{"token":"t"}`))}, nil
	})}))
	_, err := client.fetchToken(context.Background(), authChallenge{realm: "http://registry.local/token"}, &BasicAuth{Username: "u", Password: "p"})
	if err != nil {
		t.Fatalf("plain-HTTP test registry realm: %v", err)
	}
}
