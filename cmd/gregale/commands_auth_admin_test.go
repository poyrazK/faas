package main_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
)

func captureAuthStdout(t *testing.T) *bytes.Buffer {
	t.Helper()
	var out bytes.Buffer
	old := (*main.OsStdout)
	(*main.OsStdout) = &out
	t.Cleanup(func() { (*main.OsStdout) = old })
	return &out
}

func TestAuthAdminKeyCreateStoresOnlyTheHash(t *testing.T) {
	resetJSONEnv(t)
	sealed := map[string]string{}
	restarts := 0
	authIssuerServer(t, func(w http.ResponseWriter, r *http.Request) bool {
		switch {
		case r.Method == "PUT" && strings.HasPrefix(r.URL.Path, "/v1/apps/shop-auth/secrets/"):
			var body api.PutAppSecretRequest
			_ = json.NewDecoder(r.Body).Decode(&body)
			sealed[strings.TrimPrefix(r.URL.Path, "/v1/apps/shop-auth/secrets/")] = body.Value
			main.WriteJSONTest(w, map[string]any{})
		case r.Method == "POST" && r.URL.Path == "/v1/apps/shop-auth/restart":
			restarts++
			main.WriteJSONTest(w, map[string]any{"app_id": "auth-app", "wake_id": "w1"})
		default:
			return false
		}
		return true
	})
	out := captureAuthStdout(t)
	if code := main.CmdAuth([]string{"admin-key", "create", "shop-auth"}); code != 0 {
		t.Fatalf("create = %d", code)
	}
	key := regexp.MustCompile(`gaak_[A-Za-z0-9_-]{43}`).FindString(out.String())
	if key == "" || len(sealed) != 1 || restarts != 1 {
		t.Fatalf("key=%q sealed=%v restarts=%d\n%s", key, sealed, restarts, out.String())
	}
	sum := sha256.Sum256([]byte(key))
	for name, value := range sealed {
		if !(*main.AuthAdminKeyName).MatchString(name) || value != hex.EncodeToString(sum[:]) || strings.Contains(value, key) {
			t.Fatalf("sealed %s=%s, want the key's SHA-256 only", name, value)
		}
	}
	if !strings.Contains(out.String(), "/dashboard/apps/shop-auth/auth") {
		t.Fatalf("no dashboard link:\n%s", out.String())
	}
}

func TestAuthAdminKeyListAndRevoke(t *testing.T) {
	resetJSONEnv(t)
	unset := ""
	authIssuerServer(t, func(w http.ResponseWriter, r *http.Request) bool {
		switch {
		case r.Method == "GET" && r.URL.Path == "/v1/apps/shop-auth/secrets":
			main.WriteJSONTest(w, api.AppSecretListResponse{Secrets: []api.AppSecretResponse{{Key: "AUTH_ADMIN_KEY_0A1B2C3D", CreatedAt: "2026-10-14T00:00:00Z"}, {Key: "AUTH_JWT_SECRET"}, {Key: "AUTH_ADMIN_KEY_lower"}}})
		case r.Method == "DELETE" && strings.HasPrefix(r.URL.Path, "/v1/apps/shop-auth/secrets/"):
			unset = strings.TrimPrefix(r.URL.Path, "/v1/apps/shop-auth/secrets/")
			w.WriteHeader(http.StatusNoContent)
		case r.Method == "POST" && r.URL.Path == "/v1/apps/shop-auth/restart":
			main.WriteJSONTest(w, map[string]any{"app_id": "auth-app", "wake_id": "w1"})
		default:
			return false
		}
		return true
	})
	out := captureAuthStdout(t)
	if code := main.CmdAuth([]string{"admin-key", "list", "shop-auth"}); code != 0 || !strings.Contains(out.String(), "0A1B2C3D") || strings.Contains(out.String(), "JWT") || strings.Contains(out.String(), "lower") {
		t.Fatalf("list = %d\n%s", code, out.String())
	}
	if code := main.CmdAuth([]string{"admin-key", "revoke", "shop-auth", "0A1B2C3D"}); code != 0 || unset != "AUTH_ADMIN_KEY_0A1B2C3D" {
		t.Fatalf("revoke = %d unset=%q", code, unset)
	}
	if code := main.CmdAuth([]string{"admin-key", "revoke", "shop-auth", "../JWT"}); code == 0 {
		t.Fatal("revoked a malformed ID")
	}
}

type authRoundTrip func(*http.Request) (*http.Response, error)

func (f authRoundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestAuthImportValidatesBatchesAndMapsRecords(t *testing.T) {
	resetJSONEnv(t)
	authIssuerServer(t, func(http.ResponseWriter, *http.Request) bool { return false })
	key := "gaak_" + strings.Repeat("k", 43)
	var batches [][]map[string]any
	auth := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/admin/v1/users/import" || r.Header.Get("Authorization") != "Bearer "+key {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		var body struct {
			Users []map[string]any `json:"users"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		batches = append(batches, body.Users)
		skipped := []map[string]any{}
		for i, user := range body.Users {
			if user["email"] == "exists@example.test" {
				skipped = append(skipped, map[string]any{"index": i, "code": "email_exists"})
			}
		}
		main.WriteJSONTest(w, map[string]any{"created": len(body.Users) - len(skipped), "skipped": skipped})
	})
	old := (*main.AuthAdminHTTPClient)
	(*main.AuthAdminHTTPClient) = &http.Client{Transport: authRoundTrip(func(r *http.Request) (*http.Response, error) {
		if r.URL.Host != "shop-auth.gregale.app" {
			t.Errorf("import went to %s", r.URL.Host)
		}
		recorder := httptest.NewRecorder()
		auth.ServeHTTP(recorder, r)
		return recorder.Result(), nil
	})}
	t.Cleanup(func() { (*main.AuthAdminHTTPClient) = old })

	var lines []string
	for i := 0; i < api.AuthImportBatch+5; i++ {
		lines = append(lines, fmt.Sprintf(`{"email":"user%03d@example.test"}`, i))
	}
	lines = append(lines,
		`{"email":"exists@example.test","password_hash":"$2b$10$N9qo8uLOickgx2ZMRZoMyeIjZAgcfl7p92ldGxad68LJZdL17lhWy","email_confirmed":true}`,
		`{"email":"plain@example.test","password":"hunter2"}`,
		`{"email":"md5@example.test","password_hash":"5f4dcc3b5aa765d61d8327deb882cf99"}`,
		`{"email":"EXISTS@example.test"}`,
		``,
	)
	path := filepath.Join(t.TempDir(), "users.jsonl")
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")), 0o600); err != nil {
		t.Fatal(err)
	}
	out := captureAuthStdout(t)
	if code := main.CmdAuth([]string{"import", "shop-auth", "--file", path, "--dry-run"}); code != 1 || len(batches) != 0 {
		t.Fatalf("dry run = %d batches=%d", code, len(batches))
	}
	for _, want := range []string{"record 107: unknown_field", "record 108: unsupported_password_hash", "record 109: duplicate_in_file"} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("dry run output lacks %q:\n%s", want, out.String())
		}
	}
	oldStdin := (*main.OsStdin)
	(*main.OsStdin) = strings.NewReader(key + "\n")
	t.Cleanup(func() { (*main.OsStdin) = oldStdin })
	out.Reset()
	code := main.CmdAuth([]string{"import", "shop-auth", "--file", path, "--admin-key-stdin"})
	if code != 1 || len(batches) != 2 || len(batches[0]) != api.AuthImportBatch || len(batches[1]) != 6 {
		t.Fatalf("import = %d batches=%d\n%s", code, len(batches), out.String())
	}
	if !strings.Contains(out.String(), "Imported 105 of 109 users") || !strings.Contains(out.String(), "record 106: email_exists\n  record 107: unknown_field") {
		t.Fatalf("unexpected summary:\n%s", out.String())
	}
	if _, ok := batches[1][5]["password"]; ok {
		t.Fatal("an invalid record was sent")
	}
}

func TestAuthAdminKeyListJSON(t *testing.T) {
	resetJSONEnv(t)
	authIssuerServer(t, func(w http.ResponseWriter, r *http.Request) bool {
		if r.Method != "GET" || r.URL.Path != "/v1/apps/shop-auth/secrets" {
			return false
		}
		main.WriteJSONTest(w, api.AppSecretListResponse{Secrets: []api.AppSecretResponse{{Key: "AUTH_ADMIN_KEY_0A1B2C3D", CreatedAt: "2026-10-14T00:00:00Z"}, {Key: "AUTH_JWT_SECRET"}}})
		return true
	})
	out := captureAuthStdout(t)
	(*main.JsonOutput) = true
	t.Cleanup(main.ResetJSONOutput) // later tests expect text output
	if code := main.CmdAuth([]string{"admin-key", "list", "shop-auth"}); code != 0 {
		t.Fatalf("list = %d", code)
	}
	var body struct {
		AdminKeys []struct {
			ID        string `json:"id"`
			CreatedAt string `json:"created_at"`
		} `json:"admin_keys"`
	}
	if err := json.Unmarshal(out.Bytes(), &body); err != nil || len(body.AdminKeys) != 1 || body.AdminKeys[0].ID != "0A1B2C3D" {
		t.Fatalf("json = %s (%v)", out.String(), err)
	}
}

func TestAuthActivityReadsTheAdminAPIWithTheOwnerKey(t *testing.T) {
	resetJSONEnv(t)
	authIssuerServer(t, func(http.ResponseWriter, *http.Request) bool { return false })
	key := "gaak_" + strings.Repeat("k", 43)
	var seen *http.Request
	old := (*main.AuthAdminHTTPClient)
	(*main.AuthAdminHTTPClient) = &http.Client{Transport: authRoundTrip(func(r *http.Request) (*http.Response, error) {
		seen = r
		recorder := httptest.NewRecorder()
		if r.Header.Get("Authorization") != "Bearer "+key || r.Method != http.MethodGet {
			recorder.WriteHeader(http.StatusUnauthorized)
			return recorder.Result(), nil
		}
		main.WriteJSONTest(recorder, map[string]any{"entries": []map[string]any{
			{"id": "e1", "created_at": "2026-10-15T10:00:00Z", "action": "login", "user_id": "u1", "email": "a@example.test"},
			{"id": "e2", "created_at": "2026-10-15T09:00:00Z", "action": "user_signedup", "user_id": "u2", "email": nil},
			{"id": "e3", "created_at": "2026-10-15T08:00:00Z", "action": "user_deleted", "actor": "administrator", "user_id": "u3", "email": "c@example.test"},
		}, "more": true})
		return recorder.Result(), nil
	})}
	t.Cleanup(func() { (*main.AuthAdminHTTPClient) = old })
	oldIn := (*main.OsStdin)
	(*main.OsStdin) = strings.NewReader(key + "\n")
	t.Cleanup(func() { (*main.OsStdin) = oldIn })
	out := captureAuthStdout(t)
	if code := main.CmdAuth([]string{"activity", "shop-auth", "--search", "login", "--limit", "20", "--admin-key-stdin"}); code != 0 {
		t.Fatalf("activity = %d", code)
	}
	if seen.URL.Host != "shop-auth.gregale.app" || seen.URL.Path != "/admin/v1/activity" || seen.URL.Query().Get("search") != "login" || seen.URL.Query().Get("per_page") != "20" || seen.URL.Query().Has("user") {
		t.Fatalf("request = %s", seen.URL)
	}
	text := out.String()
	for _, want := range []string{"login", "a@example.test", "u2", "user_deleted (administrator)", "More entries exist"} {
		if !strings.Contains(text, want) {
			t.Fatalf("output lacks %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, key) {
		t.Fatal("printed the admin key")
	}
	if code := main.CmdAuth([]string{"activity", "shop-auth", "--limit", "500"}); code == 0 {
		t.Fatal("accepted a limit above 100")
	}
}

func TestRestartAuthAppRetriesWhileARestartIsRunning(t *testing.T) {
	resetJSONEnv(t)
	attempts := 0
	authIssuerServer(t, func(w http.ResponseWriter, r *http.Request) bool {
		if r.Method != "POST" || r.URL.Path != "/v1/apps/shop-auth/restart" {
			return false
		}
		attempts++
		if attempts < 3 {
			w.Header().Set("Content-Type", "application/problem+json")
			w.WriteHeader(http.StatusConflict)
			main.WriteJSONTest(w, map[string]any{"status": 409, "code": "conflict", "title": "Restart already in progress"})
			return true
		}
		main.WriteJSONTest(w, map[string]any{"app_id": "auth-app", "wake_id": "w1"})
		return true
	})
	oldWindow, oldPause := (*main.AuthRestartRetryWindow), (*main.AuthRestartRetryPause)
	(*main.AuthRestartRetryWindow), (*main.AuthRestartRetryPause) = time.Minute, time.Millisecond
	t.Cleanup(func() { (*main.AuthRestartRetryWindow), (*main.AuthRestartRetryPause) = oldWindow, oldPause })
	client, err := main.AuthedClient()
	if err != nil {
		t.Fatal(err)
	}
	var stderr bytes.Buffer
	old := (*main.OsStderr)
	(*main.OsStderr) = &stderr
	t.Cleanup(func() { (*main.OsStderr) = old })
	main.RestartAuthApp(t.Context(), client, "shop-auth")
	if attempts != 3 || stderr.Len() != 0 {
		t.Fatalf("attempts = %d, warnings = %q", attempts, stderr.String())
	}
}
