package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestManagedCLISession_LogoutRevokesOnlyTrackedKey(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("XDG_CONFIG_HOME", dir)
	t.Setenv("FAAS_TOKEN", "")
	setFakeKeyring(t)
	token := testAPIKey('a')
	deleted := ""
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/apps":
			_, _ = w.Write([]byte(`[]`))
		case r.Method == http.MethodDelete && r.URL.Path == "/v1/keys/cli-key-1":
			if r.Header.Get("Authorization") != "Bearer "+token {
				t.Errorf("authorization = %q", r.Header.Get("Authorization"))
			}
			deleted = "cli-key-1"
			w.WriteHeader(http.StatusNoContent)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	if code := finalizeLogin(context.Background(), NewClient(srv.URL, ""), token, "cli-key-1",
		api.AccountResponse{Email: "managed@example.com", Plan: string(api.PlanFree)}); code != 0 {
		t.Fatalf("finalizeLogin = %d", code)
	}
	if code := cmdLogout(); code != 0 {
		t.Fatalf("cmdLogout = %d", code)
	}
	if deleted != "cli-key-1" {
		t.Fatalf("deleted key = %q", deleted)
	}
	if loadToken() != "" {
		t.Fatal("token still present after logout")
	}
	if p, _ := cliSessionMetadataPath(); p != "" {
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Fatalf("session metadata still exists: %v", err)
		}
	}
}

func TestNonOwningToken_LogoutDoesNotRevokeServerKey(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("XDG_CONFIG_HOME", dir)
	t.Setenv("FAAS_TOKEN", "")
	setFakeKeyring(t)
	deletes := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodDelete {
			deletes++
		}
		if r.URL.Path == "/v1/apps" {
			_, _ = w.Write([]byte(`[]`))
			return
		}
		http.NotFound(w, r)
	}))
	defer srv.Close()
	if code := finalizeLogin(context.Background(), NewClient(srv.URL, ""), testAPIKey('b'), "",
		api.AccountResponse{Email: "shared@example.com", Plan: string(api.PlanFree)}); code != 0 {
		t.Fatalf("finalizeLogin = %d", code)
	}
	if code := cmdLogout(); code != 0 {
		t.Fatalf("cmdLogout = %d", code)
	}
	if deletes != 0 {
		t.Fatalf("DELETE calls = %d, want 0", deletes)
	}
}
