package journeys

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"
)

// GA inventory rows 99 (queues) and 115 (outbound integrations). Both are
// preview capabilities released per account, so each journey first checks
// that the account may use them; until it may, that step is a known failure
// (known_failures.json) and the rest report blocked.

// featureEnabled fails t unless `gregale feature list` reports every key
// enabled for the account.
func featureEnabled(t *testing.T, tg *target, keys ...string) {
	t.Helper()
	doc := object(t, tg.ok(t, time.Minute, "feature", "list").stdout)
	list, _ := field(doc, "capabilities").([]any)
	for _, want := range keys {
		found := false
		for _, c := range list {
			if str(c, "key") != want {
				continue
			}
			found = true
			if str(c, "enabled") != "true" {
				t.Fatalf("%s is not enabled for this account (%s): %s", want, str(c, "unavailable_reason"), str(c, "unavailable_detail"))
			}
		}
		if !found {
			t.Fatalf("gregale feature list does not list %s", want)
		}
	}
}

// --- outbound integrations (row 115) -----------------------------------

// outboundJourney is the state TestOutboundIntegrations' steps share.
type outboundJourney struct {
	tg              *target
	key             string
	provider        string // the shared probe's base URL, the integration origin
	caller          string // the bound app's slug
	callerBase      string
	integration     string
	credentialSHA   string // SHA-256 of the Authorization value; never the value
	credentialValue string
	deleted         bool
}

// TestOutboundIntegrations is a customer's outbound integration journey
// (row 115): register the run's shared probe as the provider, seal its
// Authorization value, bind a second probe app, and call the provider
// through GREGALE_OUTBOUND_URL. The provider reports only the SHA-256 of
// the Authorization it received, so the journey proves the relay injected
// the sealed credential without either app seeing it. It then checks the
// route policy, usage counting, binding narrowing and unbinding. AI
// integrations, key pools, Runs grants and probe verification are not run.
func TestOutboundIntegrations(t *testing.T) {
	tg := needs(t, tierFull, true)
	t.Parallel()
	j := &outboundJourney{tg: tg}
	j.key = tg.scopedKey(t, "outbound")
	// Apps are made here, not in a step: a step's cleanups run when it ends.
	j.caller = tg.app(t, "outbound")
	t.Cleanup(j.cleanup)
	s := newStory(t)
	s.step("outbound_enabled", func(t *testing.T) { featureEnabled(t, tg, "outbound-integrations") })
	s.step("deploy_caller", j.deployCaller)
	s.step("create_integration", j.create)
	s.step("set_credential", j.setCredential)
	s.check("credential_never_returned", j.credentialHidden)
	s.step("bind_app", j.bind)
	s.check("call_through_relay", j.callThroughRelay)
	s.check("route_policy_refused", j.routeRefused)
	s.check("usage_counted", j.usageCounted)
	s.check("binding_cannot_widen", j.bindingCannotWiden)
	s.step("unbind_app", j.unbind)
	s.check("unbound_app_refused", j.unboundRefused)
	s.step("delete_integration", j.delete)
}

func (j *outboundJourney) cleanup() {
	if j.integration == "" || j.deleted {
		return
	}
	j.tg.apiCall(http.MethodDelete, "/v1/outbound/integrations/"+j.integration, nil, j.key)
}

func (j *outboundJourney) deployCaller(t *testing.T) {
	_, j.provider = j.tg.sharedProbe(t)
	receipt := j.tg.deployVersion(t, j.caller, "v1")
	j.callerBase = strings.TrimRight(str(receipt, "app_url"), "/")
	if j.callerBase == "" {
		j.callerBase = j.tg.appURL(j.caller)
	}
}

func (j *outboundJourney) create(t *testing.T) {
	r := j.tg.apiCall(http.MethodPost, "/v1/outbound/integrations", map[string]any{
		"name":                  j.tg.slug("outbound"),
		"origin":                j.provider,
		"allowed_methods":       []string{"GET"},
		"allowed_path_prefixes": []string{"/outbound-echo"},
	}, j.key)
	if r.status != http.StatusCreated {
		t.Fatalf("POST /v1/outbound/integrations: %s", r)
	}
	doc := jsonBody(t, r)
	j.integration = str(doc, "id")
	if j.integration == "" || str(doc, "credential_configured") != "false" || str(doc, "owner_kind") != "customer" {
		t.Fatalf("a new integration should have an id, no credential and a customer owner: %s", compact(doc))
	}
}

func (j *outboundJourney) setCredential(t *testing.T) {
	j.credentialValue = "Bearer e2e-" + randomHex(t, 32)
	sum := sha256.Sum256([]byte(j.credentialValue))
	j.credentialSHA = hex.EncodeToString(sum[:])
	r := j.tg.apiCall(http.MethodPut, "/v1/outbound/integrations/"+j.integration+"/credential",
		map[string]string{"authorization": j.credentialValue}, j.key)
	if r.status != http.StatusNoContent {
		t.Fatalf("PUT credential: status %d", r.status)
	}
}

// credentialHidden reads the integration back: it reports a configured
// credential and no response carries the value.
func (j *outboundJourney) credentialHidden(t *testing.T) {
	r := j.tg.apiCall(http.MethodGet, "/v1/outbound/integrations", nil, j.key)
	if r.status != http.StatusOK {
		t.Fatalf("GET /v1/outbound/integrations: %s", r)
	}
	if strings.Contains(string(r.body), strings.TrimPrefix(j.credentialValue, "Bearer ")) {
		t.Fatal("the integration list returns the sealed credential")
	}
	items, _ := field(jsonBody(t, r), "items").([]any)
	for _, item := range items {
		if str(item, "id") == j.integration {
			if str(item, "credential_configured") != "true" {
				t.Errorf("after PUT credential the integration reports credential_configured=%s", str(item, "credential_configured"))
			}
			return
		}
	}
	t.Fatalf("the integration list does not include %s", j.integration)
}

func (j *outboundJourney) bindingPath() string {
	return "/v1/apps/" + j.caller + "/outbound-bindings/" + j.integration
}

func (j *outboundJourney) bind(t *testing.T) {
	r := j.tg.apiCall(http.MethodPut, j.bindingPath(), nil, j.key)
	if r.status != http.StatusOK || str(jsonBody(t, r), "integration.id") != j.integration {
		t.Fatalf("PUT outbound binding: %s", r)
	}
	list := j.tg.apiCall(http.MethodGet, "/v1/apps/"+j.caller+"/outbound-bindings", nil, j.key)
	if list.status != http.StatusOK || !strings.Contains(string(list.body), j.integration) {
		t.Fatalf("the app's outbound bindings do not include %s: %s", j.integration, list)
	}
}

// relayCall asks the caller app to call the integration on one of its fixed
// routes and returns the probe's report.
func (j *outboundJourney) relayCall(t *testing.T, route string) map[string]any {
	t.Helper()
	r := get(j.callerBase + "/outbound?integration=" + url.QueryEscape(j.integration) + "&route=" + route)
	if r.status != http.StatusOK {
		t.Fatalf("GET /outbound on %s: %s", j.caller, r)
	}
	doc := jsonBody(t, r)
	if str(doc, "configured") != "true" {
		t.Fatalf("the app has no GREGALE_OUTBOUND_URL: %s", compact(doc))
	}
	return doc
}

func (j *outboundJourney) callThroughRelay(t *testing.T) {
	var last map[string]any
	eventually(t, "a relayed call reaches the provider", 2*time.Minute, 5*time.Second, func() (bool, string) {
		last = j.relayCall(t, "echo")
		return str(last, "status") == "200" && str(last, "body.echo") == "true", compact(last)
	})
	if got := str(last, "body.path"); got != "/outbound-echo/call" {
		t.Errorf("the provider saw path %q, want /outbound-echo/call", got)
	}
	if got := str(last, "body.method"); got != http.MethodGet {
		t.Errorf("the provider saw method %q, want GET", got)
	}
	if got := str(last, "body.authorization_sha256"); got != j.credentialSHA {
		t.Errorf("the provider did not receive the sealed credential (Authorization SHA-256 %q)", got)
	}
}

func (j *outboundJourney) routeRefused(t *testing.T) {
	doc := j.relayCall(t, "denied")
	if str(doc, "status") != "403" || str(doc, "body.code") != "outbound_route_not_allowed" {
		t.Fatalf("a call outside the route policy should be refused 403 outbound_route_not_allowed: %s", compact(doc))
	}
}

func (j *outboundJourney) usageCounted(t *testing.T) {
	for _, path := range []string{"/v1/outbound/integrations/" + j.integration + "/usage", j.bindingPath() + "/usage"} {
		r := j.tg.apiCall(http.MethodGet, path, nil, j.key)
		if r.status != http.StatusOK {
			t.Errorf("GET %s: %s", path, r)
			continue
		}
		doc := jsonBody(t, r)
		if num(doc, "daily_request_count") < 1 || str(doc, "usage_date") == "" {
			t.Errorf("%s does not count the relayed call: %s", path, compact(doc))
		}
	}
}

// bindingCannotWiden tries to give the app a path the integration does not
// allow: the binding policy may only narrow it.
func (j *outboundJourney) bindingCannotWiden(t *testing.T) {
	r := j.tg.apiCall(http.MethodPatch, j.bindingPath(), map[string]any{
		"allowed_methods":       []string{"GET"},
		"allowed_path_prefixes": []string{"/outbound-denied"},
	}, j.key)
	if r.status != http.StatusBadRequest {
		t.Fatalf("a binding policy wider than the integration should be refused 400: %s", r)
	}
}

func (j *outboundJourney) unbind(t *testing.T) {
	if r := j.tg.apiCall(http.MethodDelete, j.bindingPath(), nil, j.key); r.status != http.StatusNoContent {
		t.Fatalf("DELETE outbound binding: %s", r)
	}
}

func (j *outboundJourney) unboundRefused(t *testing.T) {
	eventually(t, "the unbound app is refused", time.Minute, 5*time.Second, func() (bool, string) {
		doc := j.relayCall(t, "echo")
		return str(doc, "status") == "403" && str(doc, "body.code") == "outbound_app_not_attached", compact(doc)
	})
}

func (j *outboundJourney) delete(t *testing.T) {
	if r := j.tg.apiCall(http.MethodDelete, "/v1/outbound/integrations/"+j.integration, nil, j.key); r.status != http.StatusNoContent {
		t.Fatalf("DELETE integration: %s", r)
	}
	j.deleted = true
}

// --- queue pull leases (row 99) ----------------------------------------

// leasedMessage is one message of a lease response.
type leasedMessage struct {
	ID      string          `json:"id"`
	Receipt string          `json:"receipt"`
	Payload json.RawMessage `json:"payload"`
	Attempt int             `json:"attempt"`
}

// queueJourney is the state TestQueuePullLeases' steps share.
type queueJourney struct {
	tg       *target
	key      string
	app      string
	queue    string
	binding  string
	sent     map[string]string // payload "n" -> message id, once leased
	receipts map[string]string // message id -> current receipt
	stale    string            // a receipt superseded by redelivery
	nacked   string            // the message id nacked for redelivery
}

// TestQueuePullLeases is a worker's queue journey (row 99): bind an app to a
// queue in pull mode, send two messages, lease them over the HTTP lease API,
// see a leased message stay hidden, nack one and get it redelivered as a
// second attempt (at-least-once delivery), extend a lease, ack both, see the
// superseded receipt refused, and find the queue empty. Push bindings, the
// SDK consumers, workload-identity tokens from inside a worker, dead
// letters and queue-depth scaling are not run.
func TestQueuePullLeases(t *testing.T) {
	tg := needs(t, tierFull, true)
	t.Parallel()
	j := &queueJourney{tg: tg, sent: map[string]string{}, receipts: map[string]string{}}
	j.key = tg.scopedKey(t, "queues")
	j.app = tg.app(t, "queues")
	// Registered after the app, so the binding is retired before the app is deleted.
	t.Cleanup(func() {
		if j.binding != "" {
			tg.run(t, time.Minute, "queue", "bindings", "rm", j.app, j.binding)
		}
	})
	s := newStory(t)
	s.step("queues_enabled", func(t *testing.T) { featureEnabled(t, tg, "queues", "queue-pull-leases") })
	s.step("create_pull_binding", j.createBinding)
	s.step("send_messages", j.send)
	s.step("lease_messages", j.leaseBoth)
	s.check("leased_message_hidden", j.leasedHidden)
	s.step("nack_redelivers", j.nackRedelivers)
	s.check("extend_lease", j.extend)
	s.step("ack_messages", j.ack)
	s.check("superseded_receipt_refused", j.staleRefused)
	s.check("queue_drained", j.drained)
	s.step("retire_binding", j.retire)
}

func (j *queueJourney) createBinding(t *testing.T) {
	if res := j.tg.exec(t, j.tg.token, t.TempDir(), time.Minute, "deploy", "--create-only", "--app", "--name", j.app); res.exit != 0 {
		t.Fatalf("deploy --create-only %s failed (exit %d): %s", j.app, res.exit, j.tg.redact(res.problem()))
	}
	j.queue = "jobs-" + randomHex(t, 6)
	doc := object(t, j.tg.ok(t, time.Minute, "queue", "bindings", "create", j.app,
		"--name", j.queue, "--queue-name", j.queue, "--mode", "pull", "--workload-class", "worker", "--max-concurrency", "4").stdout)
	j.binding = str(doc, "id")
	if j.binding == "" {
		t.Fatalf("queue bindings create printed no id: %s", compact(doc))
	}
}

func (j *queueJourney) send(t *testing.T) {
	for _, n := range []string{"1", "2"} {
		payload := fmt.Sprintf(`{"n":%s,"run":%q}`, n, j.tg.runID)
		j.tg.ok(t, time.Minute, "queue", "send", j.app, "--queue-name", j.queue, "--payload", payload)
	}
}

func (j *queueJourney) leasePath(suffix string) string {
	return "/v1/apps/" + j.app + "/queues/" + url.PathEscape(j.queue) + "/leases" + suffix
}

// lease leases up to limit messages, waiting up to wait seconds.
func (j *queueJourney) lease(t *testing.T, limit, wait int) []leasedMessage {
	t.Helper()
	r := j.tg.apiCall(http.MethodPost, j.leasePath(""), map[string]int{
		"max_messages": limit, "visibility_timeout_seconds": 60, "wait_seconds": wait,
	}, j.key)
	if r.status != http.StatusOK {
		t.Fatalf("POST leases: %s", r)
	}
	var out struct {
		Messages []leasedMessage `json:"messages"`
	}
	if err := json.Unmarshal(r.body, &out); err != nil {
		t.Fatalf("decode lease response: %v: %s", err, r)
	}
	return out.Messages
}

// payloadN returns a message payload's "n", checking it belongs to this run.
func (j *queueJourney) payloadN(t *testing.T, m leasedMessage) string {
	t.Helper()
	var p struct {
		N   json.Number `json:"n"`
		Run string      `json:"run"`
	}
	if err := json.Unmarshal(m.Payload, &p); err != nil || p.Run != j.tg.runID {
		t.Fatalf("message %s has a payload this run did not send: %s", m.ID, m.Payload)
	}
	return p.N.String()
}

func (j *queueJourney) leaseBoth(t *testing.T) {
	deadline := time.Now().Add(2 * time.Minute)
	for len(j.sent) < 2 && time.Now().Before(deadline) {
		for _, m := range j.lease(t, 10, 20) {
			if m.Attempt != 1 {
				t.Errorf("first lease of message %s reports attempt %d, want 1", m.ID, m.Attempt)
			}
			j.sent[j.payloadN(t, m)] = m.ID
			j.receipts[m.ID] = m.Receipt
		}
	}
	if j.sent["1"] == "" || j.sent["2"] == "" {
		t.Fatalf("leased %d of the 2 sent messages within 2 minutes: %v", len(j.sent), j.sent)
	}
}

func (j *queueJourney) leasedHidden(t *testing.T) {
	if got := j.lease(t, 10, 1); len(got) != 0 {
		t.Fatalf("leased messages are visible to another consumer: %d leased again", len(got))
	}
}

// receiptsCall posts receipts to an ack, nack or extend action and returns
// the response.
func (j *queueJourney) receiptsCall(t *testing.T, action string, body map[string]any) map[string]any {
	t.Helper()
	r := j.tg.apiCall(http.MethodPost, j.leasePath(":"+action), body, j.key)
	if r.status != http.StatusOK {
		t.Fatalf("POST leases:%s: %s", action, r)
	}
	return jsonBody(t, r)
}

func succeeded(doc map[string]any) int {
	list, _ := field(doc, "succeeded").([]any)
	return len(list)
}

func (j *queueJourney) nackRedelivers(t *testing.T) {
	j.nacked = j.sent["1"]
	j.stale = j.receipts[j.nacked]
	doc := j.receiptsCall(t, "nack", map[string]any{"receipts": []string{j.stale}, "error": "e2e retry"})
	if succeeded(doc) != 1 {
		t.Fatalf("nack did not apply: %s", compact(doc))
	}
	deadline := time.Now().Add(3 * time.Minute)
	for time.Now().Before(deadline) {
		for _, m := range j.lease(t, 10, 20) {
			if m.ID != j.nacked {
				t.Fatalf("lease returned message %s, want only the nacked %s", m.ID, j.nacked)
			}
			if m.Attempt != 2 {
				t.Errorf("the redelivered message reports attempt %d, want 2", m.Attempt)
			}
			j.receipts[m.ID] = m.Receipt
			return
		}
	}
	t.Fatalf("the nacked message %s was not redelivered within 3 minutes", j.nacked)
}

func (j *queueJourney) extend(t *testing.T) {
	doc := j.receiptsCall(t, "extend", map[string]any{"receipts": []string{j.receipts[j.nacked]}, "visibility_timeout_seconds": 120})
	if succeeded(doc) != 1 {
		t.Fatalf("extend did not apply to the current receipt: %s", compact(doc))
	}
}

func (j *queueJourney) ack(t *testing.T) {
	doc := j.receiptsCall(t, "ack", map[string]any{"receipts": []string{j.receipts[j.sent["1"]], j.receipts[j.sent["2"]]}})
	if succeeded(doc) != 2 {
		t.Fatalf("ack did not apply to both receipts: %s", compact(doc))
	}
}

// staleRefused acks the receipt the redelivery superseded: it no longer owns
// its message and must be reported lease_expired.
func (j *queueJourney) staleRefused(t *testing.T) {
	doc := j.receiptsCall(t, "ack", map[string]any{"receipts": []string{j.stale}})
	failed, _ := field(doc, "failed").([]any)
	if succeeded(doc) != 0 || len(failed) != 1 || str(failed[0], "reason") != "lease_expired" {
		t.Fatalf("a superseded receipt should fail with lease_expired: %s", compact(doc))
	}
}

func (j *queueJourney) drained(t *testing.T) {
	if got := j.lease(t, 10, 2); len(got) != 0 {
		t.Fatalf("after acking both messages the queue still leases %d", len(got))
	}
}

func (j *queueJourney) retire(t *testing.T) {
	j.tg.ok(t, time.Minute, "queue", "bindings", "rm", j.app, j.binding)
	j.binding = ""
}
