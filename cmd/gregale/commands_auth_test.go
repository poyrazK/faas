// adr: 1190 — application Auth reuses the app, binding and deploy lifecycles.
package main

import (
	"bytes"
	"crypto/ecdh"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"slices"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestAuthCreateDeploysRuntimeAfterConfiguration(t *testing.T) {
	t.Setenv("FAAS_APPLICATION_AUTH_ENABLED", "1")
	for _, tc := range []struct {
		name              string
		resume, workflows bool
	}{{name: "create"}, {name: "resume-keeps-keys", resume: true}, {name: "workflows", workflows: true}, {name: "resume-keeps-events-key", resume: true, workflows: true}} {
		t.Run(tc.name, func(t *testing.T) { testAuthCreateDeploysRuntime(t, tc.resume, tc.workflows) })
	}
}

func testAuthCreateDeploysRuntime(t *testing.T, resume, workflows bool) {
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
	created, patched, minted := 0, 0, 0
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
				list.Secrets = []api.AppSecretResponse{{Key: authSigningKeyPrefix + "0A1B2C3D"}, {Key: authSignerPrefix + "0A1B2C3D"}, {Key: authJWTSecretName}, {Key: authEventsKeyName}}
			}
			writeJSONTest(w, list)
		case r.Method == "POST" && r.URL.Path == "/v1/keys":
			var body api.CreateKeyRequest
			_ = json.NewDecoder(r.Body).Decode(&body)
			if len(body.Scopes) != 1 || body.Scopes[0] != api.ScopeEventsPublish {
				t.Errorf("workflow key scopes = %v, want only events:publish", body.Scopes)
			}
			minted++
			writeJSONTest(w, api.APIKeyResponse{ID: "key-1", Plaintext: "fp_live_events_only"})
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
	if workflows {
		args = append(args, "--workflows")
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
	if secrets["AUTH_APP_SLUG"] != "shop-auth" || secrets["AUTH_GREGALE_API_URL"] != strings.TrimRight(os.Getenv("FAAS_API"), "/") {
		t.Fatalf("app identity not sealed: %q %q", secrets["AUTH_APP_SLUG"], secrets["AUTH_GREGALE_API_URL"])
	}
	wantMinted := 0
	if workflows && !resume {
		wantMinted = 1
		if secrets[authEventsKeyName] != "fp_live_events_only" {
			t.Fatal("workflow key not sealed in the Auth app")
		}
	}
	if minted != wantMinted {
		t.Fatalf("minted %d workflow keys, want %d", minted, wantMinted)
	}
	// A new app gets one key and the marker that makes it sign.
	keys, signers := 0, 0
	for name := range secrets {
		if authSigningKeyName.MatchString(name) {
			keys++
			if _, ok := secrets[authSignerPrefix+strings.TrimPrefix(name, authSigningKeyPrefix)]; ok {
				signers++
			}
		}
	}
	_, secret := secrets[authJWTSecretName]
	if resume && (keys != 0 || secret) {
		t.Fatal("resume replaced signing material that already signed sessions")
	}
	if !resume && (keys != 1 || signers != 1 || !secret) {
		t.Fatalf("create sealed %d keys and %d signer markers", keys, signers)
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

func TestNewAuthSigningKeyIsConsistentES256(t *testing.T) {
	id, value, err := newAuthSigningKey()
	if err != nil {
		t.Fatal(err)
	}
	var key map[string]any
	if err := json.Unmarshal([]byte(value), &key); err != nil {
		t.Fatalf("key = %s: %v", value, err)
	}
	if !authAdminKeyID.MatchString(id) || key["kty"] != "EC" || key["crv"] != "P-256" || key["alg"] != "ES256" || key["kid"] != "gregale-"+strings.ToLower(id) {
		t.Fatalf("unexpected key header %s: %v", id, key)
	}
	// The Auth app assigns key_ops from the signer marker.
	if _, ok := key["key_ops"]; ok {
		t.Fatalf("key_ops stored with the key: %v", key["key_ops"])
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
	otherID, other, _ := newAuthSigningKey()
	if other == value || otherID == id {
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
	for name, bound := range map[string]int{"accessTokenSeconds": api.AuthAccessTokenTTLSeconds, "pool": api.AuthMaxPoolConnections, "frontPool": api.AuthFrontPoolConnections,
		"bodyBytes": api.AuthMaxRequestBytes, "authzBodyBytes": api.AuthAuthzBodyBytes, "authzConfigBytes": api.AuthAuthzConfigBytes, "redirectURLs": api.AuthMaxRedirectURLs,
		"migrationStatementMs": api.AuthMigrationStatementTimeoutMS, "invitationSeconds": api.AuthInvitationTTLSeconds,
		"emailTimeoutMs": api.AuthEmailTimeoutMs, "hookBodyBytes": api.AuthHookBodyBytes, "storageURLSeconds": api.AuthStorageURLSeconds,
		"adminBodyBytes": api.AuthAdminBodyBytes, "importBatch": api.AuthImportBatch, "adminPageSize": api.AuthAdminPageSize, "reauthenticationSeconds": api.AuthReauthenticationSeconds, "emailSubjectChars": api.AuthEmailSubjectMaxChars, "emailBodyBytes": api.AuthEmailBodyMaxBytes, "signingKeys": api.AuthMaxSigningKeys,
		"emailGrantSeconds": api.ProductControlEmailGrantSeconds,
		"teamMembers":       api.ProductControlTeamMaxMembers, "teamInvitations": api.ProductControlTeamMaxInvitations,
		"teamEmailBytes": api.ProductControlTeamEmailMaxBytes, "teamContextBytes": api.ProductControlTeamContextMaxBytes,
		"teamInvitationMinSeconds": api.ProductControlTeamInvitationMinSeconds, "teamInvitationMaxSeconds": api.ProductControlTeamInvitationMaxSeconds,
		"teamHistoryPageSize": api.ProductControlTeamHistoryPageSize, "teamCursorBytes": api.ProductControlOwnerCursorMaxBytes,
		"uiNameChars": api.AuthUINameMaxChars, "auditRetentionMaxDays": api.AuthAuditRetentionMaxDays, "auditPruneBatch": api.AuthAuditPruneBatch,
		"auditPruneBatches": api.AuthAuditPruneBatches, "auditPruneIntervalSeconds": api.AuthAuditPruneIntervalSeconds,
		"eventBatch": api.AuthEventBatch, "eventLeaseSeconds": api.AuthEventLeaseSeconds, "eventMaxBackoffSeconds": api.AuthEventMaxBackoffSeconds, "eventRetentionSeconds": api.AuthEventRetentionSeconds, "smsCodeSeconds": api.AuthSMSCodeSeconds,
		"ssoMetadataBytes": api.AuthSSOMetadataBytes, "ssoDomains": api.AuthSSOMaxDomains, "smsCountries": api.AuthSMSMaxCountries,
		"scimPageSize": api.AuthSCIMPageSize, "scimBodyBytes": api.AuthSCIMBodyBytes, "oauthClients": api.AuthOAuthMaxClients, "anonymousRetentionMaxDays": api.AuthAnonymousRetentionMaxDays} {
		if !strings.Contains(string(config), fmt.Sprintf("%s: %d", name, bound)) {
			t.Fatalf("runtime %s differs from canonical limit", name)
		}
	}
	authorization, err := os.ReadFile("templates/auth/sql/0001_authorization.sql")
	if err != nil {
		t.Fatal(err)
	}
	templates, err := os.ReadFile("templates/auth/sql/0014_email_templates.sql")
	if err != nil || !strings.Contains(string(templates), fmt.Sprintf("char_length(subject) BETWEEN 1 AND %d", api.AuthEmailSubjectMaxChars)) || !strings.Contains(string(templates), fmt.Sprintf("octet_length(body) BETWEEN 1 AND %d", api.AuthEmailBodyMaxBytes)) {
		t.Fatalf("email template SQL differs from the canonical limits: %v", err)
	}
	// 0009 replaces create_organization; both must keep the bound.
	anonymous, err := os.ReadFile("templates/auth/sql/0009_anonymous_users.sql")
	if err != nil || !strings.Contains(string(anonymous), fmt.Sprintf("WHERE created_by = caller) >= %d", api.AuthMaxOrganizationsPerUser)) {
		t.Fatalf("anonymous-user SQL differs from the canonical organization limit: %v", err)
	}
	for _, bound := range []string{
		fmt.Sprintf("WHERE created_by = caller) >= %d", api.AuthMaxOrganizationsPerUser),
		fmt.Sprintf("expires_at > now()) >= %d", api.AuthMaxPendingInvitationsPerOrg),
		fmt.Sprintf("ttl_seconds NOT BETWEEN 300 AND %d", api.AuthMaxInvitationTTLSeconds),
	} {
		if !strings.Contains(string(authorization), bound) {
			t.Fatalf("authorization SQL differs from canonical limit: %q", bound)
		}
	}
}

func TestReadAuthzFileAcceptsOnlyRolesAndPolicies(t *testing.T) {
	dir := t.TempDir()
	write := func(name, body string) string {
		path := dir + "/" + name
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		return path
	}
	got, err := readAuthzFile(write("ok.json", `{"roles": {"editor": ["doc:write"]}, "policies": [{"action": "write", "resource": "doc", "permission": "doc:write"}]}`))
	if err != nil || !strings.Contains(got, `"editor":["doc:write"]`) {
		t.Fatalf("valid document rejected: %q %v", got, err)
	}
	for name, body := range map[string]string{"array.json": `[]`, "null.json": `null`, "extra.json": `{"roles": {}, "admins": []}`, "broken.json": `{`} {
		if _, err := readAuthzFile(write(name, body)); err == nil {
			t.Fatalf("%s accepted", name)
		}
	}
	if _, err := readAuthzFile(write("large.json", `{"roles":{"r":["`+strings.Repeat("x", api.AuthAuthzConfigBytes)+`"]}}`)); err == nil {
		t.Fatal("oversized document accepted")
	}
}

// authIssuerServer serves the Auth app lookup that --auth and --jwt-auth
// resolve, and records what the command then writes.
func authIssuerServer(t *testing.T, handle func(w http.ResponseWriter, r *http.Request) bool) {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "GET" && r.URL.Path == "/v1/apps/shop-auth" {
			writeJSONTest(w, map[string]any{"id": "auth-app", "slug": "shop-auth", "type": "app", "url": "https://shop-auth.gregale.app"})
			return
		}
		if !handle(w, r) {
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(server.Close)
	t.Setenv("FAAS_API", server.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_x")
}

func TestEdgeRuleJWTAuthUsesTheAuthAppIssuer(t *testing.T) {
	t.Setenv("FAAS_APPLICATION_AUTH_ENABLED", "1")
	resetJSONEnv(t)
	var action map[string]any
	authIssuerServer(t, func(w http.ResponseWriter, r *http.Request) bool {
		if r.Method != "POST" || !strings.HasSuffix(r.URL.Path, "/edge-rules") {
			return false
		}
		var body struct {
			Action map[string]any `json:"action"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		action = body.Action
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(sampleEdgeRuleResponse(edgeRuleTestID))
		return true
	})
	if code := cmdEdgeRulesCreate([]string{"--app", "shop", "--kind", "jwt", "--match-host", "shop.example.com", "--jwt-auth", "shop-auth"}); code != 0 {
		t.Fatalf("create = %d", code)
	}
	encoded, _ := json.Marshal(action)
	for _, want := range []string{`"https://shop-auth.gregale.app/auth/v1"`, `"https://shop-auth.gregale.app/auth/v1/.well-known/jwks.json"`, `"authenticated"`, `"ES256"`} {
		if !strings.Contains(string(encoded), want) {
			t.Fatalf("action %s lacks %s", encoded, want)
		}
	}
	if code := cmdEdgeRulesCreate([]string{"--app", "shop", "--kind", "jwt", "--match-host", "shop.example.com", "--jwt-auth", "shop-auth", "--jwt-issuer", "https://other.example"}); code == 0 {
		t.Fatal("--jwt-auth combined with an explicit issuer")
	}
	if code := cmdEdgeRulesCreate([]string{"--app", "shop", "--kind", "cors", "--match-host", "shop.example.com", "--jwt-auth", "shop-auth"}); code == 0 {
		t.Fatal("--jwt-auth accepted for a non-JWT rule")
	}
}

func TestDataAPIAuthFlagReplacesManualIssuer(t *testing.T) {
	t.Setenv("FAAS_APPLICATION_AUTH_ENABLED", "1")
	resetJSONOut(t)
	if code := cmdDataAPI([]string{"create", "notes-data", "--database", "db", "--auth", "shop-auth", "--issuer", "https://other.example"}); code == 0 {
		t.Fatal("--auth combined with an explicit issuer")
	}
	if code := cmdDataAPI([]string{"create", "notes-data", "--database", "db"}); code == 0 {
		t.Fatal("create accepted without an issuer or --auth")
	}
	t.Setenv("HOME", t.TempDir())
	resolved := false
	authIssuerServer(t, func(w http.ResponseWriter, r *http.Request) bool {
		// Stop after the lookup: the remaining create flow is covered by
		// the Data API create tests.
		if r.Method == "GET" && r.URL.Path == "/v1/postgres/databases" {
			resolved = true
			w.WriteHeader(http.StatusServiceUnavailable)
			return true
		}
		return false
	})
	_ = cmdDataAPI([]string{"create", "notes-data", "--database", "db", "--auth", "shop-auth"})
	if !resolved {
		t.Fatal("create did not continue after resolving the Auth app")
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
	manifest, err := os.ReadFile("templates/auth/migrations.sha256")
	if err != nil {
		t.Fatal(err)
	}
	line := regexp.MustCompile(`^[0-9a-f]{64}  ([0-9a-z_]+(\.postgres)?\.up\.sql)$`)
	pinned := map[string]bool{}
	for _, text := range strings.Split(strings.TrimRight(string(manifest), "\n"), "\n") {
		m := line.FindStringSubmatch(text)
		if m == nil || pinned[m[1]] {
			t.Fatalf("invalid or duplicate manifest line %q", text)
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

func TestAuthEmailSettingsTakeTheKeyOnlyFromStdin(t *testing.T) {
	withStdin := func(value string) {
		previous := osStdin
		osStdin = strings.NewReader(value)
		t.Cleanup(func() { osStdin = previous })
	}
	withStdin("re_test_key_123\n")
	var opts authCreateOptions
	if err := authEmailSettings(&opts, "resend", "Shop <auth@shop.example.com>", true); err != nil {
		t.Fatal(err)
	}
	if opts.emailProvider != "resend" || opts.emailFrom != "Shop <auth@shop.example.com>" || opts.emailAPIKey != "re_test_key_123" {
		t.Fatalf("unexpected settings: %+v", opts)
	}
	for _, tc := range []struct{ provider, from, key string }{
		{"smtp", "auth@shop.example.com", "re_test_key_123"},
		{"resend", "not an address", "re_test_key_123"},
		{"resend", "auth@localhost", "re_test_key_123"},
		{"postmark", "Shop <auth@shop.example.com>\r\nBcc: x@y.z", "re_test_key_123"},
		{"postmark", "auth@shop.example.com", "short"},
		{"postmark", "auth@shop.example.com", "has space key"},
		{"postmark", "auth@shop.example.com", ""},
	} {
		withStdin(tc.key)
		if err := authEmailSettings(&authCreateOptions{}, tc.provider, tc.from, true); err == nil {
			t.Fatalf("accepted %+v", tc)
		}
	}
	if code := cmdAuth([]string{"create", "shop-auth", "--database", "db", "--site-url", "https://shop.example.com", "--email-from", "auth@shop.example.com"}); code == 0 {
		t.Fatal("--email-from accepted without --email-provider")
	}
}

// Node tests import modules from the template directory, so a module the
// Dockerfile forgets to copy passes every unit test and breaks the image.
func TestAuthTemplateImageCopiesEveryRuntimeFile(t *testing.T) {
	dockerfile, err := os.ReadFile("templates/auth/Dockerfile")
	if err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir("templates/auth")
	if err != nil {
		t.Fatal(err)
	}
	copyLine := regexp.MustCompile(`(?m)^COPY ([^\n]+) \./$`)
	copied := map[string]bool{}
	for _, m := range copyLine.FindAllStringSubmatch(string(dockerfile), -1) {
		for _, name := range strings.Fields(m[1]) {
			copied[name] = true
		}
	}
	for _, entry := range entries {
		if strings.HasSuffix(entry.Name(), ".mjs") && !copied[entry.Name()] {
			t.Errorf("Dockerfile does not copy %s into the image", entry.Name())
		}
	}
	for _, dir := range []string{"sql/", "ui/"} {
		if !strings.Contains(string(dockerfile), "COPY "+dir+" ./"+dir) {
			t.Errorf("Dockerfile does not copy %s", dir)
		}
	}
}

func TestRealtimeCreateWithAuthWiresCallbackAndOIDC(t *testing.T) {
	t.Setenv("FAAS_APPLICATION_AUTH_ENABLED", "1")
	resetJSONEnv(t)
	sealed := map[string]string{}
	var endpoint api.CreateManagedRealtimeEndpointRequest
	restarted, unset := 0, 0
	fail := false
	authIssuerServer(t, func(w http.ResponseWriter, r *http.Request) bool {
		switch {
		case r.Method == "PUT" && strings.HasPrefix(r.URL.Path, "/v1/apps/shop-auth/secrets/AUTH_REALTIME_CALLBACK_TOKEN_"):
			var body api.PutAppSecretRequest
			_ = json.NewDecoder(r.Body).Decode(&body)
			sealed[strings.TrimPrefix(r.URL.Path, "/v1/apps/shop-auth/secrets/")] = body.Value
			writeJSONTest(w, map[string]any{})
		case r.Method == "DELETE" && strings.HasPrefix(r.URL.Path, "/v1/apps/shop-auth/secrets/AUTH_REALTIME_CALLBACK_TOKEN_"):
			unset++
			w.WriteHeader(http.StatusNoContent)
		case r.Method == "POST" && r.URL.Path == "/v1/apps/shop/realtime/endpoints":
			if fail {
				w.WriteHeader(http.StatusUnprocessableEntity)
				writeJSONTest(w, map[string]any{"code": "invalid"})
				return true
			}
			_ = json.NewDecoder(r.Body).Decode(&endpoint)
			writeJSONTest(w, map[string]any{"id": "ep1", "app_slug": "shop", "enabled": true})
		case r.Method == "POST" && r.URL.Path == "/v1/apps/shop-auth/restart":
			restarted++
			writeJSONTest(w, map[string]any{"app_id": "auth-app", "wake_id": "w1"})
		default:
			return false
		}
		return true
	})
	if code := cmdRealtimeCreate([]string{"shop", "--auth", "shop-auth", "--allowed-origin", "https://shop.example.com"}); code != 0 {
		t.Fatalf("create = %d", code)
	}
	if len(sealed) != 1 {
		t.Fatalf("sealed %d callback tokens", len(sealed))
	}
	for name, token := range sealed {
		if !regexp.MustCompile(`^AUTH_REALTIME_CALLBACK_TOKEN_[0-9A-F]{8}$`).MatchString(name) || len(token) != 64 || endpoint.CallbackAuthToken != token {
			t.Fatalf("callback token %s not wired to the endpoint", name)
		}
	}
	if endpoint.CallbackURL != "https://shop-auth.gregale.app" || endpoint.AuthMode != "oidc_jwt" || endpoint.AuthIssuer != "https://shop-auth.gregale.app/auth/v1" ||
		endpoint.AuthJWKSURL != "https://shop-auth.gregale.app/auth/v1/.well-known/jwks.json" || strings.Join(endpoint.AuthAudience, ",") != "authenticated" || strings.Join(endpoint.AuthAlgorithms, ",") != "ES256" ||
		endpoint.ConnectPath != "" || endpoint.MessagePath != "" || endpoint.DisconnectPath != "" {
		t.Fatalf("unexpected endpoint request: %+v", endpoint)
	}
	if restarted != 1 {
		t.Fatal("the Auth app was not restarted to load the token")
	}
	fail = true
	if code := cmdRealtimeCreate([]string{"shop", "--auth", "shop-auth"}); code == 0 || unset != 1 {
		t.Fatalf("failed create left the sealed token behind: code=%d unset=%d", code, unset)
	}
	for _, extra := range [][]string{{"--callback-url", "https://x.example"}, {"--auth-mode", "none"}, {"--message-path", "/m"}} {
		if code := cmdRealtimeCreate(append([]string{"shop", "--auth", "shop-auth"}, extra...)); code == 0 {
			t.Fatalf("--auth accepted with %v", extra)
		}
	}
}

func TestAuthConfigAnonymousUsersAndLinking(t *testing.T) {
	resetJSONEnv(t)
	sealed, removed, _ := authSettingsWorld(t, nil, nil)
	captureAuthStdout(t)
	var stderr bytes.Buffer
	oldErr := osStderr
	osStderr = &stderr
	t.Cleanup(func() { osStderr = oldErr })
	if code := cmdAuth([]string{"config", "shop-auth", "--anonymous", "on", "--anonymous-retention", "30", "--identity-linking", "on", "--self-deletion", "on", "--org-claims", "on"}); code != 0 ||
		sealed["GOTRUE_EXTERNAL_ANONYMOUS_USERS_ENABLED"] != "true" || sealed["AUTH_ANONYMOUS_RETENTION_DAYS"] != "30" || sealed["AUTH_IDENTITY_LINKING"] != "1" || sealed["AUTH_SELF_DELETION"] != "1" || sealed["AUTH_ORG_CLAIMS"] != "1" {
		t.Fatalf("config = %d, sealed %v", code, sortedKeys(sealed))
	}
	if !strings.Contains(stderr.String(), "turn on CAPTCHA") {
		t.Fatalf("no CAPTCHA advice: %q", stderr.String())
	}
	if code := cmdAuth([]string{"config", "shop-auth", "--anonymous-retention", "forever"}); code != 0 {
		t.Fatalf("forever = %d", code)
	}
	if !slices.Contains(*removed, "AUTH_ANONYMOUS_RETENTION_DAYS") {
		t.Fatalf("forever kept the retention secret; removed %v", *removed)
	}
	if code := cmdAuth([]string{"config", "shop-auth", "--anonymous-retention", "0"}); code == 0 {
		t.Fatal("accepted zero days")
	}
}
