package journeys

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestHarnessMCPPrivateToolConfiguration(t *testing.T) {
	for _, tc := range []struct {
		name  string
		env   map[string]string
		valid bool
	}{
		{"private", map[string]string{"GREGALE_E2E_API": "https://api.staging.example", "GREGALE_E2E_TIER": "full", "GREGALE_E2E_TOKEN": "owner", "GREGALE_E2E_MCP_PRIVATE_TOOL_LIFECYCLE": "1"}, true},
		{"no target", map[string]string{"GREGALE_E2E_MCP_PRIVATE_TOOL_LIFECYCLE": "1"}, false},
		{"no owner", map[string]string{"GREGALE_E2E_API": "https://api.staging.example", "GREGALE_E2E_TIER": "full", "GREGALE_E2E_MCP_PRIVATE_TOOL_LIFECYCLE": "1"}, false},
		{"smoke", map[string]string{"GREGALE_E2E_API": "https://api.staging.example", "GREGALE_E2E_TOKEN": "owner", "GREGALE_E2E_MCP_PRIVATE_TOOL_LIFECYCLE": "1"}, false},
		{"conflicting open", map[string]string{"GREGALE_E2E_API": "https://api.staging.example", "GREGALE_E2E_TIER": "full", "GREGALE_E2E_TOKEN": "owner", "GREGALE_E2E_MCP_PRIVATE_TOOL_LIFECYCLE": "1", "GREGALE_E2E_MCP_TOOL_LIFECYCLE": "1"}, false},
		{"conflicting hosted", map[string]string{"GREGALE_E2E_API": "https://api.staging.example", "GREGALE_E2E_TIER": "full", "GREGALE_E2E_TOKEN": "owner", "GREGALE_E2E_MCP_PRIVATE_TOOL_LIFECYCLE": "1", "GREGALE_E2E_MCP_HOSTED_TOOL_LIFECYCLE": "1"}, false},
		{"invalid", map[string]string{"GREGALE_E2E_API": "https://api.staging.example", "GREGALE_E2E_MCP_PRIVATE_TOOL_LIFECYCLE": "yes"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tg, err := loadTarget(func(k string) string { return tc.env[k] })
			if (err == nil) != tc.valid {
				t.Fatalf("valid=%v: %v", tc.valid, err)
			}
			if err == nil && (!tg.mcpPrivateTools || tg.mcpTools || tg.mcpHostedTools) {
				t.Fatal("private opt-in enabled an open publishing journey")
			}
		})
	}
}

func TestHarnessMCPPrivateJWTAndRedaction(t *testing.T) {
	p, err := newPrivateToolCredentials("https://private.staging.example")
	if err != nil {
		t.Fatal(err)
	}
	token, err := p.sign("journey:read journey:invoke", time.Now().Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		t.Fatal("invalid synthetic JWT")
	}
	sig, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil || len(sig) != 64 {
		t.Fatal("invalid ES256 signature")
	}
	digest := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	if !ecdsa.Verify(&p.key.PublicKey, digest[:], new(big.Int).SetBytes(sig[:32]), new(big.Int).SetBytes(sig[32:])) {
		t.Fatal("fixture token does not verify")
	}
	var jwks map[string][]map[string]any
	if json.Unmarshal(p.publicJWKS(), &jwks) != nil || len(jwks["keys"]) != 1 || jwks["keys"][0]["d"] != nil || jwks["keys"][0]["alg"] != "ES256" {
		t.Fatal("source key set includes private material or changed algorithms")
	}
	tg := &target{token: "owner-secret"}
	tg.addSensitive(token)
	tg.addSensitive("rotated-provider-token")
	report := tg.redact("owner-secret " + token + " rotated-provider-token")
	if strings.Contains(report, token) || strings.Contains(report, "owner-secret") || strings.Contains(report, "rotated-provider-token") {
		t.Fatal("journey report leaked a credential")
	}
	for _, origin := range []string{"http://remote.example", "https://user:pass@host", "https://host/path", "https://host?token=value"} {
		if _, err := newPrivateToolCredentials(origin); err == nil {
			t.Fatal("unsafe synthetic issuer origin accepted")
		}
	}
}

func TestHarnessMCPPrivateDescriptorRequiresExactBinding(t *testing.T) {
	catalog := []byte(`{"version":2,"tools":[{"name":"journey_echo"}],"capabilities":["tools"]}`)
	binding := strings.Repeat("a", 64)
	doc := map[string]any{"version": "version", "app_id": "app", "deployment_id": "deployment", "access_mode": "external-oauth", "execution_authorized": false, "catalog_projection": "single_tool", "catalog_contract_sha256": toolJourneyDigest(catalog), "published_catalog_contract_sha256": strings.Repeat("b", 64), "grants": []any{map[string]any{"grant_id": "grant", "allow_invocation": true, "expires_at": time.Now().Add(time.Hour).Format(time.RFC3339), "credential_binding_sha256": binding}}}
	if err := checkToolJourneyDescriptorBinding(doc, catalog, "version", "app", "deployment", "grant", "journey_echo", "external-oauth", binding); err != nil {
		t.Fatal(err)
	}
	for _, wrong := range []string{"", strings.Repeat("0", 64)} {
		if checkToolJourneyDescriptorBinding(doc, catalog, "version", "app", "deployment", "grant", "journey_echo", "external-oauth", wrong) == nil {
			t.Fatal("wrong credential binding accepted")
		}
	}
}

// Optional real CLI preparation, without any deployed API request. When supplied
// dependencies are available, also exercise the generated source's real JWT
// middleware, scopes, credential reflection and protected catalog mutation.
func TestHarnessMCPPrivateToolSourceWithCLI(t *testing.T) {
	cli := os.Getenv("GREGALE_JOURNEY_TEST_CLI")
	if cli == "" {
		t.Skip("set GREGALE_JOURNEY_TEST_CLI to a matching local CLI")
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("private scaffold contacted a native API")
		w.WriteHeader(500)
	}))
	defer server.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	defer cancel()
	j := &mcpToolJourney{root: t, ctx: ctx, app: "private-fixture", tg: &target{api: server.URL, appsDomain: "staging.example", cli: cli, token: "fp_test_0123456789abcdef0123456789abcdef", home: t.TempDir(), runID: "hk3x9a"}}
	j.scaffoldPrivate(t)
	if err := filepath.WalkDir(j.path, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for _, token := range j.private.tokens {
			if bytes.Contains(raw, []byte(token)) {
				t.Fatal("source archive includes a private credential")
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(j.path, "gregale-mcp.json"))
	if err != nil {
		t.Fatal(err)
	}
	var config map[string]any
	if json.Unmarshal(data, &config) != nil || str(config, "auth.mode") != "external-oauth" || str(config, "auth.resource") != j.private.resource {
		t.Fatal("private scaffold lost its auth binding")
	}
	if _, err = archiveJourneyToolSource(j.path); err != nil {
		t.Fatal(err)
	}
	modules := os.Getenv("GREGALE_JOURNEY_NODE_MODULES")
	if modules == "" {
		return
	}
	if !filepath.IsAbs(modules) {
		t.Fatal("GREGALE_JOURNEY_NODE_MODULES must be absolute")
	}
	if err = os.Symlink(modules, filepath.Join(j.path, "node_modules")); err != nil {
		t.Fatal(err)
	}
	testSource, err := os.ReadFile(filepath.Join("testdata", "mcp-tool-lifecycle", "journey-private.test.mjs"))
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(j.path, "journey-private.test.mjs"), testSource, 0o600); err != nil {
		t.Fatal(err)
	}
	expired, err := j.private.sign("journey:read journey:invoke", time.Now().Add(-time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	j.tg.addSensitive(expired)
	readOnly := j.newPrivateToken(t, "journey:read")
	wrong := *j.private
	wrong.resource = "https://wrong.example/mcp"
	wrongAudience, err := wrong.sign("journey:read journey:invoke", time.Now().Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	j.tg.addSensitive(wrongAudience)
	cmd := exec.CommandContext(ctx, "node", "--test", "journey-private.test.mjs")
	cmd.Dir = j.path
	cmd.Env = append(os.Environ(), "GREGALE_JOURNEY_FIXTURE_TOKEN="+j.private.token, "GREGALE_JOURNEY_FIXTURE_CONTROL="+j.private.control, "GREGALE_JOURNEY_FIXTURE_EXPIRED="+expired, "GREGALE_JOURNEY_FIXTURE_READ_ONLY="+readOnly, "GREGALE_JOURNEY_FIXTURE_WRONG_AUDIENCE="+wrongAudience)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("private JWT fixture: %v\n%s", err, j.tg.redact(string(out)))
	}
	t.Log("Generated private fixture passes authenticated HTTP, scope, reflection and catalog-drift checks.")
}
