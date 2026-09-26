package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestGetServiceCallerKeysIsUnauthenticated(t *testing.T) {
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		if got := r.Header.Get("Authorization"); got != "" {
			t.Errorf("Authorization = %q, want no bearer token", got)
		}
		w.Header().Set("Content-Type", "application/jwk-set+json")
		_ = json.NewEncoder(w).Encode(ServiceCallerJWKSet{Keys: []ServiceCallerJWK{{
			Kty: "OKP", Crv: "Ed25519", Kid: "key-id", X: "public-key", Alg: "EdDSA", Use: "sig",
		}}})
	}))
	t.Cleanup(srv.Close)

	client := NewClient(srv.URL, "")
	client.SetCompletionCache(nil)
	got, err := client.GetServiceCallerKeys(context.Background())
	if err != nil {
		t.Fatalf("GetServiceCallerKeys: %v", err)
	}
	if gotPath != "/v1/service-caller-keys" {
		t.Fatalf("path = %q", gotPath)
	}
	if len(got.Keys) != 1 || got.Keys[0].Kid != "key-id" {
		t.Fatalf("keys = %+v", got.Keys)
	}
}
