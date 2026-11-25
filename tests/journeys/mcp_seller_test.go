package journeys

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
)

// TestMCPSellerSelfServe is an MCP server owner selling to strangers
// (ADR-20261124094745, preview): people who sign in through the server's
// OAuth provider become consumers on a free plan with no card, a caller past
// the plan's limit is offered an upgrade link, a paid plan starts a Stripe
// Checkout, someone who already holds a consumer key links their sign-in to
// it, and the owner unlinks or revokes a sign-in.
//
// The issuer app is the OAuth provider: it serves the public half of a key
// the journey signs access tokens with. API monetization is a preview
// released per account, so the journey skips while `gregale feature list`
// reports api-monetization off. Sign-up settings need a Stripe connection;
// with none on the account the journey connects GREGALE_E2E_STRIPE_TEST_KEY
// (an sk_test_ key) for the run and skips without one.
//
// Settings reach the gateway within five minutes and it caches each
// sign-in's consumer for as long, so the enrollment, unlink and revoke
// steps poll for up to seven.

const (
	mcpSellerScope = "mcp:tools"
	// mcpSellerRPM is the free plan's request limit per minute.
	mcpSellerRPM = 3
	// mcpSellerPropagation covers the gateway's five-minute caches.
	mcpSellerPropagation = 7 * time.Minute
)

// mcpSellerJourney is the state TestMCPSellerSelfServe's steps share.
type mcpSellerJourney struct {
	tg                   *target
	issuerSlug, issuer   string
	slug, base, resource string
	signer               *ecdsa.PrivateKey
	kid                  string
	connectedStripe      bool
	testmode             bool
	alice, bob           string // sign-in subjects
	aliceFree            string // the consumer alice's first sign-in created
	keyholder, holderKey string // a consumer with a key, and the key
	otherKey             string // a second consumer's key
	upgradeURL           string
}

func TestMCPSellerSelfServe(t *testing.T) {
	tg := needs(t, tierFull, true)
	t.Parallel()
	j := &mcpSellerJourney{tg: tg, alice: "e2e-alice-" + randomHex(t, 4), bob: "e2e-bob-" + randomHex(t, 4)}
	j.prerequisites(t)
	j.issuerSlug = tg.app(t, "mcp-issuer")
	j.slug = tg.app(t, "mcp-seller")
	t.Cleanup(func() { j.cleanup(t) })
	s := newStory(t)
	s.step("deploy_issuer", j.deployIssuer)
	s.step("deploy_private_mcp_server", j.deployServer)
	s.step("offer_free_plan", j.offerPlans)
	s.check("unsigned_call_challenged", j.unsignedChallenged)
	s.step("sign_in_enrolls_consumer", j.signInEnrolls)
	s.check("same_sign_in_same_consumer", j.sameConsumer)
	s.step("plan_limit_offers_upgrade", j.planLimitUpgrade)
	s.check("upgrade_page_lists_plans", j.upgradePage)
	s.check("paid_plan_starts_checkout", j.paidCheckout)
	s.step("consumers_with_keys", j.keyholders)
	s.check("unknown_key_rejected", j.unknownKeyRejected)
	s.step("link_by_key", j.linkByKey)
	s.check("second_key_conflicts", j.linkConflict)
	s.step("identity_delete_reenrolls", j.deleteReenrolls)
	s.check("revoked_consumer_refused", j.revokedRefused)
}

// prerequisites skips the journey while API monetization is off for the
// account, and makes sure Stripe is connected for sign-up settings.
func (j *mcpSellerJourney) prerequisites(t *testing.T) {
	doc := object(t, j.tg.ok(t, time.Minute, "feature", "list").stdout)
	list, _ := field(doc, "capabilities").([]any)
	enabled := slices.ContainsFunc(list, func(c any) bool {
		return str(c, "key") == "api-monetization" && str(c, "enabled") == "true"
	})
	if !enabled {
		t.Skip("preview journey: api-monetization is not enabled for this account")
	}
	res := j.tg.run(t, time.Minute, "account", "billing-connection", "status", "stripe")
	if res.exit == 0 {
		j.testmode = str(object(t, res.stdout), "livemode") != "true"
		return
	}
	if j.tg.stripeTestKey == "" {
		t.Skip("the account has no Stripe connection; set GREGALE_E2E_STRIPE_TEST_KEY to an sk_test_ key")
	}
	j.tg.execStdin(t, j.tg.stripeTestKey+"\n", time.Minute, "account", "billing-connection", "connect", "stripe")
	if res := j.tg.run(t, time.Minute, "account", "billing-connection", "status", "stripe"); res.exit != 0 {
		t.Fatalf("connect Stripe: %s", j.tg.redact(res.problem()))
	}
	j.connectedStripe, j.testmode = true, true
}

// deployIssuer deploys the probe with the journey's public key, so it is
// the OAuth provider the MCP server trusts.
func (j *mcpSellerJourney) deployIssuer(t *testing.T) {
	signer, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	j.signer, j.kid = signer, "e2e-"+randomHex(t, 4)
	point, err := signer.PublicKey.ECDH()
	if err != nil {
		t.Fatal(err)
	}
	raw := point.Bytes() // 0x04 || X || Y
	jwks, err := json.Marshal(map[string]any{"keys": []map[string]string{{
		"kty": "EC", "crv": "P-256", "alg": "ES256", "use": "sig", "kid": j.kid,
		"x": base64.RawURLEncoding.EncodeToString(raw[1:33]),
		"y": base64.RawURLEncoding.EncodeToString(raw[33:]),
	}}})
	if err != nil {
		t.Fatal(err)
	}
	if res := j.tg.exec(t, j.tg.token, t.TempDir(), time.Minute, "deploy", "--create-only", "--app", "--name", j.issuerSlug); res.exit != 0 {
		t.Fatalf("deploy --create-only %s failed (exit %d): %s", j.issuerSlug, res.exit, j.tg.redact(res.problem()))
	}
	j.tg.ok(t, 2*time.Minute, "env", "set", "--app", j.issuerSlug, "E2E_REALTIME_JWKS_B64="+base64.StdEncoding.EncodeToString(jwks))
	receipt := j.tg.deployVersion(t, j.issuerSlug, "v1")
	j.issuer = strings.TrimRight(str(receipt, "app_url"), "/")
	if j.issuer == "" {
		j.issuer = j.tg.appURL(j.issuerSlug)
	}
	if r := get(j.issuer + "/.well-known/jwks.json"); r.status != http.StatusOK || !strings.Contains(string(r.body), j.kid) {
		t.Fatalf("the issuer's JWKS: %s, want the journey's key %s", r, j.kid)
	}
}

// deployServer scaffolds the Node MCP starter behind external OAuth from
// the issuer and deploys it with a token the issuer signed.
func (j *mcpSellerJourney) deployServer(t *testing.T) {
	j.base = j.tg.appURL(j.slug)
	j.resource = j.base + "/mcp"
	path := t.TempDir() + "/server"
	j.tg.ok(t, time.Minute, "mcp", "server", "init", "--path", path, "--runtime", "node",
		"--auth", "external-oauth", "--issuer", j.issuer, "--jwks-url", j.issuer+"/.well-known/jwks.json",
		"--resource", j.resource, "--scopes", mcpSellerScope)
	j.tg.deploySlots <- struct{}{}
	defer func() { <-j.tg.deploySlots }()
	res := j.tg.execInEnv(t, j.tg.token, t.TempDir(), "", []string{"MCP_ACCESS_TOKEN=" + j.token(t, "e2e-deployer")},
		deployLimit+5*time.Minute, "mcp", "server", "deploy", "--path", path, "--name", j.slug, "--token-env", "MCP_ACCESS_TOKEN")
	if res.exit != 0 {
		t.Fatalf("mcp server deploy %s failed (exit %d): %s", j.slug, res.exit, j.tg.redact(res.problem()))
	}
}

// offerPlans makes a card-less free plan and a paid plan, and lets sign-ins
// join the free one.
func (j *mcpSellerJourney) offerPlans(t *testing.T) {
	j.tg.ok(t, time.Minute, "api", "consumer", "plan", "create", j.slug, "--name", "free",
		"--max-requests-per-minute", strconv.Itoa(mcpSellerRPM))
	j.tg.ok(t, time.Minute, "api", "consumer", "rate-card", "create", j.slug, "--plan", "free", "--currency", "USD", "--price-millicents", "0")
	j.tg.ok(t, time.Minute, "api", "consumer", "plan", "create", j.slug, "--name", "pro")
	j.tg.ok(t, time.Minute, "api", "consumer", "rate-card", "create", j.slug, "--plan", "pro", "--currency", "USD", "--price-millicents", "100")
	j.tg.ok(t, time.Minute, "api", "consumer", "signup", "set", j.slug, "--display-name", "E2E tools",
		"--plans", "free,pro", "--free-plan", "free", "--oauth-consumers", "--customer-plan-changes")
}

func (j *mcpSellerJourney) unsignedChallenged(t *testing.T) {
	r := send(http.MethodPost, j.resource, mcpSellerHeaders(""), []byte(mcpSellerInitialize))
	if r.status != http.StatusUnauthorized || !strings.Contains(r.header.Get("WWW-Authenticate"), "resource_metadata") {
		t.Fatalf("an unsigned call: %s, want 401 naming the resource metadata", r)
	}
}

// signInEnrolls calls as alice until her sign-in shows as a consumer.
func (j *mcpSellerJourney) signInEnrolls(t *testing.T) {
	eventually(t, "alice enrolled", mcpSellerPropagation, 15*time.Second, func() (bool, string) {
		r := j.call(t, j.alice)
		if r.status != http.StatusOK && problemCodeOf(r) != "consumer_plan_limit_exceeded" {
			return false, r.String()
		}
		id := j.identity(t, j.alice)
		if id == nil {
			return false, "served, but alice is not listed: " + r.String()
		}
		j.aliceFree = str(id, "consumer_id")
		return true, ""
	})
	consumer := object(t, j.tg.ok(t, time.Minute, "api", "consumer", "info", j.slug, j.aliceFree).stdout)
	if !strings.HasPrefix(str(consumer, "external_ref"), "oauth:") {
		t.Fatalf("alice's consumer: %s, want an oauth: external_ref", compact(consumer))
	}
}

// sameConsumer signs alice in again with a fresh token: the same issuer and
// subject is the same consumer.
func (j *mcpSellerJourney) sameConsumer(t *testing.T) {
	time.Sleep(time.Minute) // a fresh minute for the plan's request limit
	if r := j.call(t, j.alice); r.status != http.StatusOK {
		t.Fatalf("alice's second sign-in: %s", r)
	}
	if id := j.identity(t, j.alice); str(id, "consumer_id") != j.aliceFree {
		t.Fatalf("alice's second sign-in bills %q, want her first consumer %q", str(id, "consumer_id"), j.aliceFree)
	}
}

// planLimitUpgrade calls past the free plan's per-minute limit: the 429
// carries a link to the hosted upgrade page.
func (j *mcpSellerJourney) planLimitUpgrade(t *testing.T) {
	var refused httpResult
	for range 4 * mcpSellerRPM {
		if r := j.call(t, j.alice); r.status == http.StatusTooManyRequests {
			refused = r
			break
		}
	}
	if refused.status != http.StatusTooManyRequests {
		t.Fatalf("%d calls in a minute were all served; the free plan allows %d", 4*mcpSellerRPM, mcpSellerRPM)
	}
	doc := jsonBody(t, refused)
	j.upgradeURL = str(doc, "upgrade_url")
	u, err := url.Parse(j.upgradeURL)
	if str(doc, "code") != "consumer_plan_limit_exceeded" || err != nil || u.Scheme != "https" || u.Query().Get("token") == "" ||
		!strings.Contains(u.Path, "/"+j.slug+"/") {
		t.Fatalf("the plan-limit refusal: %s, want consumer_plan_limit_exceeded with an https upgrade_url for %s", refused, j.slug)
	}
}

// upgradePage opens the link and reads the plans it offers.
func (j *mcpSellerJourney) upgradePage(t *testing.T) {
	if r := get(j.upgradeURL); r.status != http.StatusOK || !strings.HasPrefix(r.header.Get("Content-Type"), "text/html") {
		t.Fatalf("the upgrade page: %s", r)
	}
	r := get(j.tg.api + "/v1/consumer-signup/" + j.slug + "/upgrade/plans?token=" + url.QueryEscape(j.upgradeToken(t)))
	if r.status != http.StatusOK {
		t.Fatalf("the upgrade plans: %s", r)
	}
	doc := jsonBody(t, r)
	plans, _ := field(doc, "plans").([]any)
	names := []string{}
	for _, p := range plans {
		names = append(names, str(p, "name"))
	}
	slices.Sort(names)
	if !slices.Equal(names, []string{"free", "pro"}) || doc["has_card"] == true {
		t.Fatalf("the upgrade page offers %v (has_card %v), want free and pro and no card", names, doc["has_card"])
	}
}

// paidCheckout chooses the paid plan: with no card, the answer is a Stripe
// Checkout to save one. It creates a Stripe customer, so it runs only on a
// test-mode connection.
func (j *mcpSellerJourney) paidCheckout(t *testing.T) {
	if !j.testmode {
		t.Skip("the account's Stripe connection is live; the journey creates Stripe customers only in test mode")
	}
	body, _ := json.Marshal(map[string]string{"token": j.upgradeToken(t), "plan_id": j.planID(t, "pro")})
	r := send(http.MethodPost, j.tg.api+"/v1/consumer-signup/"+j.slug+"/upgrade", map[string]string{"Content-Type": "application/json"}, body)
	if r.status != http.StatusOK {
		t.Fatalf("choose the paid plan: %s", r)
	}
	doc := jsonBody(t, r)
	if !strings.HasPrefix(str(doc, "checkout_url"), "https://checkout.stripe.com/") || str(doc, "complete_token") == "" {
		t.Fatalf("choose the paid plan: %s, want a Stripe Checkout and a complete token", compact(doc))
	}
}

// keyholders makes two consumers the owner gave keys, as a key sign-up
// would.
func (j *mcpSellerJourney) keyholders(t *testing.T) {
	j.keyholder, j.holderKey = j.consumerWithKey(t, "holder")
	_, j.otherKey = j.consumerWithKey(t, "other")
}

func (j *mcpSellerJourney) unknownKeyRejected(t *testing.T) {
	r := j.link(t, j.bob, "ck_"+randomHex(t, 4)+"_"+randomHex(t, 16))
	if r.status != http.StatusForbidden || problemCodeOf(r) != "consumer_link_key_rejected" {
		t.Fatalf("linking a made-up key: %s, want 403 consumer_link_key_rejected", r)
	}
}

// linkByKey links alice's sign-in to the keyholder: her calls bill to it,
// and the free consumer her first sign-in created is closed.
func (j *mcpSellerJourney) linkByKey(t *testing.T) {
	r := j.link(t, j.alice, j.holderKey)
	if r.status != http.StatusOK || str(jsonBody(t, r), "consumer_id") != j.keyholder {
		t.Fatalf("link alice to the keyholder: %s, want consumer %s", r, j.keyholder)
	}
	if id := j.identity(t, j.alice); str(id, "consumer_id") != j.keyholder {
		t.Fatalf("alice's sign-in bills %q after the link, want %q", str(id, "consumer_id"), j.keyholder)
	}
	free := object(t, j.tg.ok(t, time.Minute, "api", "consumer", "info", j.slug, j.aliceFree).stdout)
	if str(free, "status") == "active" || str(free, "revoked_at") == "" {
		t.Fatalf("alice's free consumer after the link: %s, want it closed", compact(free))
	}
}

func (j *mcpSellerJourney) linkConflict(t *testing.T) {
	r := j.link(t, j.alice, j.otherKey)
	if r.status != http.StatusConflict || problemCodeOf(r) != "consumer_link_conflict" {
		t.Fatalf("link alice to a second key: %s, want 409 consumer_link_conflict", r)
	}
}

// deleteReenrolls frees alice's sign-in: once the gateway's cache expires,
// her next call starts a new free consumer.
func (j *mcpSellerJourney) deleteReenrolls(t *testing.T) {
	id := j.identity(t, j.alice)
	j.tg.ok(t, time.Minute, "api", "consumer", "identity", "delete", j.slug, str(id, "id"))
	eventually(t, "alice re-enrolled", mcpSellerPropagation, 20*time.Second, func() (bool, string) {
		r := j.call(t, j.alice)
		now := str(j.identity(t, j.alice), "consumer_id")
		if now == "" || now == j.keyholder || now == j.aliceFree {
			return false, "alice bills " + strconv.Quote(now) + "; last call " + r.String()
		}
		j.aliceFree = now
		return true, ""
	})
}

// revokedRefused closes alice's new consumer: her calls are refused.
func (j *mcpSellerJourney) revokedRefused(t *testing.T) {
	j.tg.ok(t, time.Minute, "api", "consumer", "revoke", j.slug, j.aliceFree)
	eventually(t, "alice refused", mcpSellerPropagation, 20*time.Second, func() (bool, string) {
		r := j.call(t, j.alice)
		return r.status == http.StatusForbidden, r.String()
	})
}

func (j *mcpSellerJourney) cleanup(t *testing.T) {
	if j.slug != "" {
		j.tg.run(t, time.Minute, "api", "consumer", "signup", "delete", j.slug)
	}
	if j.connectedStripe {
		if res := j.tg.run(t, time.Minute, "account", "billing-connection", "disconnect", "stripe"); res.exit != 0 {
			t.Logf("warning: could not disconnect the run's Stripe key: %s", res.problem())
		}
	}
}

// --- helpers -------------------------------------------------------------

const mcpSellerInitialize = `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"gregale-e2e","version":"1"}}}`

func mcpSellerHeaders(token string) map[string]string {
	h := map[string]string{"Content-Type": "application/json", "Accept": "application/json, text/event-stream"}
	if token != "" {
		h["Authorization"] = "Bearer " + token
	}
	return h
}

// call initializes an MCP session as subject: one request the gateway
// attributes and counts against the subject's plan.
func (j *mcpSellerJourney) call(t *testing.T, subject string) httpResult {
	return send(http.MethodPost, j.resource, mcpSellerHeaders(j.token(t, subject)), []byte(mcpSellerInitialize))
}

// link sends key to the server's consumer-link endpoint as subject.
func (j *mcpSellerJourney) link(t *testing.T, subject, key string) httpResult {
	body, _ := json.Marshal(map[string]string{"key": key})
	u := j.base + "/.well-known/gregale-consumer-link/mcp"
	return send(http.MethodPost, u, map[string]string{"Authorization": "Bearer " + j.token(t, subject), "Content-Type": "application/json"}, body)
}

// identity is subject's row in the app's sign-in list, or nil.
func (j *mcpSellerJourney) identity(t *testing.T, subject string) map[string]any {
	doc := object(t, j.tg.ok(t, time.Minute, "api", "consumer", "identity", "list", j.slug).stdout)
	rows, _ := field(doc, "identities").([]any)
	for _, row := range rows {
		if m, ok := row.(map[string]any); ok && str(m, "subject") == subject && str(m, "issuer") == j.issuer {
			return m
		}
	}
	return nil
}

func (j *mcpSellerJourney) upgradeToken(t *testing.T) string {
	u, err := url.Parse(j.upgradeURL)
	if err != nil || u.Query().Get("token") == "" {
		t.Fatalf("no upgrade link from an earlier step: %q", j.upgradeURL)
	}
	return u.Query().Get("token")
}

func (j *mcpSellerJourney) planID(t *testing.T, name string) string {
	doc := object(t, j.tg.ok(t, time.Minute, "api", "consumer", "plan", "list", j.slug).stdout)
	plans, _ := field(doc, "plans").([]any)
	for _, p := range plans {
		if str(p, "name") == name {
			return str(p, "id")
		}
	}
	t.Fatalf("plan %s is not listed: %s", name, compact(doc))
	return ""
}

// consumerWithKey registers a consumer and issues it a key.
func (j *mcpSellerJourney) consumerWithKey(t *testing.T, name string) (id, key string) {
	created := object(t, j.tg.ok(t, time.Minute, "api", "consumer", "create", j.slug,
		"--external-ref", "e2e-"+name+"-"+randomHex(t, 4), "--name", "e2e "+name).stdout)
	id = str(created, "id")
	res := j.tg.ok(t, time.Minute, "api", "consumer", "key", "create", j.slug, id, "--name", "e2e "+name)
	key = str(object(t, res.stdout), "key")
	if key == "" {
		key = consumerKeyRe.FindString(string(res.stdout))
	}
	if id == "" || key == "" {
		t.Fatalf("consumer %s: no id or key printed", name)
	}
	return id, key
}

// token is the ES256 access token the issuer would give subject: for the
// server's resource, with its scope, for ten minutes.
func (j *mcpSellerJourney) token(t *testing.T, subject string) string {
	t.Helper()
	now := time.Now()
	header, _ := json.Marshal(map[string]string{"alg": "ES256", "typ": "JWT", "kid": j.kid})
	claims, _ := json.Marshal(map[string]any{
		"iss": j.issuer, "aud": j.resource, "sub": subject, "scope": mcpSellerScope,
		"email": subject + "@example.com",
		"jti":   randomHex(t, 8),
		"iat":   now.Unix(),
		"nbf":   now.Add(-time.Minute).Unix(),
		"exp":   now.Add(10 * time.Minute).Unix(),
	})
	input := base64.RawURLEncoding.EncodeToString(header) + "." + base64.RawURLEncoding.EncodeToString(claims)
	digest := sha256.Sum256([]byte(input))
	r, s, err := ecdsa.Sign(rand.Reader, j.signer, digest[:])
	if err != nil {
		t.Fatal(err)
	}
	sig := make([]byte, 64)
	r.FillBytes(sig[:32])
	s.FillBytes(sig[32:])
	return input + "." + base64.RawURLEncoding.EncodeToString(sig)
}

// problemCodeOf is the RFC 7807 code of r's body, or "".
func problemCodeOf(r httpResult) string {
	var doc struct {
		Code string `json:"code"`
	}
	_ = json.Unmarshal(r.body, &doc)
	return doc.Code
}
