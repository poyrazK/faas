package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestDataAPIServingContract(t *testing.T) {
	for _, scenario := range []string{"match", "mismatch", "invalid", "oversized", "unauthorized", "unready", "version", "redirect", "timeout"} {
		t.Run(scenario, func(t *testing.T) {
			var leaked atomic.Int32
			other := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { leaked.Add(1) }))
			defer other.Close()
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/__gregale/schema" || r.Header.Get("Authorization") != "Bearer secret.token.sentinel" || r.Header.Get("Cookie") != "" {
					t.Error("unexpected contract request or credentials")
				}
				switch scenario {
				case "redirect":
					http.Redirect(w, r, other.URL, http.StatusTemporaryRedirect)
					return
				case "timeout":
					<-r.Context().Done()
					return
				case "unauthorized":
					w.WriteHeader(http.StatusUnauthorized)
					_, _ = w.Write([]byte("secret.token.sentinel"))
					return
				case "oversized":
					_, _ = w.Write([]byte(strings.Repeat("x", 8193)))
					return
				case "invalid":
					_, _ = w.Write([]byte("secret.token.sentinel"))
					return
				}
				fingerprint, ready, version := strings.Repeat("a", 64), true, 1
				if scenario == "mismatch" {
					fingerprint = strings.Repeat("b", 64)
				}
				if scenario == "unready" {
					ready = false
				}
				if scenario == "version" {
					version = 2
				}
				writeJSONTest(w, map[string]any{"version": version, "ready": ready, "fingerprint": fingerprint})
			}))
			defer server.Close()
			original := http.DefaultTransport
			http.DefaultTransport = server.Client().Transport
			defer func() { http.DefaultTransport = original }()
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			if scenario == "timeout" {
				cancel()
				ctx, cancel = context.WithTimeout(context.Background(), 100*time.Millisecond)
				defer cancel()
			}
			err := verifyDataAPIServingContract(ctx, server.URL+"/healthz", "secret.token.sentinel", strings.Repeat("a", 64))
			if (err == nil) != (scenario == "match") {
				t.Fatalf("unexpected contract result: %v", err)
			}
			if err != nil && strings.Contains(err.Error(), "secret.token.sentinel") {
				t.Fatal("error leaked token or response body")
			}
			if leaked.Load() != 0 {
				t.Fatal("followed a contract redirect")
			}
		})
	}
}
