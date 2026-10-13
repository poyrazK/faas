// adr: 1190 — application Auth reuses the app, binding and deploy lifecycles.
package main

import (
	"bufio"
	"crypto/ecdh"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestAuthCreateDeploysRuntimeAfterConfiguration(t *testing.T) {
	for _, tc := range []struct {
		name   string
		resume bool
	}{{name: "create"}, {name: "resume-keeps-keys", resume: true}} {
		t.Run(tc.name, func(t *testing.T) { testAuthCreateDeploysRuntime(t, tc.resume) })
	}
}

func testAuthCreateDeploysRuntime(t *testing.T, resume bool) {
	resetJSONOut(t)
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("FAAS_TOKEN", "test-token")
	t.Chdir(t.TempDir())
	const database = "00000000-0000-0000-0000-000000000001"
	var dockerfile, source int32
	base := zeroConfigDeployServer(t, "shop-auth", &dockerfile, &source, nil)
	defer base.Close()
	secrets := map[string]string{}
	created, patched := 0, 0
	var bindingAccess string
	app := map[string]any{"id": "app", "slug": "shop-auth", "type": "app", "url": "https://shop-auth.gregale.app", "require_authn": true, "public_auth": map[string]string{"mode": "bearer"}}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == "GET" && r.URL.Path == "/v1/postgres/databases":
			writeJSONTest(w, api.ManagedPostgresDatabaseList{Items: []api.ManagedPostgresDatabase{{ID: database, State: "ready"}}})
		case r.Method == "POST" && r.URL.Path == "/v1/apps":
			var body api.CreateAppRequest
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.RequireAuthn == nil || *body.RequireAuthn {
				t.Error("end-user sign-in would be blocked by the paid-plan owner-key default", err)
			}
			created++
			writeJSONTest(w, app)
		case r.Method == "GET" && r.URL.Path == "/v1/apps/shop-auth":
			writeJSONTest(w, app)
		case r.Method == "GET" && r.URL.Path == "/v1/apps/shop-auth/secrets":
			list := api.AppSecretListResponse{}
			if resume {
				list.Secrets = []api.AppSecretResponse{{Key: authSigningKeysName}, {Key: authJWTSecretName}}
			}
			writeJSONTest(w, list)
		case r.Method == "POST" && strings.HasSuffix(r.URL.Path, "/bindings"):
			var body api.CreateManagedPostgresBindingRequest
			_ = json.NewDecoder(r.Body).Decode(&body)
			bindingAccess = body.Access
			writeJSONTest(w, api.ManagedPostgresBinding{ID: "binding", State: "ready"})
		case r.Method == "PATCH" && r.URL.Path == "/v1/apps/shop-auth":
			var body api.UpdateAppRequest
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.RequireAuthn == nil || *body.RequireAuthn || body.PublicAuth == nil || body.PublicAuth.Mode != "open" {
				t.Error("end-user sign-in would be blocked by inherited edge authentication", err)
			}
			patched++
			writeJSONTest(w, map[string]any{})
		case r.Method == "PUT" && strings.HasPrefix(r.URL.Path, "/v1/apps/shop-auth/secrets/"):
			var body api.PutAppSecretRequest
			_ = json.NewDecoder(r.Body).Decode(&body)
			secrets[strings.TrimPrefix(r.URL.Path, "/v1/apps/shop-auth/secrets/")] = body.Value
			writeJSONTest(w, map[string]any{})
		case r.Method == "POST" && strings.HasSuffix(r.URL.Path, "/deployments"):
			if patched != 1 || secrets["AUTH_SITE_URL"] == "" {
				t.Errorf("deployment preceded configuration: patched=%d secrets=%d", patched, len(secrets))
			}
			base.Config.Handler.ServeHTTP(w, r)
		default:
			base.Config.Handler.ServeHTTP(w, r)
		}
	}))
	defer server.Close()
	t.Setenv("FAAS_API", server.URL)
	args := []string{"create", "shop-auth", "--database", database, "--site-url", "https://shop.example.com", "--redirect-urls", "https://shop.example.com/cb, http://localhost:3000/cb"}
	if resume {
		args = append(args, "--resume")
	}
	if code := cmdAuth(args); code != 0 {
		t.Fatal("create failed", code)
	}
	if bindingAccess != "auth" {
		t.Fatalf("binding access = %q, want auth", bindingAccess)
	}
	wantCreated := 1
	if resume {
		wantCreated = 0
	}
	if created != wantCreated || atomic.LoadInt32(&dockerfile) != 1 || atomic.LoadInt32(&source) != 1 {
		t.Fatalf("incomplete create: created=%d dockerfile=%d source=%d", created, dockerfile, source)
	}
	if secrets["AUTH_EXTERNAL_URL"] != "https://shop-auth.gregale.app" || secrets["AUTH_REDIRECT_URLS"] != "https://shop.example.com/cb,http://localhost:3000/cb" || secrets["AUTH_AUTOCONFIRM"] != "0" || secrets["AUTH_DISABLE_SIGNUP"] != "0" {
		t.Fatalf("unexpected settings: %v", secrets)
	}
	_, keys := secrets[authSigningKeysName]
	_, secret := secrets[authJWTSecretName]
	if resume && (keys || secret) {
		t.Fatal("resume replaced signing material that already signed sessions")
	}
	if !resume && (!keys || !secret) {
		t.Fatal("create did not seal signing material")
	}
}

func TestAuthCreateRejectsUnsafeURLs(t *testing.T) {
	resetJSONOut(t)
	for _, args := range [][]string{
		{"create", "shop-auth", "--database", "db", "--site-url", "http://shop.example.com"},
		{"create", "shop-auth", "--database", "db", "--site-url", "https://user:pw@shop.example.com"},
		{"create", "shop-auth", "--database", "db", "--site-url", "https://shop.example.com", "--redirect-urls", "javascript:alert(1)"},
		{"create", "shop-auth", "--site-url", "https://shop.example.com"},
	} {
		if code := cmdAuth(args); code == 0 {
			t.Fatalf("accepted %v", args)
		}
	}
	for value, want := range map[string]bool{"https://a.example/cb": true, "http://localhost:3000/cb": true, "http://127.0.0.1/cb": true, "http://[::1]:8080/": true, "http://a.example": false, "https://a.example/#x": false, "ftp://a.example": false, "https:///x": false} {
		if authRedirectURL(value) != want {
			t.Errorf("authRedirectURL(%q) = %v", value, !want)
		}
	}
}

func TestAuthAccountVerbsPointToLogin(t *testing.T) {
	resetJSONOut(t)
	for _, verb := range []string{"login", "logout", "whoami", "token"} {
		if code := cmdAuth([]string{verb}); code == 0 {
			t.Fatalf("%s treated as an application Auth command", verb)
		}
	}
}

func TestNewAuthSigningKeysAreConsistentES256(t *testing.T) {
	value, err := newAuthSigningKeys()
	if err != nil {
		t.Fatal(err)
	}
	var keys []map[string]any
	if err := json.Unmarshal([]byte(value), &keys); err != nil || len(keys) != 1 {
		t.Fatalf("keys = %s: %v", value, err)
	}
	key := keys[0]
	if key["kty"] != "EC" || key["crv"] != "P-256" || key["alg"] != "ES256" || !regexp.MustCompile(`^gregale-[0-9a-f]{16}$`).MatchString(fmt.Sprint(key["kid"])) {
		t.Fatalf("unexpected key header: %v", key)
	}
	if ops, _ := key["key_ops"].([]any); len(ops) != 2 || ops[0] != "sign" || ops[1] != "verify" {
		t.Fatalf("key_ops = %v", key["key_ops"])
	}
	decode := func(name string) []byte {
		b, err := base64.RawURLEncoding.DecodeString(fmt.Sprint(key[name]))
		if err != nil || len(b) != 32 {
			t.Fatalf("%s: %v (%d bytes)", name, err, len(b))
		}
		return b
	}
	private, err := ecdh.P256().NewPrivateKey(decode("d"))
	if err != nil {
		t.Fatal(err)
	}
	point := private.PublicKey().Bytes()
	if string(point[1:33]) != string(decode("x")) || string(point[33:65]) != string(decode("y")) {
		t.Fatal("public coordinates do not match the private scalar")
	}
	other, _ := newAuthSigningKeys()
	if other == value {
		t.Fatal("key generation repeated")
	}
	if secret, err := randomAuthSecret(); err != nil || len(secret) < 32 {
		t.Fatalf("JWT secret too short: %d %v", len(secret), err)
	}
}

func TestAuthTemplateRuntimeBounds(t *testing.T) {
	config, err := os.ReadFile("templates/auth/config.mjs")
	if err != nil {
		t.Fatal(err)
	}
	for name, bound := range map[string]int{"accessTokenSeconds": api.AuthAccessTokenTTLSeconds, "pool": api.AuthMaxPoolConnections, "bodyBytes": api.AuthMaxRequestBytes, "redirectURLs": api.AuthMaxRedirectURLs, "migrationStatementMs": api.AuthMigrationStatementTimeoutMS} {
		if !strings.Contains(string(config), fmt.Sprintf("%s: %d", name, bound)) {
			t.Fatalf("runtime %s differs from canonical limit", name)
		}
	}
}

// The image build verifies these pins; this keeps the repository side honest:
// a digest-pinned GoTrue stage, one hash per upstream migration, and an
// overlay only for a file the manifest pins.
func TestAuthTemplatePinsVerifiedMigrations(t *testing.T) {
	dockerfile, err := os.ReadFile("templates/auth/Dockerfile")
	if err != nil {
		t.Fatal(err)
	}
	if !regexp.MustCompile(`(?m)^FROM supabase/gotrue@sha256:[0-9a-f]{64} AS gotrue$`).Match(dockerfile) || !strings.Contains(string(dockerfile), "sha256sum --check --strict") {
		t.Fatal("GoTrue stage is not digest-pinned and migration-verified")
	}
	manifest, err := os.Open("templates/auth/migrations.sha256")
	if err != nil {
		t.Fatal(err)
	}
	defer manifest.Close()
	line := regexp.MustCompile(`^[0-9a-f]{64}  ([0-9a-z_]+(\.postgres)?\.up\.sql)$`)
	pinned := map[string]bool{}
	scanner := bufio.NewScanner(manifest)
	for scanner.Scan() {
		m := line.FindStringSubmatch(scanner.Text())
		if m == nil || pinned[m[1]] {
			t.Fatalf("invalid or duplicate manifest line %q", scanner.Text())
		}
		pinned[m[1]] = true
	}
	if len(pinned) != 75 {
		t.Fatalf("manifest pins %d migrations, want the 75 in v2.197.0", len(pinned))
	}
	overlays, err := os.ReadDir("templates/auth/overlay")
	if err != nil {
		t.Fatal(err)
	}
	for _, overlay := range overlays {
		if !pinned[overlay.Name()] {
			t.Fatalf("overlay %s replaces no pinned upstream migration", overlay.Name())
		}
		body, _ := os.ReadFile("templates/auth/overlay/" + overlay.Name())
		if !strings.Contains(string(body), "Gregale overlay (ADR-1190)") {
			t.Fatalf("overlay %s does not explain its change", overlay.Name())
		}
	}
}
