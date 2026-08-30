//go:build !no_pg

// Package e2e — meterd_alerts_e2e_test.go is the §14 issue #396 /
// ADR-045 PR 4 acceptance gate for the meterd alert-evaluator loop.
// It boots the daemon trio (apid + schedd + meterd) against a real
// Postgres, seeds an alert rule for failed_invocations, fires the
// meterd alerts tick, and asserts:
//
//  1. The webhook receiver (httptest.NewServer in-process, signed by
//     the customer's age-sealed secret) accepts the HMAC-signed POST
//     exactly once per cool-down bucket.
//  2. alert_deliveries row exists with Status='delivered' and the
//     payload includes the observed_value.
//  3. alert_rules row transitions from ok to firing with a non-zero
//     last_fired_at.
//  4. A SECOND tick inside the same cool-down window does not dispatch
//     (idempotency_key UNIQUE pin; mirrors pkg/state/PG CTE 23505
//     handling).
//  5. Backdating last_fired_at by > 2× the cool-down window surfaces
//     a fresh dispatch on the next tick.
//  6. A second rule pointing at an SSRF-blocked target records a
//     failed delivery with the oci.ErrImageEgressDenied sentinel in
//     last_error.
//
// To skip locally: export FAAS_SKIP_PG_TESTS=1.
//
// This file is the spec §14 evidence that the alert evaluator's
// production path (apid config → seal → Postgres → meterd unseal →
// webhookout dispatch → alert_deliveries row → audit row) lands a
// delivered webhook end-to-end against a real database. The unit
// tests in pkg/alerts/evaluator_test.go cover the in-memory
// semantics; the cmd/meterd wiring test that goes through the same
// dispatcher stand-in is covered by the in-process meterd tests.
// This file is the cross-process tripwire that pins the wire.
package e2e_test

import (
	"context"
	"crypto/hmac"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"filippo.io/age"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/alerts"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/e2etest"
	"github.com/onebox-faas/faas/pkg/oci"
	"github.com/onebox-faas/faas/pkg/secretbox"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/webhookout"
)

// signingReceiver is a httptest server that records each request's
// headers and body, then asserts the HMAC-SHA256 signature against a
// shared secret. The Signer mirrors pkg/webhookout.Signer so the test
// exercises the EXACT production wire format (header names + hex).
//
// ConnCount is atomic so the test can poll without taking a mutex
// hot path; the per-request data is appended under mu.
type signingReceiver struct {
	mu         sync.Mutex
	secret     []byte
	received   int32 // atomic
	deliveries []deliveryRecord
	signer     *webhookout.Signer
}

type deliveryRecord struct {
	At        time.Time
	Body      []byte
	Signature string
	ID        string
	Unix      string
}

func newSigningReceiver(secret []byte) *signingReceiver {
	return &signingReceiver{secret: secret, signer: webhookout.NewSigner(secret)}
}

func (r *signingReceiver) handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		body, err := io.ReadAll(req.Body)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		_ = req.Body.Close()
		sig := req.Header.Get(webhookout.HeaderSignature)
		id := req.Header.Get(webhookout.HeaderID)
		ts := req.Header.Get(webhookout.HeaderTimestamp)
		// Drop the leading "sha256=" prefix before verify (mirrors
		// pkg/webhookout NewSigner.Verify, but we exercise the
		// unprefixed hex form here since that is what we set
		// when comparing).
		const prefix = "sha256="
		if len(sig) > len(prefix) && sig[:len(prefix)] == prefix {
			sig = sig[len(prefix):]
		}
		unix, perr := strconv.ParseInt(ts, 10, 64)
		if perr != nil {
			http.Error(w, "bad ts: "+perr.Error(), http.StatusBadRequest)
			return
		}
		// Constant-time compare; hmac.Equal is the same primitive
		// pkg/webhookout.Signer.Verify uses.
		want := r.signer.Sign(unix, id, body)
		if !hmac.Equal([]byte(want), []byte(sig)) {
			http.Error(w, "bad signature", http.StatusUnauthorized)
			return
		}
		r.mu.Lock()
		r.deliveries = append(r.deliveries, deliveryRecord{
			At: time.Now(), Body: body, Signature: sig, ID: id, Unix: ts,
		})
		r.mu.Unlock()
		atomic.AddInt32(&r.received, 1)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ack":true}`))
	})
}

func (r *signingReceiver) snapshotCount() int32 {
	return atomic.LoadInt32(&r.received)
}

func (r *signingReceiver) lastBody(t *testing.T) []byte {
	t.Helper()
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.deliveries) == 0 {
		return nil
	}
	return r.deliveries[len(r.deliveries)-1].Body
}

// freshIdentity returns a freshly generated age X25519 identity +
// recipient. Used by the test to seal the webhook_secret and to
// write <tmp>/host.age so meterd can LoadHostKey with the strict
// 0o400 mode pin (pkg/secretbox/hostkey.go:87).
func freshIdentity(t *testing.T) (*age.X25519Identity, *age.X25519Recipient) {
	t.Helper()
	id, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatalf("GenerateX25519Identity: %v", err)
	}
	rcpt := id.Recipient()
	return id, rcpt
}

// writeHostAge writes the identity's serialised form (per pkg/secretbox
// LoadHostKey: a single line with the X25519 bech32 secret) at path
// with mode 0o400 + root-only. LoadHostKey is strict on perms (see
// pkg/secretbox/hostkey.go ErrHostKeyInsecurePerms) so the tripwire
// is also asserted here — the on-disk file must hold 0o400 root:root.
//
// On macOS (where the e2e runs on the dev box) the umask allows the
// OpenFile create-perm, then Chmod sets the target perm; the result
// must hold 0o400 or meterd will fail-fast on load.
func writeHostAge(t *testing.T, path string, id *age.X25519Identity) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o400)
	if err != nil {
		t.Fatalf("open %s: %v", path, err)
	}
	if _, err := f.Write([]byte(id.String())); err != nil {
		_ = f.Close()
		t.Fatalf("write %s: %v", path, err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("close %s: %v", path, err)
	}
	if err := os.Chmod(path, 0o400); err != nil {
		t.Fatalf("chmod %s 0o400: %v", path, err)
	}
	st, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat %s: %v", path, err)
	}
	if got := st.Mode().Perm(); got != 0o400 {
		t.Fatalf("%s perm = %o, want 0o400 (LoadHostKey strict pin)", path, got)
	}
}

// TestMeterdAlertEvaluator_FiresAndDedupes is the §14 issue #396 PR 4
// acceptance gate. Sequence:
//
//  1. Boot apid + schedd + meterd against the pgtest schema with
//     FAAS_ALERT_EVAL_INTERVAL=2s and a freshly-written host.age
//     identity path.
//  2. Seed account + app + 1 alert rule (metric=failed_invocations,
//     gt 0, source=any, cooldown=1 min, webhook_secret_sealed against
//     the test's host identity).
//  3. Stand up an in-process httptest receiver bound to a TCP port.
//     Rewrite the rule's webhook_url to point at the receiver via a
//     loopback-safe URL — the SSRF guard is exercised by the separate
//     SSRF-block test below.
//  4. Insert 5 failed invocations via raw SQL (CountFailedInvocationsSince
//     source=any returns >0 → comparison fires).
//  5. Wait one tick (≤ 2× interval + slack). Assert:
//     - receiver snapshotCount == 1
//     - alert_deliveries row exists with status='delivered'
//     - alert_rules.state == 'firing', last_fired_at > 0
//  6. Wait a second tick inside the cool-down window. Assert: receiver
//     snapshotCount STILL == 1 (idempotency_key UNIQUE blocks the
//     duplicate claim).
//  7. Backdate last_fired_at by 2× cooldown (122 s) so the bucket
//     rolls over. Wait one more tick. Assert: receiver snapshotCount
//     == 2 (fresh dispatch lands).
//
// SSRF case is split into TestMeterdAlertEvaluator_SSRFBlocked
// below so a failure of one doesn't mask the other.
func TestMeterdAlertEvaluator_FiresAndDedupes(t *testing.T) {
	if os.Getenv("FAAS_SKIP_PG_TESTS") != "" {
		t.Skip("FAAS_SKIP_PG_TESTS set")
	}
	pool := pgtest.Open(t)
	ctx := context.Background()

	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatalf("MigrateUp: %v", err)
	}
	pgtest.WaitForMigration(t, pool, 64, 10*time.Second) // alert_rules schema landed at slot 62; latest slot 64

	// Generate identity and write host.age for meterd's
	// FAAS_HOST_AGE_IDENTITY_PATH. Receiver secret is also bound
	// to this identity's age key (PR 2's HMAC + age primitive).
	ident, _ := freshIdentity(t)
	tmpDir := t.TempDir()
	hostKeyPath := filepath.Join(tmpDir, "host.age")
	writeHostAge(t, hostKeyPath, ident)

	// Alert rule secret for the happy path (PR 2's HMAC + age
	// primitive). The SSRF test has its own plaintext + sealed
	// bytes further down (TestMeterdAlertEvaluator_SSRFBlocked
	// generates its own identity + secret so the tripwires stay
	// isolated).
	plaintextHappy := []byte("happy-path-test-secret-no-empty-string-please-12345")
	sealedHappy, err := secretbox.SealBytes(ident.Recipient(), alerts.AlertSecretNamespace, plaintextHappy, 4096)
	if err != nil {
		t.Fatalf("seal happy: %v", err)
	}

	// Receiver: httptest.NewServer with TLSServer=nil (HTTP) —
	// webhookout.Dispatcher dials http://, so the SSRF guard via
	// pkg/oci would refuse 127.0.0.1 on production. We opt INTO
	// the test-only loopback escape hatch by setting
	// FAAS_EGRESS_ALLOW_LOOPBACK=1 BEFORE StartWithEnv so the
	// meterd subprocess inherits it at exec time. The SSRF-block
	// branch below asserts the production path (no override) — so
	// what we exercise here is "loopback allowed, dispatch lands",
	// and what we exercise there is "loopback blocked, last_error
	// contains oci.ErrImageEgressDenied".
	const alertEvalInterval = 2 * time.Second
	// alert_rules_cooldown_chk (migrations/00062) requires
	// BETWEEN 5 AND 1440 — pick the minimum so the
	// backdate-and-refire path doesn't have to wait long.
	const cooldownMin = 5

	t.Setenv("FAAS_EGRESS_ALLOW_LOOPBACK", "1")
	receiver := newSigningReceiver(plaintextHappy)
	srv := httptest.NewServer(receiver.handler())
	defer srv.Close()

	h := e2etest.StartWithEnv(t, pool,
		e2etest.APID|e2etest.Schedd|e2etest.Meterd,
		[]string{
			"FAAS_ALERT_EVAL_INTERVAL=" + alertEvalInterval.String(),
			"FAAS_HOST_AGE_IDENTITY_PATH=" + hostKeyPath,
		})

	store := state.NewPgStore(pool)

	acct, err := store.CreateAccount(ctx, "alerts-e2e-"+time.Now().Format(time.RFC3339Nano)+"@example.com", api.PlanHobby)
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	app, err := store.CreateApp(ctx, state.App{
		AccountID: acct.ID, Slug: "alerts-e2e-" + time.Now().Format("150405.000000"),
		Type: state.AppTypeApp, RAMMB: 256, MaxConcurrency: 1, IdleTimeoutS: 30,
	})
	if err != nil {
		t.Fatalf("CreateApp: %v", err)
	}
	rule, err := store.CreateAlertRule(ctx, state.AlertRule{
		AccountID:           acct.ID,
		AppID:               app.ID,
		Name:                "happy-path-rule-" + time.Now().Format("150405.000000"),
		Enabled:             true,
		Metric:              state.AlertMetricFailedInvocs,
		Comparison:          state.AlertGt,
		Threshold:           0,
		WindowSpec:          state.AlertWindow5m,
		FailureSource:       state.AlertFailureAny,
		WebhookURL:          srv.URL,
		WebhookSecretSealed: sealedHappy,
		CooldownMinutes:     cooldownMin,
	})
	if err != nil {
		t.Fatalf("CreateAlertRule: %v", err)
	}

	// Seed 5 failed invocation rows for (acct, app) with a mix of
	// sources so CountFailedInvocationsSince(source='any') walks
	// all four source-named columns (Cron, Queue, DelayedTask,
	// AsyncInvoke). Mirrors the alert evaluator's summariseFailed
	// path which sums across all four sources for source='any'.
	for i := 0; i < 5; i++ {
		// invocations_source_check requires source in the closed
		// set {cron, queue, delayed_task, async_invoke}. Empty
		// source is rejected by the CHECK constraint.
		src := []string{"cron", "queue", "delayed_task", "async_invoke"}[i%4]
		// invocations.id is uuid-typed; invocations.state (not
		// 'status') carries the lifecycle column. The id is a fresh
		// UUID — not derived from rule.ID — because the table has no
		// rule_id column and a UUID primary key is the canonical
		// insert shape.
		if _, err := pool.Exec(ctx,
			`insert into invocations (id, account_id, app_id, source, state, created_at) values ($1, $2, $3, $4, 'failed', now())`,
			uuid.NewString(), acct.ID, app.ID, src); err != nil {
			t.Fatalf("seed invocation %d: %v", i, err)
		}
	}

	// Allow one full eval interval plus slack for boot + PromQL
	// degraded path to take shape (PromQL not configured → all
	// PromQL-backed metrics gate to "degraded:" skipped, but
	// failed_invocations is Postgres-backed so it still fires).
	deadline := time.Now().Add(alertEvalInterval*3 + 5*time.Second)
	for receiver.snapshotCount() < 1 {
		if time.Now().After(deadline) {
			t.Fatalf("receiver never received a delivery within %s (alerts tick did not fire)\n%s",
				alertEvalInterval*3+5*time.Second, h.MeterdLogs())
		}
		time.Sleep(150 * time.Millisecond)
	}

	// Snapshot #1: delivered, payload non-empty, state firing.
	if got := receiver.snapshotCount(); got != 1 {
		t.Fatalf("after first tick: receiver count = %d; want 1 (HMAC verify would have failed closed)", got)
	}
	deliveries, err := store.ListAlertDeliveriesForRule(ctx, rule.ID, 5, false)
	if err != nil || len(deliveries) != 1 {
		t.Fatalf("after first tick: ListAlertDeliveriesForRule err=%v count=%d; want 1", err, len(deliveries))
	}
	if deliveries[0].Status != state.AlertDeliveryDelivered {
		t.Errorf("delivery[0].Status = %s; want delivered (last_error=%q)", deliveries[0].Status, deliveries[0].LastError)
	}
	// Payload is non-empty JSON; observed_value is what we recorded
	// when sealing (the evaluator passes the observed CountFailed).
	if deliveries[0].ObservedValue <= 0 {
		t.Errorf("delivery[0].ObservedValue = %v; want > 0", deliveries[0].ObservedValue)
	}
	if len(deliveries[0].Payload) == 0 {
		t.Errorf("delivery[0].Payload is empty; evaluator should serialise the alert payload")
	}
	// Verify the payload is valid JSON and contains the rule's metric.
	var payload map[string]any
	if err := json.Unmarshal(deliveries[0].Payload, &payload); err != nil {
		t.Fatalf("payload unmarshal: %v (payload=%s)", err, deliveries[0].Payload)
	}
	if payload["metric"] != string(state.AlertMetricFailedInvocs) {
		t.Errorf("payload.metric = %v; want %s", payload["metric"], state.AlertMetricFailedInvocs)
	}
	if payload["rule_name"] != rule.Name {
		t.Errorf("payload.rule_name = %v; want %s", payload["rule_name"], rule.Name)
	}

	ruleRow, err := store.AlertRuleByID(ctx, rule.ID)
	if err != nil {
		t.Fatalf("AlertRuleByID: %v", err)
	}
	if ruleRow.State != state.AlertStateFiring {
		t.Errorf("rule.State = %s; want firing", ruleRow.State)
	}
	if ruleRow.LastFiredAt.IsZero() {
		t.Errorf("rule.LastFiredAt is zero; want non-zero after dispatch")
	}

	// Verify the receiver body matches the payload stored in
	// alert_deliveries (downstream customers persist the JSON
	// blob too; mismatch means evaluator or dispatcher dropped a
	// field). Lightweight: just compare the rule_name key.
	if body := receiver.lastBody(t); len(body) > 0 {
		var recv map[string]any
		if err := json.Unmarshal(body, &recv); err != nil {
			t.Fatalf("receiver body unmarshal: %v", err)
		}
		if recv["rule_name"] != rule.Name {
			t.Errorf("body.rule_name = %v; want %s", recv["rule_name"], rule.Name)
		}
	}

	// Phase 2: second tick inside the cooldown window (cooldown is
	// 1 minute; two intervals = 4 s so we're well inside).
	time.Sleep(alertEvalInterval * 2)
	if got := receiver.snapshotCount(); got != 1 {
		t.Fatalf("after second tick (inside cooldown): receiver count = %d; want 1 (idempotency_key UNIQUE blocks duplicate claim)", got)
	}

	// Phase 3: backdate last_fired_at by 2× cooldown so the bucket
	// rolls over. The evaluator's idempotency key is
	// rule.ID + ":" + (now.Unix() / (cooldownMin*60)), so a 122-s
	// backdate shifts into a fresh bucket.
	twoCooldownsAgo := time.Now().UTC().Add(-time.Duration(2*cooldownMin) * time.Minute)
	if _, err := pool.Exec(ctx,
		`update alert_rules set last_fired_at = $1 where id = $2`,
		twoCooldownsAgo, rule.ID); err != nil {
		t.Fatalf("backdate last_fired_at: %v", err)
	}

	deadline = time.Now().Add(alertEvalInterval*3 + 5*time.Second)
	for receiver.snapshotCount() < 2 {
		if time.Now().After(deadline) {
			t.Fatalf("after backdate: receiver count stayed at %d (expected 2 within %s)\n%s",
				receiver.snapshotCount(), alertEvalInterval*3+5*time.Second, h.MeterdLogs())
		}
		time.Sleep(150 * time.Millisecond)
	}
	if got := receiver.snapshotCount(); got != 2 {
		t.Fatalf("after 3rd tick: receiver count = %d; want 2", got)
	}
}

// TestMeterdAlertEvaluator_SSRFBlocked is the §11 SSRF pin for the
// alert evaluator: a webhook URL pointing at 127.0.0.1:1 (the
// "guaranteed refused" address) must surface as
// status='failed' + last_error containing oci.ErrImageEgressDenied
// (or the test-shim equivalent — pkg/oci has a stable sentinel
// string "egress denied" in the formatted error).
//
// Mirrors pkg/webhookout tests (webhookout_test.go around L493-536)
// which exercise the SSRF guard in isolation; this is the
// cross-process version that proves the meterd-side evaluator picks
// up the same path.
func TestMeterdAlertEvaluator_SSRFBlocked(t *testing.T) {
	if os.Getenv("FAAS_SKIP_PG_TESTS") != "" {
		t.Skip("FAAS_SKIP_PG_TESTS set")
	}
	pool := pgtest.Open(t)
	ctx := context.Background()

	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatalf("MigrateUp: %v", err)
	}
	pgtest.WaitForMigration(t, pool, 64, 10*time.Second) // alert_rules schema landed at slot 62; latest slot 64

	ident, _ := freshIdentity(t)
	tmpDir := t.TempDir()
	hostKeyPath := filepath.Join(tmpDir, "host.age")
	writeHostAge(t, hostKeyPath, ident)

	plaintext := []byte("ssrf-secret-32b-padding-for-good-measure-zzz")
	sealed, err := secretbox.SealBytes(ident.Recipient(), alerts.AlertSecretNamespace, plaintext, 4096)
	if err != nil {
		t.Fatalf("seal ssrf rule secret: %v", err)
	}

	// CRITICAL: do NOT set FAAS_EGRESS_ALLOW_LOOPBACK=1 — the
	// production SSRF guard rejects loopback. We expect a failed
	// delivery with an egress-denied error string.
	const alertEvalInterval = 2 * time.Second

	h := e2etest.StartWithEnv(t, pool,
		e2etest.APID|e2etest.Schedd|e2etest.Meterd,
		[]string{
			"FAAS_ALERT_EVAL_INTERVAL=" + alertEvalInterval.String(),
			"FAAS_HOST_AGE_IDENTITY_PATH=" + hostKeyPath,
		})

	store := state.NewPgStore(pool)

	acct, err := store.CreateAccount(ctx, "alerts-ssrf-"+time.Now().Format(time.RFC3339Nano)+"@example.com", api.PlanHobby)
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	app, err := store.CreateApp(ctx, state.App{
		AccountID: acct.ID, Slug: "alerts-ssrf-" + time.Now().Format("150405.000000"),
		Type: state.AppTypeApp, RAMMB: 256, MaxConcurrency: 1, IdleTimeoutS: 30,
	})
	if err != nil {
		t.Fatalf("CreateApp: %v", err)
	}
	rule, err := store.CreateAlertRule(ctx, state.AlertRule{
		AccountID:           acct.ID,
		AppID:               app.ID,
		Name:                "ssrf-rule-" + time.Now().Format("150405.000000"),
		Enabled:             true,
		Metric:              state.AlertMetricFailedInvocs,
		Comparison:          state.AlertGt,
		Threshold:           0,
		WindowSpec:          state.AlertWindow5m,
		FailureSource:       state.AlertFailureAny,
		WebhookURL:          "http://127.0.0.1:1/hook",
		WebhookSecretSealed: sealed,
		// Cooldown must satisfy alert_rules_cooldown_chk
		// (migrations/00062: BETWEEN 5 AND 1440). We pick the
		// minimum (5) so the backdate-and-refire path below
		// doesn't have to wait long.
		CooldownMinutes: 5,
	})
	if err != nil {
		t.Fatalf("CreateAlertRule: %v", err)
	}

	// Seed failed invocations so the rule fires.
	for i := 0; i < 3; i++ {
		if _, err := pool.Exec(ctx,
			`insert into invocations (id, account_id, app_id, source, state, created_at) values ($1, $2, $3, $4, 'failed', now())`,
			uuid.NewString(), acct.ID, app.ID, "queue"); err != nil {
			t.Fatalf("seed ssrf invocation %d: %v", i, err)
		}
	}

	// Wait for alert_deliveries to land with status=failed. meterd
	// records the SSRF rejection synchronously inside Dispatch
	// (oci.ErrImageEgressDenied is terminal, no retries).
	deadline := time.Now().Add(alertEvalInterval*3 + 5*time.Second)
	for {
		deliveries, err := store.ListAlertDeliveriesForRule(ctx, rule.ID, 5, false)
		if err != nil {
			t.Fatalf("ListAlertDeliveriesForRule: %v", err)
		}
		if len(deliveries) >= 1 && deliveries[0].Status == state.AlertDeliveryFailed {
			// Found the failed delivery — verify the error
			// string. The SSRF block surfaces
			// oci.ErrImageEgressDenied ("oci: egress denied by
			// policy"); the persisted LastError carries the
			// "egress denied" substring. Pin against
			// oci.EgressDeniedMessage so a future tweak to the
			// sentinel's Error() that keeps the substring
			// continues to satisfy this assertion.
			if !contains([]byte(deliveries[0].LastError), oci.EgressDeniedMessage) {
				t.Errorf("delivery.LastError = %q; want substring %q (oci sentinel)", deliveries[0].LastError, oci.EgressDeniedMessage)
			}
			// Note: we deliberately do NOT assert HTTP status
			// code (always 0 for an SSRF refusal — never
			// reached the wire).
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("ssrf delivery did not land with status=failed within %s (expected one; got %d rows)\n%s",
				alertEvalInterval*3+5*time.Second, len(deliveries), h.MeterdLogs())
		}
		time.Sleep(150 * time.Millisecond)
	}
}
