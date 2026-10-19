package main_test

import (
	"context"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
)

func TestManagedCLISession_LogoutRevokesOnlyTrackedKey(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("XDG_CONFIG_HOME", dir)
	t.Setenv("FAAS_TOKEN", "")
	main.SetFakeKeyring(t)
	token := main.TestAPIKey('a')
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

	if code := main.FinalizeLogin(context.Background(), main.NewClient(srv.URL, ""), token, "cli-key-1",
		api.AccountResponse{Email: "managed@example.com", Plan: string(api.PlanFree)}); code != 0 {
		t.Fatalf("finalizeLogin = %d", code)
	}
	if code := main.CmdLogout(); code != 0 {
		t.Fatalf("cmdLogout = %d", code)
	}
	if deleted != "cli-key-1" {
		t.Fatalf("deleted key = %q", deleted)
	}
	if main.LoadToken() != "" {
		t.Fatal("token still present after logout")
	}
	if p, _ := main.CliSessionMetadataPath(); p != "" {
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
	main.SetFakeKeyring(t)
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
	if code := main.FinalizeLogin(context.Background(), main.NewClient(srv.URL, ""), main.TestAPIKey('b'), "",
		api.AccountResponse{Email: "shared@example.com", Plan: string(api.PlanFree)}); code != 0 {
		t.Fatalf("finalizeLogin = %d", code)
	}
	if code := main.CmdLogout(); code != 0 {
		t.Fatalf("cmdLogout = %d", code)
	}
	if deletes != 0 {
		t.Fatalf("DELETE calls = %d, want 0", deletes)
	}
}

func TestManagedSessionFingerprintRejectsReplacedToken(t *testing.T) {
	salt := []byte("01234567890123456789012345678901")
	meta := main.CliSessionMetadata{
		KeyID:            "cli-key-1",
		Managed:          true,
		FingerprintSalt:  base64.RawURLEncoding.EncodeToString(salt),
		TokenFingerprint: main.TokenFingerprint(main.TestAPIKey('a'), salt),
	}
	if !meta.Matches(main.TestAPIKey('a')) {
		t.Fatal("managed token did not match its fingerprint")
	}
	if meta.Matches(main.TestAPIKey('b')) {
		t.Fatal("replacement token matched stale session metadata")
	}
}

func TestLegacyManagedSessionIsConsumedForRevocation(t *testing.T) {
	meta := main.CliSessionMetadata{KeyID: "legacy-key", Managed: true, TokenSHA256: "legacy-digest"}
	if !meta.Matches(main.TestAPIKey('a')) {
		t.Fatal("legacy managed metadata must permit one final server revocation")
	}
}
