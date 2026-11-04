package journeys

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// GA inventory row 112: managed realtime. Endpoints, OIDC sign-in, the
// original WebSocket protocol and backend publish, subscribe, send and
// close are released. The resumable protocol (v2) with retained history,
// presence, occupancy, tickets and usage is a preview released per
// account. Realtime itself is a preview capability too, so
// realtime_enabled and resumable_protocol_offered are known failures
// (known_failures.json) until the e2e account is released, and the steps
// after them report blocked.

const (
	// realtimeSubprotocol selects the resumable protocol (v2).
	realtimeSubprotocol = "gregale.realtime.v2"
	realtimeAudience    = "gregale-e2e"
	// realtimeRoom is the channel the v2 clients share.
	realtimeRoom = "room"
)

// realtimeJourney is the state TestRealtime's steps share.
type realtimeJourney struct {
	tg            *target
	key           string // API key for the calls the CLI has no command for
	slug, base    string
	endpoint      string
	callbackToken string
	signer        *ecdsa.PrivateKey
	kid           string
	v1            *realtimeClient
	v1Conn        string // v1's connection ID
	alice, bob    *realtimeClient
	bobMember     string // bob's presence member ID
	retained      int64  // sequence of the retained publish
	deleted       bool
}

// TestRealtime is a customer's realtime journey (row 112). The app is its
// own OIDC issuer: it serves the public half of a key the journey signs
// tokens with, and answers the endpoint's callbacks. On the original
// protocol a backend finds a signed-in user's connection, subscribes it to
// a channel, publishes to it, sends to it directly and closes it. On the
// resumable protocol two users share a room: a retained publish reaches
// the first and replays to the second when it subscribes from sequence 0,
// both see each other's presence, occupancy counts them, the app's
// authorize callback refuses a channel, a connection's ninth subscription
// is refused, a read-only ticket cannot set presence and closes when
// revoked, and deleting the endpoint closes everything.
func TestRealtime(t *testing.T) {
	tg := needs(t, tierFull, true)
	t.Parallel()
	j := &realtimeJourney{tg: tg, slug: tg.app(t, "realtime")}
	j.key = tg.scopedKey(t, "realtime")
	t.Cleanup(func() { j.cleanup(t) })
	s := newStory(t)
	s.step("realtime_enabled", j.enabled)
	s.step("deploy_issuer_app", j.deployIssuerApp)
	s.check("jwks_served", j.jwksServed)
	s.step("create_endpoint", j.createEndpoint)
	s.step("v1_connect", j.v1Connect)
	s.check("unsigned_handshake_refused", j.unsignedRefused)
	s.step("connection_listed", j.connectionListed)
	s.step("server_subscribe", func(t *testing.T) {
		tg.ok(t, time.Minute, "realtime", "subscribe", j.slug, j.endpoint, j.v1Conn, "lobby")
	})
	s.check("live_publish_delivered", j.livePublish)
	s.check("direct_send_delivered", j.directSend)
	s.check("close_connection", j.closeConnection)
	s.step("resumable_protocol_offered", j.resumableOffered)
	s.step("subscribe_with_presence", j.subscribeAlice)
	s.step("retained_publish_delivered", j.retainedPublish)
	s.step("retained_replayed_to_late_subscriber", j.lateSubscriber)
	s.check("presence_seen_both_ways", j.presence)
	s.check("occupancy_counts_both", j.occupancy)
	s.check("authorize_callback_refusal", j.refusal)
	s.check("subscriptions_per_connection_limit", j.subscriptionLimit)
	s.check("presence_left_on_close", j.presenceLeft)
	s.check("ticket_read_only_and_revoked", j.ticket)
	s.check("usage_reports_connections", j.usage)
	s.step("delete_endpoint", j.deleteEndpoint)
}

// enabled checks that the account may use realtime: `gregale feature list`
// reports whether the preview capability is released to it.
func (j *realtimeJourney) enabled(t *testing.T) {
	doc := object(t, j.tg.ok(t, time.Minute, "feature", "list").stdout)
	list, _ := field(doc, "capabilities").([]any)
	for _, c := range list {
		if str(c, "key") != "realtime" {
			continue
		}
		if str(c, "enabled") != "true" {
			t.Fatalf("realtime is not enabled for this account (%s): %s",
				str(c, "unavailable_reason"), str(c, "unavailable_detail"))
		}
		return
	}
	t.Fatal("gregale feature list does not list realtime")
}

// deployIssuerApp deploys the probe with the journey's public key and the
// endpoint's callback token, so it is both the OIDC issuer and the
// callback receiver.
func (j *realtimeJourney) deployIssuerApp(t *testing.T) {
	signer, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	j.signer, j.kid, j.callbackToken = signer, "e2e-"+randomHex(t, 4), randomHex(t, 24)
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
	if res := j.tg.exec(t, j.tg.token, t.TempDir(), time.Minute, "deploy", "--create-only", "--app", "--name", j.slug); res.exit != 0 {
		t.Fatalf("deploy --create-only %s failed (exit %d): %s", j.slug, res.exit, j.tg.redact(res.problem()))
	}
	j.tg.ok(t, 2*time.Minute, "env", "set", "--app", j.slug,
		"E2E_REALTIME_JWKS_B64="+base64.StdEncoding.EncodeToString(jwks),
		"E2E_REALTIME_CALLBACK_TOKEN="+j.callbackToken)
	receipt := j.tg.deployVersion(t, j.slug, "v1")
	j.base = strings.TrimRight(str(receipt, "app_url"), "/")
	if j.base == "" {
		j.base = j.tg.appURL(j.slug)
	}
}

func (j *realtimeJourney) jwksServed(t *testing.T) {
	r := get(j.base + "/.well-known/jwks.json")
	if r.status != http.StatusOK || !strings.Contains(string(r.body), j.kid) {
		t.Fatalf("the app's JWKS: %s, want the journey's key %s", r, j.kid)
	}
}

// createEndpoint creates the endpoint with the CLI, reading the callback
// token from stdin as the docs advise.
func (j *realtimeJourney) createEndpoint(t *testing.T) {
	res := j.tg.execStdin(t, j.callbackToken+"\n", 2*time.Minute, "realtime", "create", j.slug,
		"--callback-url", j.base, "--callback-auth-token-stdin",
		"--auth-mode", "oidc_jwt", "--auth-issuer", j.base,
		"--auth-jwks-url", j.base+"/.well-known/jwks.json",
		"--auth-audience", realtimeAudience, "--auth-algorithm", "ES256")
	if res.exit != 0 {
		t.Fatalf("realtime create failed (exit %d): %s", res.exit, j.tg.redact(res.problem()))
	}
	doc := object(t, res.stdout)
	j.endpoint = str(doc, "id")
	if j.endpoint == "" || str(doc, "auth_mode") != "oidc_jwt" {
		t.Fatalf("realtime create printed %s, want an oidc_jwt endpoint", compact(doc))
	}
	if strings.Contains(string(res.stdout), j.callbackToken) {
		t.Fatal("realtime create printed the callback token")
	}
}

// unsignedRefused runs once v1 is connected, so the endpoint has reached
// the realtime nodes, and checks that a handshake without a token, or with
// one signed by another key, is refused.
func (j *realtimeJourney) unsignedRefused(t *testing.T) {
	other, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	forged := j.signWith(t, other, "mallory")
	for name, header := range map[string]http.Header{
		"no token":     {},
		"forged token": {"Authorization": {"Bearer " + forged}},
	} {
		conn, code, status, err := wsDial(websocket.DefaultDialer, j.wsURL(), header)
		if err == nil {
			_ = conn.Close()
			t.Errorf("handshake with %s was accepted", name)
			continue
		}
		if code != http.StatusUnauthorized && code != http.StatusForbidden {
			t.Errorf("handshake with %s: %v (%s), want 401 or 403", name, err, status)
		}
	}
}

func (j *realtimeJourney) v1Connect(t *testing.T) {
	j.v1 = j.dial(t, "", "v1-user")
}

// connectionListed finds the v1 user's connection the way a backend does,
// by principal.
func (j *realtimeJourney) connectionListed(t *testing.T) {
	eventually(t, "connection listed", 30*time.Second, 2*time.Second, func() (bool, string) {
		res := j.tg.run(t, time.Minute, "realtime", "connections", j.slug, j.endpoint, "--principal", "v1-user")
		if res.exit != 0 {
			return false, res.problem()
		}
		doc := object(t, res.stdout)
		list, _ := field(doc, "connections").([]any)
		if len(list) == 1 && str(list[0], "principal") == "v1-user" {
			j.v1Conn = str(list[0], "id")
			return j.v1Conn != "", compact(doc)
		}
		return false, compact(doc)
	})
}

func (j *realtimeJourney) livePublish(t *testing.T) {
	data := "hello lobby " + randomHex(t, 4)
	j.tg.ok(t, time.Minute, "realtime", "publish", j.slug, j.endpoint, "lobby", "--data", data)
	j.v1.awaitRaw(t, "live publish", data)
}

func (j *realtimeJourney) directSend(t *testing.T) {
	data := "just for you " + randomHex(t, 4)
	j.tg.ok(t, time.Minute, "realtime", "send", j.slug, j.endpoint, j.v1Conn, "--data", data)
	j.v1.awaitRaw(t, "direct send", data)
}

func (j *realtimeJourney) closeConnection(t *testing.T) {
	j.tg.ok(t, time.Minute, "realtime", "close", j.slug, j.endpoint, j.v1Conn, "--reason", "journey done")
	j.v1.awaitClosed(t, "close by the backend", 10*time.Second)
}

// resumableOffered connects alice on the resumable protocol, which the
// realtime nodes offer only to accounts in the realtime preview.
func (j *realtimeJourney) resumableOffered(t *testing.T) {
	j.alice = j.dial(t, realtimeSubprotocol, "alice")
	if got := j.alice.conn.Subprotocol(); got != realtimeSubprotocol {
		t.Fatalf("handshake negotiated protocol %q, want %q: the resumable protocol is a preview not released to this account", got, realtimeSubprotocol)
	}
}

func (j *realtimeJourney) subscribeAlice(t *testing.T) {
	j.alice.subscribe(t, map[string]any{"channel": realtimeRoom, "after": 0, "state": map[string]any{"name": "alice"}, "occupancy": true})
	j.alice.await(t, "presence snapshot", 10*time.Second, frameOf("presence", realtimeRoom, "snapshot"))
}

// retainedPublish publishes through the API (retained delivery is an API
// option the released CLI hides) and expects the sequenced message.
func (j *realtimeJourney) retainedPublish(t *testing.T) {
	data := "retained " + randomHex(t, 4)
	path := j.endpointPath() + "/channels/" + realtimeRoom + "/publish?delivery=retained"
	body, _ := json.Marshal(map[string]any{"data_base64": base64.StdEncoding.EncodeToString([]byte(data))})
	r := send(http.MethodPost, j.tg.api+path, map[string]string{
		"Authorization": "Bearer " + j.key, "Content-Type": "application/json",
		"Idempotency-Key": "e2e-" + randomHex(t, 8), "User-Agent": "gregale-e2e-journeys",
	}, body)
	if r.status != http.StatusOK {
		t.Fatalf("retained publish: %s", r)
	}
	j.retained = int64(num(jsonBody(t, r), "sequence"))
	if j.retained < 1 {
		t.Fatalf("retained publish returned no sequence: %s", r)
	}
	message := j.alice.await(t, "retained message", 15*time.Second, frameOf("message", realtimeRoom, ""))
	if decoded(t, message) != data || int64(num(message, "sequence")) != j.retained {
		t.Fatalf("alice got %s, want %q at sequence %d", compact(message), data, j.retained)
	}
	j.alice.retained = data
}

// lateSubscriber connects bob after the publish; subscribing from sequence
// 0 replays the retained message, which is how a client recovers what it
// missed while disconnected.
func (j *realtimeJourney) lateSubscriber(t *testing.T) {
	j.bob = j.dial(t, realtimeSubprotocol, "bob")
	j.bob.subscribe(t, map[string]any{"channel": realtimeRoom, "after": 0, "state": map[string]any{"name": "bob"}})
	message := j.bob.await(t, "replayed message", 15*time.Second, frameOf("message", realtimeRoom, ""))
	if decoded(t, message) != j.alice.retained || int64(num(message, "sequence")) != j.retained {
		t.Fatalf("bob replayed %s, want %q at sequence %d", compact(message), j.alice.retained, j.retained)
	}
}

func (j *realtimeJourney) presence(t *testing.T) {
	joined := j.alice.await(t, "bob joined", 15*time.Second, presenceOf("joined", func(f realtimeFrame) bool {
		return str(f, "state.name") == "bob"
	}))
	j.bobMember = str(joined, "member_id")
	snapshot := j.bob.await(t, "bob's presence snapshot", 10*time.Second, frameOf("presence", realtimeRoom, "snapshot"))
	if !presenceHas(snapshot, "alice") {
		t.Errorf("bob's presence snapshot %s does not list alice", compact(snapshot))
	}
	j.bob.write(t, map[string]any{"type": "presence", "channel": realtimeRoom, "state": map[string]any{"name": "bob", "status": "typing"}})
	j.alice.await(t, "bob's update", 15*time.Second, presenceOf("updated", func(f realtimeFrame) bool {
		return str(f, "state.status") == "typing"
	}))
}

// occupancy reads the count through the API and in alice's live counts.
func (j *realtimeJourney) occupancy(t *testing.T) {
	eventually(t, "occupancy counts alice and bob", 20*time.Second, 2*time.Second, func() (bool, string) {
		r := j.tg.apiCall(http.MethodGet, j.endpointPath()+"/occupancy?channel="+realtimeRoom, nil, j.key)
		if r.status != http.StatusOK {
			return false, r.String()
		}
		list, _ := field(jsonBody(t, r), "channels").([]any)
		return len(list) == 1 && num(list[0], "subscribers") == 2 && num(list[0], "presence_members") == 2, r.String()
	})
	j.alice.await(t, "live count of 2", 15*time.Second, func(f realtimeFrame) bool {
		return f["type"] == "occupancy" && f["channel"] == realtimeRoom && num(f, "subscribers") == 2
	})
}

// refusal subscribes to a channel the app's authorize callback refuses.
func (j *realtimeJourney) refusal(t *testing.T) {
	channel := "denied-" + randomHex(t, 3)
	j.alice.write(t, map[string]any{"type": "subscribe", "channel": channel, "after": 0})
	frame := j.alice.await(t, "refusal", 15*time.Second, func(f realtimeFrame) bool {
		return f["channel"] == channel && (f["type"] == "error" || f["type"] == "subscribed")
	})
	if frame["type"] != "error" || frame["code"] != "not_authorized" {
		t.Fatalf("subscribing %s answered %s, want error not_authorized", channel, compact(frame))
	}
}

// subscriptionLimit fills bob's connection to its 8 subscriptions; the
// ninth is refused.
func (j *realtimeJourney) subscriptionLimit(t *testing.T) {
	for i := 1; i <= 7; i++ {
		j.bob.subscribe(t, map[string]any{"channel": fmt.Sprintf("extra-%d", i), "after": 0})
	}
	j.bob.write(t, map[string]any{"type": "subscribe", "channel": "extra-8", "after": 0})
	frame := j.bob.await(t, "ninth subscription", 15*time.Second, func(f realtimeFrame) bool {
		return f["channel"] == "extra-8" && (f["type"] == "error" || f["type"] == "subscribed")
	})
	if frame["type"] != "error" || frame["code"] != "subscription_limit" {
		t.Fatalf("the ninth subscription answered %s, want error subscription_limit", compact(frame))
	}
}

func (j *realtimeJourney) presenceLeft(t *testing.T) {
	if j.bobMember == "" {
		t.Fatal("presence_seen_both_ways did not see bob join")
	}
	_ = j.bob.conn.Close()
	j.alice.await(t, "bob left", 15*time.Second, presenceOf("left", func(f realtimeFrame) bool {
		return str(f, "member_id") == j.bobMember
	}))
}

// ticket gives a browser read-only access to the room without a sign-in:
// it may read but not set presence, and revoking it closes the socket.
func (j *realtimeJourney) ticket(t *testing.T) {
	r := j.tg.apiCall(http.MethodPost, j.endpointPath()+"/tickets",
		map[string]any{"principal": "ticket-user", "channels": []string{realtimeRoom}, "ttl_seconds": 120}, j.key)
	if r.status != http.StatusCreated {
		t.Fatalf("mint a ticket: %s", r)
	}
	doc := jsonBody(t, r)
	ticket, id := str(doc, "ticket"), str(doc, "ticket_id")
	if ticket == "" || id == "" {
		t.Fatalf("ticket response %s has no ticket or ticket_id", compact(doc))
	}
	client := j.dialToken(t, realtimeSubprotocol, ticket)
	client.subscribe(t, map[string]any{"channel": realtimeRoom, "after": j.retained})
	client.write(t, map[string]any{"type": "presence", "channel": realtimeRoom, "state": map[string]any{"name": "intruder"}})
	frame := client.await(t, "presence refusal", 10*time.Second, func(f realtimeFrame) bool { return f["type"] == "error" })
	if frame["code"] != "ticket_read_only" {
		t.Errorf("setting presence on a ticket answered %s, want error ticket_read_only", compact(frame))
	}
	if r := j.tg.apiCall(http.MethodDelete, j.endpointPath()+"/tickets/"+url.PathEscape(id), nil, j.key); r.status/100 != 2 {
		t.Fatalf("revoke the ticket: %s", r)
	}
	client.awaitClosed(t, "close after revocation", 15*time.Second)
}

func (j *realtimeJourney) usage(t *testing.T) {
	r := j.tg.apiCall(http.MethodGet, "/v1/usage/realtime", nil, j.key)
	if r.status != http.StatusOK {
		t.Fatalf("realtime usage: %s", r)
	}
	doc := jsonBody(t, r)
	if num(doc, "connection_limit") <= 0 || num(doc, "open_connections") < 1 {
		t.Fatalf("usage %s, want a connection limit and alice's open connection", compact(doc))
	}
}

func (j *realtimeJourney) deleteEndpoint(t *testing.T) {
	j.tg.ok(t, time.Minute, "realtime", "delete", j.slug, j.endpoint, "--yes")
	j.deleted = true
	j.alice.awaitClosed(t, "close after delete", 30*time.Second)
	eventually(t, "handshake refused after delete", 30*time.Second, 2*time.Second, func() (bool, string) {
		conn, code, status, err := wsDial(websocket.DefaultDialer, j.wsURL(), http.Header{"Authorization": {"Bearer " + j.sign(t, "alice")}})
		if err == nil {
			_ = conn.Close()
			return false, "handshake accepted"
		}
		return code >= 400, status
	})
}

func (j *realtimeJourney) cleanup(t *testing.T) {
	for _, c := range []*realtimeClient{j.v1, j.alice, j.bob} {
		if c != nil {
			_ = c.conn.Close()
		}
	}
	if j.endpoint != "" && !j.deleted {
		r := j.tg.apiCall(http.MethodDelete, j.endpointPath(), nil, j.key)
		if r.status/100 != 2 && r.status != http.StatusNotFound {
			t.Logf("warning: could not delete realtime endpoint %s: %s", j.endpoint, r)
		}
	}
}

func (j *realtimeJourney) endpointPath() string {
	return "/v1/apps/" + j.slug + "/realtime/endpoints/" + j.endpoint
}

func (j *realtimeJourney) wsURL() string {
	return "wss" + strings.TrimPrefix(j.base, "https") + "/__gregale/realtime/" + j.endpoint
}

// dial signs a token for principal and connects. The endpoint reaches the
// realtime nodes shortly after create, so early refusals are retried.
func (j *realtimeJourney) dial(t *testing.T, protocol, principal string) *realtimeClient {
	t.Helper()
	return j.dialToken(t, protocol, j.sign(t, principal))
}

func (j *realtimeJourney) dialToken(t *testing.T, protocol, token string) *realtimeClient {
	t.Helper()
	dialer := websocket.Dialer{HandshakeTimeout: 15 * time.Second}
	if protocol != "" {
		dialer.Subprotocols = []string{protocol}
	}
	var conn *websocket.Conn
	eventually(t, "handshake", 45*time.Second, 2*time.Second, func() (bool, string) {
		c, _, status, err := wsDial(&dialer, j.wsURL(), http.Header{"Authorization": {"Bearer " + token}})
		if err != nil {
			return false, fmt.Sprintf("%v (%s)", err, status)
		}
		conn = c
		return true, ""
	})
	client := &realtimeClient{conn: conn, wake: make(chan struct{}, 1)}
	go client.read()
	t.Cleanup(func() { _ = conn.Close() })
	return client
}

func (j *realtimeJourney) sign(t *testing.T, principal string) string {
	t.Helper()
	return j.signWith(t, j.signer, principal)
}

// signWith makes the ES256 token a customer's identity provider would
// issue: issuer, audience, subject and a ten-minute expiry.
func (j *realtimeJourney) signWith(t *testing.T, key *ecdsa.PrivateKey, principal string) string {
	t.Helper()
	now := time.Now()
	header, _ := json.Marshal(map[string]string{"alg": "ES256", "typ": "JWT", "kid": j.kid})
	claims, _ := json.Marshal(map[string]any{
		"iss": j.base, "aud": realtimeAudience, "sub": principal,
		"iat": now.Unix(), "nbf": now.Add(-time.Minute).Unix(), "exp": now.Add(10 * time.Minute).Unix(),
	})
	input := base64.RawURLEncoding.EncodeToString(header) + "." + base64.RawURLEncoding.EncodeToString(claims)
	digest := sha256.Sum256([]byte(input))
	r, s, err := ecdsa.Sign(rand.Reader, key, digest[:])
	if err != nil {
		t.Fatal(err)
	}
	sig := make([]byte, 64)
	r.FillBytes(sig[:32])
	s.FillBytes(sig[32:])
	return input + "." + base64.RawURLEncoding.EncodeToString(sig)
}

// --- WebSocket client ----------------------------------------------------

type realtimeFrame = map[string]any

// realtimeClient reads every frame into a backlog, so a step can wait for
// the frame it needs without dropping ones a later step needs.
type realtimeClient struct {
	conn     *websocket.Conn
	writeMu  sync.Mutex
	mu       sync.Mutex
	frames   []realtimeFrame
	raw      []string // v1 text frames
	closed   error
	wake     chan struct{}
	retained string // the retained message's data, for later steps
}

func (c *realtimeClient) read() {
	for {
		_, data, err := c.conn.ReadMessage()
		c.mu.Lock()
		if err != nil {
			c.closed = err
		} else {
			var frame realtimeFrame
			if json.Unmarshal(data, &frame) == nil && frame["type"] != nil {
				c.frames = append(c.frames, frame)
			} else {
				c.raw = append(c.raw, string(data))
			}
		}
		c.mu.Unlock()
		select {
		case c.wake <- struct{}{}:
		default:
		}
		if err != nil {
			return
		}
	}
}

func (c *realtimeClient) write(t *testing.T, frame map[string]any) {
	t.Helper()
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	if err := c.conn.WriteJSON(frame); err != nil {
		t.Fatalf("send %s: %v", compact(frame), err)
	}
}

// subscribe sends a subscribe frame and waits for its answer.
func (c *realtimeClient) subscribe(t *testing.T, frame map[string]any) {
	t.Helper()
	frame["type"] = "subscribe"
	c.write(t, frame)
	channel := frame["channel"]
	got := c.await(t, fmt.Sprintf("subscribe %v", channel), 15*time.Second, func(f realtimeFrame) bool {
		return f["channel"] == channel && (f["type"] == "subscribed" || f["type"] == "error")
	})
	if got["type"] != "subscribed" {
		t.Fatalf("subscribe %v answered %s", channel, compact(got))
	}
}

// await consumes and returns the first frame match accepts.
func (c *realtimeClient) await(t *testing.T, what string, timeout time.Duration, match func(realtimeFrame) bool) realtimeFrame {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		c.mu.Lock()
		for i, f := range c.frames {
			if match(f) {
				c.frames = append(c.frames[:i], c.frames[i+1:]...)
				c.mu.Unlock()
				return f
			}
		}
		closed, backlog := c.closed, compact(c.frames)
		c.mu.Unlock()
		if closed != nil {
			t.Fatalf("%s: the connection closed (%v); unread frames: %s", what, closed, tail(backlog, 1000))
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s: nothing within %s; unread frames: %s", what, timeout, tail(backlog, 1000))
		}
		select {
		case <-c.wake:
		case <-time.After(time.Until(deadline)):
		}
	}
}

// awaitRaw waits for a v1 text frame carrying data.
func (c *realtimeClient) awaitRaw(t *testing.T, what, data string) {
	t.Helper()
	eventually(t, what, 15*time.Second, 200*time.Millisecond, func() (bool, string) {
		c.mu.Lock()
		defer c.mu.Unlock()
		for i, got := range c.raw {
			if got == data {
				c.raw = append(c.raw[:i], c.raw[i+1:]...)
				return true, ""
			}
		}
		return false, fmt.Sprintf("frames %q, closed %v", c.raw, c.closed)
	})
}

func (c *realtimeClient) awaitClosed(t *testing.T, what string, timeout time.Duration) {
	t.Helper()
	eventually(t, what, timeout, 200*time.Millisecond, func() (bool, string) {
		c.mu.Lock()
		defer c.mu.Unlock()
		return c.closed != nil, "still open"
	})
}

// presenceOf matches a presence event in the room that also passes match.
func presenceOf(event string, match func(realtimeFrame) bool) func(realtimeFrame) bool {
	return func(f realtimeFrame) bool { return frameOf("presence", realtimeRoom, event)(f) && match(f) }
}

// frameOf matches a frame by type, channel and (when set) event.
func frameOf(kind, channel, event string) func(realtimeFrame) bool {
	return func(f realtimeFrame) bool {
		return f["type"] == kind && f["channel"] == channel && (event == "" || f["event"] == event)
	}
}

func presenceHas(snapshot realtimeFrame, name string) bool {
	members, _ := snapshot["members"].([]any)
	for _, m := range members {
		if str(m, "state.name") == name {
			return true
		}
	}
	return false
}

func decoded(t *testing.T, message realtimeFrame) string {
	t.Helper()
	b, err := base64.StdEncoding.DecodeString(str(message, "data_base64"))
	if err != nil {
		t.Fatalf("message %s: data_base64: %v", compact(message), err)
	}
	return string(b)
}

// wsDial dials and closes the handshake response, returning its status
// code (0 without a response) and status line.
func wsDial(dialer *websocket.Dialer, rawURL string, header http.Header) (*websocket.Conn, int, string, error) {
	conn, resp, err := dialer.Dial(rawURL, header)
	if resp == nil {
		return conn, 0, "no response", err
	}
	if resp.Body != nil {
		_ = resp.Body.Close()
	}
	return conn, resp.StatusCode, resp.Status, err
}
