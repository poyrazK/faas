package journeys

// adr: 20261124094745 20261125230541 20261125194510 20261126082046 20261126175800
// — selling an MCP server end to end, the way a seller and their customer
// do: a private MCP server whose callers sign in with the seller's own
// OAuth issuer, a free plan with a per-tool cap, self-serve sign-up where a
// first sign-in becomes a customer, the account tools, the plan refusal,
// seller stats and the public listing page.
//
// The journey deploys its own issuer (a static app serving discovery and a
// JWKS for a key it generates), so it needs no identity provider or secret.
// It needs API monetization released to the account (platform flag
// api_monetization); without it every step skips.

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// mcpSellerToolCap is the free plan's monthly cap on the starter's greet
// tool: small, so the journey reaches the refusal in a few calls.
const mcpSellerToolCap = 2

// mcpSellerEnv shows the CLI surfaces that are still hidden by default.
var mcpSellerEnv = []string{"FAAS_API_MONETIZATION_ENABLED=1", "FAAS_MCP_GATEWAY_PREVIEW_ENABLED=1"}

func TestMCPSeller(t *testing.T) {
	tg := needs(t, tierFull, true)
	t.Parallel()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	j := &mcpSellerJourney{tg: tg, key: key, idp: tg.app(t, "mcp-idp"), app: tg.app(t, "mcp-sell")}
	s := newStory(t)
	s.step("monetization_released", j.monetizationReleased)
	s.step("issuer_deployed", j.issuerDeployed)
	s.step("server_deployed", j.serverDeployed)
	s.step("plan_created", j.planCreated)
	s.step("signup_opened", j.signupOpened)
	s.step("listing_published", j.listingPublished)
	s.check("listing_public", j.listingPublic)
	s.check("listing_republish_keeps_fields", j.listingRepublishKeepsFields)
	s.step("sign_in_becomes_customer", j.signInBecomesCustomer)
	s.check("account_tools_listed", j.accountToolsListed)
	s.step("tool_cap_refuses", j.toolCapRefuses)
	s.check("customer_usage_metered", j.customerUsageMetered)
	s.check("seller_stats", j.sellerStats)
	s.check("revoked_customer_refused", j.revokedCustomerRefused)
	s.check("listing_unpublished", j.listingUnpublished)
}

type mcpSellerJourney struct {
	tg       *target
	key      *rsa.PrivateKey
	idp, app string // the issuer app and the MCP server app
	issuer   string // https origin of the issuer app
	resource string // the MCP endpoint, also the token audience
	source   string // the MCP server's source directory
	customer string // the consumer the customer's first sign-in created
	session  *mcpSession
}

func (j *mcpSellerJourney) cli(t *testing.T, timeout time.Duration, args ...string) cliResult {
	t.Helper()
	return j.tg.runWithEnv(t, mcpSellerEnv, timeout, args...)
}

func (j *mcpSellerJourney) cliOK(t *testing.T, timeout time.Duration, args ...string) cliResult {
	t.Helper()
	res := j.cli(t, timeout, args...)
	if res.exit != 0 {
		t.Fatalf("gregale %s: exit %d: %s", strings.Join(args, " "), res.exit, res.problem())
	}
	return res
}

// token mints an access token for subject, as the seller's issuer would.
func (j *mcpSellerJourney) token(t *testing.T, subject string) string {
	t.Helper()
	enc := func(v any) string {
		b, _ := json.Marshal(v)
		return base64.RawURLEncoding.EncodeToString(b)
	}
	now := time.Now().Unix()
	input := enc(map[string]any{"alg": "RS256", "typ": "JWT", "kid": "journey-1"}) + "." + enc(map[string]any{
		"iss": j.issuer, "aud": j.resource, "sub": subject, "scope": "mcp:tools",
		"email": subject + "@e2e.gregale.invalid", "iat": now, "nbf": now - 5, "exp": now + 3600,
	})
	digest := sha256.Sum256([]byte(input))
	sig, err := rsa.SignPKCS1v15(rand.Reader, j.key, crypto.SHA256, digest[:])
	if err != nil {
		t.Fatal(err)
	}
	return input + "." + base64.RawURLEncoding.EncodeToString(sig)
}

// monetizationReleased: the account can sell; otherwise the journey skips.
// The app does not exist yet, so a released route answers app not found;
// anything else (no consumer keys on the plan, auth, transport) fails here
// rather than after two deploys.
func (j *mcpSellerJourney) monetizationReleased(t *testing.T) {
	res := j.cli(t, time.Minute, "api", "consumer", "plans", j.app)
	switch {
	case strings.Contains(res.problem(), "no such API route"):
		t.Skip("API monetization is not released to this account (platform flag api_monetization)")
	case res.exit != 0 && res.problemCode() != "not_found":
		t.Fatalf("api consumer plans for a missing app: exit %d: %s", res.exit, res.problem())
	}
}

// issuerDeployed: a public app serves the issuer's discovery document and
// the JWKS of the journey's key.
func (j *mcpSellerJourney) issuerDeployed(t *testing.T) {
	dir := t.TempDir()
	pub := j.key.PublicKey
	jwks, _ := json.Marshal(map[string]any{"keys": []map[string]any{{
		"kty": "RSA", "use": "sig", "alg": "RS256", "kid": "journey-1",
		"n": base64.RawURLEncoding.EncodeToString(pub.N.Bytes()),
		"e": base64.RawURLEncoding.EncodeToString(big.NewInt(int64(pub.E)).Bytes()),
	}}})
	writeFile(t, dir, "package.json", `{"name":"e2e-issuer","private":true,"type":"module","engines":{"node":">=22"},"scripts":{"start":"node server.mjs"}}`)
	writeFile(t, dir, "gregale.yaml", "access: public\nhosting:\n  start: npm start\n  port: 8080\n  health: /healthz\n")
	// The app's URL is known only after the deploy, so the discovery
	// document names the issuer from the request's host.
	writeFile(t, dir, "server.mjs", `import { createServer } from "node:http";
const jwks = `+"`"+string(jwks)+"`"+`;
createServer((req, res) => {
  const path = (req.url || "/").split("?")[0];
  if (path === "/healthz") { res.writeHead(200); res.end("ok"); return; }
  const issuer = "https://" + String(req.headers["x-forwarded-host"] || req.headers.host || "").split(",")[0].trim();
  const body = path === "/jwks.json" ? jwks : path === "/.well-known/openid-configuration"
    ? JSON.stringify({ issuer, jwks_uri: issuer + "/jwks.json", id_token_signing_alg_values_supported: ["RS256"] }) : "";
  res.writeHead(body ? 200 : 404, { "content-type": "application/json" });
  res.end(body || '{"error":"not_found"}');
}).listen(Number(process.env.PORT || 8080), "0.0.0.0");
`)
	receipt := j.tg.deploy(t, j.idp, "--path", dir)
	j.issuer = strings.TrimRight(str(receipt, "app_url"), "/")
	if j.issuer == "" {
		j.issuer = j.tg.appURL(j.idp)
	}
	// The MCP server's endpoint is on the same apps domain as the issuer.
	// (tg.appURL guesses the domain from the API host, which is wrong where
	// apps live on another domain, as on production.)
	domain, ok := strings.CutPrefix(j.issuer, "https://"+j.idp+".")
	if !ok || domain == "" || strings.ContainsAny(domain, "/:") {
		t.Fatalf("issuer app URL %q is not https://%s.<apps domain>", j.issuer, j.idp)
	}
	j.resource = "https://" + j.app + "." + domain + "/mcp"
	r := get(j.issuer + "/.well-known/openid-configuration")
	var discovery map[string]any
	if r.err != nil || r.status != http.StatusOK || json.Unmarshal(r.body, &discovery) != nil || str(discovery, "issuer") != j.issuer {
		t.Fatalf("issuer discovery = %s (want issuer %s)", r, j.issuer)
	}
	if r := get(j.issuer + "/jwks.json"); r.err != nil || r.status != http.StatusOK || !strings.Contains(string(r.body), "journey-1") {
		t.Fatalf("issuer JWKS = %s", r)
	}
}

// serverDeployed: the Node starter, private to the issuer's tokens, deploys
// and verifies with a token.
func (j *mcpSellerJourney) serverDeployed(t *testing.T) {
	j.source = filepath.Join(t.TempDir(), "mcp")
	j.cliOK(t, 2*time.Minute, "mcp", "server", "init", "--path", j.source, "--runtime", "node",
		"--auth", "external-oauth", "--issuer", j.issuer, "--jwks-url", j.issuer+"/jwks.json",
		"--resource", j.resource, "--scopes", "mcp:tools")
	j.tg.deploySlots <- struct{}{}
	defer func() { <-j.tg.deploySlots }()
	res := j.tg.execInEnv(t, j.tg.token, j.source, "", append(mcpSellerEnv, "MCP_TOKEN="+j.token(t, "seller-deploy")),
		deployLimit+5*time.Minute, "mcp", "server", "deploy", "--path", j.source, "--name", j.app,
		"--token-env", "MCP_TOKEN", "--timeout", "1200")
	if res.exit != 0 {
		t.Fatalf("mcp server deploy %s failed (exit %d): %s\n%s", j.app, res.exit, res.problem(), tail(string(res.stdout), 1500))
	}
	// Without the gateway checking its tokens, OAuth callers are served
	// but never become customers or metered.
	if !strings.Contains(string(res.stderr), "Gateway checks OAuth tokens") {
		t.Fatalf("mcp server deploy did not hand token checks to the gateway: %s", tail(string(res.stderr), 1500))
	}
}

// planCreated: a free plan whose greet tool is capped per month.
func (j *mcpSellerJourney) planCreated(t *testing.T) {
	j.cliOK(t, time.Minute, "api", "consumer", "plan-create", j.app, "--name", "free",
		"--max-requests-per-minute", "120", "--tool-limit", "greet="+strconv.Itoa(mcpSellerToolCap)+"/month")
	j.cliOK(t, time.Minute, "api", "consumer", "rate-card-create", j.app, "--plan", "free",
		"--currency", "USD", "--price-millicents", "0")
}

// signupOpened: sign-up offers only the free plan, so no billing provider
// is needed, and a first MCP sign-in becomes a customer.
func (j *mcpSellerJourney) signupOpened(t *testing.T) {
	j.cliOK(t, time.Minute, "api", "consumer", "signup-set", j.app, "--display-name", "E2E tools",
		"--plans", "free", "--free-plan", "free", "--oauth-consumers", "--account-tools")
	set := object(t, j.cliOK(t, time.Minute, "api", "consumer", "signup", j.app).stdout)
	if field(set, "enabled") == false {
		t.Fatalf("sign-up is not enabled: %s", compact(set))
	}
}

// listingPublished: the seller publishes a listing of the live tools.
func (j *mcpSellerJourney) listingPublished(t *testing.T) {
	res := j.tg.execInEnv(t, j.tg.token, j.tg.home, "", append(mcpSellerEnv, "MCP_TOKEN="+j.token(t, "seller-listing")),
		2*time.Minute, "mcp", "server", "listing", "publish", "--app", j.app, "--path", j.source,
		"--title", "E2E tools", "--summary", "Greets you from a journey.",
		"--registry-name", "io.github.gregale-e2e/"+j.app, "--show-usage", "--token-env", "MCP_TOKEN")
	if res.exit != 0 {
		t.Fatalf("listing publish: exit %d: %s", res.exit, res.problem())
	}
	l := object(t, res.stdout)
	if str(l, "endpoint") != j.resource || num(l, "version") != 1 || !hasNamed(field(l, "tools"), "greet") {
		t.Fatalf("published listing = %s", compact(l))
	}
}

// listingPublic: anyone can read the listing, its page and its server.json.
func (j *mcpSellerJourney) listingPublic(t *testing.T) {
	base := strings.TrimRight(j.tg.api, "/") + "/v1/consumer-signup/" + j.app
	r := get(base + "/listing")
	if r.err != nil || r.status != http.StatusOK {
		t.Fatalf("public listing = %s", r)
	}
	var l map[string]any
	if err := json.Unmarshal(r.body, &l); err != nil {
		t.Fatalf("public listing is not JSON: %v", err)
	}
	if str(l, "endpoint") != j.resource || field(l, "signup_open") != true || !hasNamed(field(l, "tools"), "greet") ||
		!hasNamed(field(l, "plans"), "free") || !strings.HasPrefix(str(l, "install.cursor_url"), "https://cursor.com/") ||
		field(l, "customers") == nil {
		t.Errorf("public listing = %s", compact(l))
	}
	if page := get(base + "/listing/page"); page.status != http.StatusOK ||
		!strings.HasPrefix(page.header.Get("Content-Type"), "text/html") || page.header.Get("Content-Security-Policy") == "" {
		t.Errorf("listing page = %s (CSP %q)", page, page.header.Get("Content-Security-Policy"))
	}
	doc := get(base + "/server.json")
	var sj map[string]any
	if doc.status != http.StatusOK || json.Unmarshal(doc.body, &sj) != nil ||
		str(sj, "name") != "io.github.gregale-e2e/"+j.app || str(sj, "remotes.0.url") != j.resource {
		t.Errorf("server.json = %s", doc)
	}
}

// listingRepublishKeepsFields: republishing after a tool change, with no
// flags, refreshes the tools and keeps the title, summary and registry name.
func (j *mcpSellerJourney) listingRepublishKeepsFields(t *testing.T) {
	res := j.tg.execInEnv(t, j.tg.token, j.tg.home, "", append(mcpSellerEnv, "MCP_TOKEN="+j.token(t, "seller-listing")),
		2*time.Minute, "mcp", "server", "listing", "publish", "--app", j.app, "--path", j.source, "--token-env", "MCP_TOKEN")
	if res.exit != 0 {
		t.Fatalf("listing republish: exit %d: %s", res.exit, res.problem())
	}
	l := object(t, res.stdout)
	if num(l, "version") != 2 || str(l, "title") != "E2E tools" || str(l, "summary") == "" ||
		str(l, "registry_name") == "" || field(l, "show_usage") != true {
		t.Errorf("republished listing lost fields: %s", compact(l))
	}
}

// signInBecomesCustomer: the customer's first call with a token of the
// seller's issuer creates a consumer on the free plan, linked to the
// sign-in.
func (j *mcpSellerJourney) signInBecomesCustomer(t *testing.T) {
	subject := "customer-" + j.tg.runID
	j.session = openMCP(t, j.resource, j.token(t, subject))
	if out := j.session.callTool(t, "greet", map[string]any{"name": "journey"}); !strings.Contains(out, "Hello, journey") {
		t.Fatalf("greet = %q", out)
	}
	deadline := time.Now().Add(time.Minute)
	for {
		ids := object(t, j.cliOK(t, time.Minute, "api", "consumer", "identities", j.app).stdout)
		for _, id := range asList(field(ids, "identities")) {
			if str(id, "subject") == subject && str(id, "issuer") == j.issuer {
				j.customer = str(id, "consumer_id")
			}
		}
		if j.customer != "" {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("no consumer identity for %s: %s", subject, compact(ids))
		}
		time.Sleep(5 * time.Second)
	}
}

// accountToolsListed: a signed-in customer sees the account tools.
func (j *mcpSellerJourney) accountToolsListed(t *testing.T) {
	names := j.session.toolNames(t)
	if !names["account_usage"] || !names["account_plans"] || !names["greet"] {
		t.Fatalf("tools/list = %v", names)
	}
}

// toolCapRefuses: once the customer used the free plan's greet calls, the
// next call is refused in MCP terms (HTTP 200, not billed) with the plan
// limit code; other tools still answer.
func (j *mcpSellerJourney) toolCapRefuses(t *testing.T) {
	// sign_in_becomes_customer made the first greet call; call n (counting
	// it) is allowed while n <= the cap and refused at n = cap+1, exactly.
	for n := 2; n <= mcpSellerToolCap+1; n++ {
		r := j.session.post(map[string]any{"jsonrpc": "2.0", "id": 100 + n, "method": "tools/call",
			"params": map[string]any{"name": "greet", "arguments": map[string]any{"name": "journey"}}})
		if r.err != nil || r.status != http.StatusOK {
			t.Fatalf("greet call %d = %s", n, r)
		}
		code := r.header.Get("X-Faas-Error-Code")
		if n <= mcpSellerToolCap {
			if code != "" || strings.Contains(string(r.body), `"isError":true`) {
				t.Fatalf("greet call %d of a %d/month cap was refused (%q): %s", n, mcpSellerToolCap, code, r)
			}
			continue
		}
		if code != "consumer_plan_limit_exceeded" {
			t.Fatalf("greet call %d, past the %d/month cap, answered %q: %s", n, mcpSellerToolCap, code, r)
		}
		t.Logf("refused at call %d (cap %d): %s", n, mcpSellerToolCap, tail(string(r.body), 400))
	}
	if out := j.session.callTool(t, "add", map[string]any{"a": 1, "b": 2}); !strings.Contains(out, "3") {
		t.Errorf("add after the greet cap = %q", out)
	}
}

// customerUsageMetered: the customer's own account_usage counts its calls,
// so they were attributed to it and metered (a gap the 2026-11-28 prod
// trial found). Usage arrives asynchronously.
func (j *mcpSellerJourney) customerUsageMetered(t *testing.T) {
	deadline := time.Now().Add(3 * time.Minute)
	for {
		usage := object(t, []byte(jsonTail(j.session.callTool(t, "account_usage", map[string]any{}))))
		if num(usage, "requests") >= mcpSellerToolCap {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("account_usage never counted the customer's %d greet calls: %s", mcpSellerToolCap, compact(usage))
		}
		time.Sleep(10 * time.Second)
	}
}

// jsonTail is the structured content callTool appends after a tool's text:
// the last top-level JSON object in out.
func jsonTail(out string) string {
	for i := len(out) - 1; i >= 0; i-- {
		if out[i] == '{' && json.Valid([]byte(out[i:])) {
			return out[i:]
		}
	}
	return "{}"
}

// sellerStats: the seller sees the sign-in and the greet usage. Usage
// reaches the stats asynchronously. Two months, summed, so a run that
// crosses a UTC month boundary still finds them.
func (j *mcpSellerJourney) sellerStats(t *testing.T) {
	deadline := time.Now().Add(3 * time.Minute)
	var stats map[string]any
	for {
		stats = object(t, j.cliOK(t, time.Minute, "mcp", "server", "stats", "--app", j.app, "--months", "2").stdout)
		var greet, oauth float64
		for _, tool := range asList(field(stats, "tools")) {
			if strings.HasSuffix(str(tool, "tool"), "greet") {
				greet += num(tool, "units")
			}
		}
		for _, month := range asList(field(stats, "months")) {
			oauth += num(month, "signed_up_by_source.oauth")
		}
		if oauth >= 1 && greet >= mcpSellerToolCap {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("seller stats never showed the OAuth sign-up and %d greet units: %s", mcpSellerToolCap, compact(stats))
		}
		time.Sleep(15 * time.Second)
	}
}

// revokedCustomerRefused: after the seller revokes the customer, the same
// sign-in no longer reaches the tools.
func (j *mcpSellerJourney) revokedCustomerRefused(t *testing.T) {
	j.cliOK(t, time.Minute, "api", "consumer", "revoke", j.app, j.customer, "--yes")
	deadline := time.Now().Add(time.Minute)
	for {
		r := j.session.post(map[string]any{"jsonrpc": "2.0", "id": 200, "method": "tools/call",
			"params": map[string]any{"name": "add", "arguments": map[string]any{"a": 1, "b": 2}}})
		// A refusal of the caller: 401/403, or a platform error code. A 404
		// (lost MCP session) or a 5xx is not evidence of revocation.
		if r.err == nil && (r.status == http.StatusUnauthorized || r.status == http.StatusForbidden ||
			(r.status != http.StatusNotFound && r.status < 500 && r.header.Get("X-Faas-Error-Code") != "")) {
			t.Logf("revoked customer refused: %s", r)
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("a revoked customer still calls tools a minute later: %s", r)
		}
		time.Sleep(5 * time.Second)
	}
}

// listingUnpublished: unpublishing takes the public listing down.
func (j *mcpSellerJourney) listingUnpublished(t *testing.T) {
	j.cliOK(t, time.Minute, "mcp", "server", "listing", "delete", "--app", j.app)
	if r := get(strings.TrimRight(j.tg.api, "/") + "/v1/consumer-signup/" + j.app + "/listing"); r.status != http.StatusNotFound {
		t.Fatalf("listing after unpublish = %s", r)
	}
}

// hasNamed reports whether a list of objects has one whose name is name.
func hasNamed(list any, name string) bool {
	for _, v := range asList(list) {
		if str(v, "name") == name {
			return true
		}
	}
	return false
}

func writeFile(t *testing.T, dir, name, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}
