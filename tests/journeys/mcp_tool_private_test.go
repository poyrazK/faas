package journeys

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

const privateToolTokenEnv = "MCP_JOURNEY_ACCESS_TOKEN"

type privateToolCredentials struct {
	key                              *ecdsa.PrivateKey
	issuer, resource, token, control string
	tokens                           []string
	bindings                         map[string]string
}

// TestMCPPrivateToolLifecycle uses deployed Gregale components and a synthetic
// ES256 issuer. Only public verification keys enter the source archive. It does
// not certify an external provider's login, refresh or revocation behavior.
func TestMCPPrivateToolLifecycle(t *testing.T) {
	tg := needs(t, tierFull, true)
	if !tg.mcpPrivateTools {
		t.Skip("preview journey: enable --mcp-private-tool-lifecycle against an explicit staging target")
	}
	t.Parallel()
	ctx, cancel := context.WithTimeout(t.Context(), time.Hour)
	t.Cleanup(cancel)
	j := &mcpToolJourney{tg: tg, root: t, ctx: ctx, grants: map[string]string{}}
	j.app = tg.app(t, "private-tools")
	s := newStory(t)
	s.step("preflight_private_invocation", func(t *testing.T) {
		j.preflight(t)
		j.native(t, http.MethodPost, "/v1/delegated-tool-invocations/"+uuid.NewString()+"/execute", map[string]any{}, j.key, http.StatusBadRequest)
	})
	s.step("scaffold_authenticated_tool", j.scaffoldPrivate)
	s.step("publish_verified_private_tool", j.publish)
	s.step("recover_private_publication", j.recoverPublication)
	s.step("provision_owner_credential", func(t *testing.T) { j.setPrivateSecret(t, false) })
	s.step("discover_exact_private_grants", j.grantAndDiscover)
	s.step("reject_changed_credential_binding", func(t *testing.T) {
		original := j.private.bindings["journey_echo"]
		j.private.bindings["journey_echo"] = strings.Repeat("0", 64)
		defer func() { j.private.bindings["journey_echo"] = original }()
		j.deniedPrivateCall(t, "binding")
	})
	for _, protocol := range []string{"2026-07-28", "2025-11-25"} {
		s.step("invoke_private_and_recover_"+protocol, func(t *testing.T) { j.invokeAndRecover(t, protocol) })
	}
	s.step("cancel_private_call", j.cancelInvocation)
	s.step("expire_private_grant", j.expirePrivateGrant)
	s.step("rotate_private_credential", j.rotatePrivateCredential)
	s.step("reject_private_catalog_drift", j.driftPrivateCatalog)
	s.step("revoke_private_grants_and_key", j.revokeAccess)
}

func newPrivateToolCredentials(origin string) (*privateToolCredentials, error) {
	u, err := url.Parse(origin)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" {
		return nil, fmt.Errorf("private fixture requires an exact HTTPS app origin")
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	return &privateToolCredentials{key: key, issuer: origin + "/journey/issuer", resource: origin + "/mcp", bindings: map[string]string{}}, nil
}

func (p *privateToolCredentials) sign(scopes string, expiry time.Time) (string, error) {
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"ES256","kid":"journey","typ":"JWT"}`))
	claims, err := json.Marshal(map[string]any{"iss": p.issuer, "aud": p.resource, "sub": "synthetic-tool-journey", "client_id": "journey", "scope": scopes, "exp": expiry.Unix(), "jti": uuid.NewString()})
	if err != nil {
		return "", err
	}
	payload := header + "." + base64.RawURLEncoding.EncodeToString(claims)
	digest := sha256.Sum256([]byte(payload))
	r, s, err := ecdsa.Sign(rand.Reader, p.key, digest[:])
	if err != nil {
		return "", err
	}
	signature := make([]byte, 64)
	r.FillBytes(signature[:32])
	s.FillBytes(signature[32:])
	return payload + "." + base64.RawURLEncoding.EncodeToString(signature), nil
}

func (p *privateToolCredentials) publicJWKS() []byte {
	return mustJourneyJSON(map[string]any{"keys": []any{map[string]any{"kty": "EC", "crv": "P-256", "kid": "journey", "alg": "ES256", "use": "sig", "x": base64.RawURLEncoding.EncodeToString(p.key.X.FillBytes(make([]byte, 32))), "y": base64.RawURLEncoding.EncodeToString(p.key.Y.FillBytes(make([]byte, 32)))}}})
}

func (j *mcpToolJourney) newPrivateToken(t *testing.T, scopes string) string {
	t.Helper()
	token, err := j.private.sign(scopes, time.Now().Add(90*time.Minute))
	if err != nil {
		t.Fatal("could not issue synthetic journey credential")
	}
	j.private.tokens = append(j.private.tokens, token)
	j.tg.addSensitive(token)
	return token
}

func (j *mcpToolJourney) scaffoldPrivate(t *testing.T) {
	p, err := newPrivateToolCredentials("https://" + j.app + "." + j.tg.appsDomain)
	if err != nil {
		t.Fatal(err)
	}
	j.private = p
	p.token = j.newPrivateToken(t, "journey:read journey:invoke")
	p.control = j.newPrivateToken(t, "journey:read journey:control")
	j.scaffold(t)
	if err = preparePrivateToolSource(j.path, p); err != nil {
		t.Fatal(err)
	}
}

func preparePrivateToolSource(dir string, p *privateToolCredentials) error {
	serverPath := filepath.Join(dir, "server.js")
	server, err := os.ReadFile(serverPath)
	if err != nil {
		return err
	}
	const declaration = "const runtime = createApp(config, { taskRuntime });"
	if bytes.Count(server, []byte(declaration)) != 1 {
		return fmt.Errorf("private fixture requires the reviewed journey entry point")
	}
	imports := "import { createLocalJWKSet } from 'jose';\nimport { installPrivateJourneyControls } from './journey-private.mjs';\n"
	replacement := "const journeyJWKS = JSON.parse(readFileSync(new URL('./journey-jwks.json', import.meta.url)));\nconst journeyKeys = createLocalJWKSet(journeyJWKS);\nconst runtime = createApp(config, { taskRuntime, keyResolver: journeyKeys });\ninstallPrivateJourneyControls(runtime, config, journeyKeys, journeyJWKS);"
	if err = os.WriteFile(serverPath, append([]byte(imports), bytes.Replace(server, []byte(declaration), []byte(replacement), 1)...), 0o644); err != nil {
		return err
	}
	if err = os.WriteFile(filepath.Join(dir, "journey-jwks.json"), p.publicJWKS(), 0o644); err != nil {
		return err
	}
	fixture, err := os.ReadFile(filepath.Join("testdata", "mcp-tool-lifecycle", "journey-private.mjs"))
	if err != nil {
		return err
	}
	if err = os.WriteFile(filepath.Join(dir, "journey-private.mjs"), fixture, 0o644); err != nil {
		return err
	}
	path := filepath.Join(dir, "gregale-mcp.json")
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var config map[string]any
	if err = json.Unmarshal(data, &config); err != nil {
		return err
	}
	auth, ok := config["auth"].(map[string]any)
	if !ok || auth["mode"] != "open" {
		return fmt.Errorf("private fixture requires the unmodified open journey policy")
	}
	auth["mode"], auth["issuer"], auth["jwks_url"], auth["resource"], auth["scopes"] = "external-oauth", p.issuer, strings.TrimSuffix(p.resource, "/mcp")+"/journey/jwks", p.resource, []string{"journey:read"}
	policy, ok := auth["tool_scopes"].(map[string]any)
	if !ok {
		return fmt.Errorf("journey policy omitted tool scopes")
	}
	policy["journey_echo"], policy["journey_wait"] = []string{"journey:invoke"}, []string{"journey:invoke"}
	return os.WriteFile(path, mustJourneyJSON(config), 0o644)
}

func (j *mcpToolJourney) assertPrivateIsolation(t *testing.T, raw []byte) {
	t.Helper()
	if j.private == nil {
		return
	}
	for _, token := range append(append([]string{}, j.private.tokens...), j.key, j.tg.token) {
		if token != "" && bytes.Contains(raw, []byte(token)) {
			t.Fatal("private journey detected a credential in an agent-visible response or receipt")
		}
	}
}

func validPrivateJourneyBinding(value string) bool {
	raw, err := hex.DecodeString(value)
	return err == nil && len(raw) == 32 && strings.ToLower(value) == value
}

func mustJourneyJSON(value any) []byte { raw, _ := json.Marshal(value); return raw }

func (j *mcpToolJourney) setPrivateSecret(t *testing.T, rotate bool) {
	verb := "set"
	if rotate {
		verb = "rotate"
	}
	r := j.tg.execStdin(t, privateToolTokenEnv+"="+j.private.token+"\n", time.Minute, "env", verb, "--app", j.app, "--scope", "default", "--from-stdin")
	if r.exit != 0 {
		t.Fatalf("owner secret %s failed: %s", verb, j.tg.redact(r.problem()))
	}
	j.assertPrivateIsolation(t, r.stdout)
}

func (j *mcpToolJourney) deniedPrivateCall(t *testing.T, label string) {
	session := j.connectHosted(t, "2026-07-28", nil)
	id, nonce := uuid.NewString(), j.nonce(t, label)
	args := j.invokeArgs("journey_echo", id, nonce)
	j.call(t, session, "gregale_invoke_tool", args, true)
	j.native(t, http.MethodPost, "/v1/delegated-tool-invocations/"+id+"/execute", privateJourneyNativeRequest(args), j.key, http.StatusNotFound)
	j.native(t, http.MethodGet, "/v1/tool-invocations/"+id, nil, j.tg.token, http.StatusNotFound)
	if r := get(j.preview + "/journey/calls/" + url.PathEscape(nonce)); r.err != nil || r.status != http.StatusNotFound {
		t.Fatal("refused private call reached the runtime or could not be checked")
	}
}

func privateJourneyNativeRequest(args map[string]any) map[string]any {
	return map[string]any{"grant_id": args["grant_id"], "arguments_json": args["arguments_json"], "intent": map[string]any{
		"version": args["version"], "name": args["name"], "arguments_sha256": toolJourneyDigest([]byte(str(args, "arguments_json"))),
		"access_mode": "external-oauth", "credential_binding_sha256": args["credential_binding"], "timeout_seconds": args["timeout_seconds"], "allow_tool_execution": true,
	}}
}

func (j *mcpToolJourney) expirePrivateGrant(t *testing.T) {
	original, originalBinding := j.grants["journey_echo"], j.private.bindings["journey_echo"]
	defer func() { j.grants["journey_echo"], j.private.bindings["journey_echo"] = original, originalBinding }()
	id := uuid.NewString()
	expiry := time.Now().UTC().Add(10 * time.Second)
	out := j.native(t, http.MethodPut, "/v1/tool-grants/"+id, map[string]any{"key_id": j.keyID, "version": j.version, "name": "journey_echo", "allow_invocation": true, "credential_secret_ref": "secret:" + privateToolTokenEnv, "expires_at": expiry.Format(time.RFC3339)}, j.tg.token, http.StatusOK)
	j.root.Cleanup(func() { j.tg.run(j.root, time.Minute, "mcp", "server", "tool-grant", "revoke", "--id", id) })
	j.grants["journey_echo"], j.private.bindings["journey_echo"] = id, str(out, "credential.sha256")
	if !validPrivateJourneyBinding(j.private.bindings["journey_echo"]) {
		t.Fatal("expiring grant omitted its binding")
	}
	timer := time.NewTimer(time.Until(expiry) + time.Second)
	defer timer.Stop()
	select {
	case <-j.ctx.Done():
		t.Fatal("private grant expiry wait cancelled")
	case <-timer.C:
	}
	j.deniedPrivateCall(t, "expired")
}

func (j *mcpToolJourney) rotatePrivateCredential(t *testing.T) {
	old := j.private.bindings["journey_echo"]
	j.private.token = j.newPrivateToken(t, "journey:read journey:invoke")
	j.setPrivateSecret(t, true)
	session := j.connectHosted(t, "2026-07-28", nil)
	j.call(t, session, "gregale_describe_tool", map[string]any{"version": j.version, "name": "journey_echo"}, true)
	for _, id := range j.invocationIDs {
		j.call(t, session, "gregale_get_tool_invocation", map[string]any{"invocation_id": id}, true)
		j.invocation(t, id, "journey_echo")
	}
	j.deniedPrivateCall(t, "rotated")
	j.grantAndDiscover(t)
	if j.private.bindings["journey_echo"] == old {
		t.Fatal("rotated credential reused the old binding")
	}
	j.invokeAndRecover(t, "2026-07-28")
}

func (j *mcpToolJourney) driftPrivateCatalog(t *testing.T) {
	req, err := http.NewRequestWithContext(j.ctx, http.MethodPost, j.preview+"/journey/catalog-drift", nil)
	if err != nil {
		t.Fatal("invalid private fixture control URL")
	}
	req.Header.Set("Authorization", "Bearer "+j.private.control)
	resp, err := (&http.Client{Timeout: 20 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}).Do(req)
	if err != nil {
		t.Fatal("private catalog control request failed")
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("private catalog control: HTTP %d", resp.StatusCode)
	}
	session := j.connectHosted(t, "2026-07-28", nil)
	id, nonce := uuid.NewString(), j.nonce(t, "drift")
	args := j.invokeArgs("journey_echo", id, nonce)
	j.call(t, session, "gregale_invoke_tool", args, false)
	j.waitInvocation(t, session, id, "not_started")
	record := j.native(t, http.MethodGet, "/v1/tool-invocations/"+id, nil, j.tg.token, http.StatusOK)
	if str(record, "dispatched_at") != "" {
		t.Fatal("catalog drift allowed native private dispatch")
	}
	j.call(t, session, "gregale_invoke_tool", args, false)
	if r := get(j.preview + "/journey/calls/" + url.PathEscape(nonce)); r.err != nil || r.status != http.StatusNotFound {
		t.Fatal("catalog drift call was executed or could not be checked")
	}
}
