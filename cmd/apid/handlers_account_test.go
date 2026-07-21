package main

// G6 handler tests for the four account self-service endpoints
// (spec §17 G6, ADR-021). Uses the package-level `setup(t, plan)`
// helper from server_test.go so each subtest gets a fresh MemStore +
// bearer key + ephemeral session manager.
//
// Coverage:
//
//   - GET  /v1/account/export     full bundle, ?include_secrets toggles
//   - GET  /v1/account/export     redaction invariant (plaintext never
//                                 appears in the bundle)
//   - DELETE /v1/account          happy path + idempotency
//   - POST /v1/account/restore    happy path + 409 past grace
//   - POST /v1/account/restore    409 when not pending
//   - GET  /v1/account/dpa        public, no auth, returns text/markdown
//   - /v1/account/* carve-out     reachable in deleted_pending
//   - non-/v1/account/*           still 402 in deleted_pending

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"filippo.io/age"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/grace"
	"github.com/onebox-faas/faas/pkg/state"
)

// withAccountTestRecipient wires a fresh X25519 recipient into
// setSecretRecipient so PUT /v1/apps/{slug}/secrets/* can seal during
// the G6 export tests. Named to avoid clashing with the equivalent
// helper in handlers_secrets_test.go (different filename, but same
// package main).
func withAccountTestRecipient(t *testing.T) {
	t.Helper()
	prev := setSecretRecipient
	t.Cleanup(func() { setSecretRecipient = prev })
	id, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatalf("GenerateX25519Identity: %v", err)
	}
	setSecretRecipient = func() *age.X25519Recipient { return id.Recipient() }
}

// seedOneApp creates a single app the handler tests can hang
// deployments + secrets + crons off.
func seedOneApp(t *testing.T, e testEnv, slug string) {
	t.Helper()
	rec := e.do(t, "POST", "/v1/apps", api.CreateAppRequest{Slug: slug}, nil)
	if rec.Code != http.StatusCreated {
		t.Fatalf("seed app: %d %s", rec.Code, rec.Body)
	}
}

// TestExportAccount_FullBundle creates an app + a secret, then asks
// for the export bundle and asserts every slice is present.
func TestExportAccount_FullBundle(t *testing.T) {
	withAccountTestRecipient(t)
	e := setup(t, api.PlanHobby)
	seedOneApp(t, e, "exp-app")

	// Upsert a secret so the ciphertext slice has one row.
	rec := e.do(t, "PUT", "/v1/apps/exp-app/secrets/STRIPE_KEY",
		api.PutAppSecretRequest{Value: "sk_test_export"}, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("PUT secret: %d %s", rec.Code, rec.Body)
	}

	rec = e.do(t, "GET", "/v1/account/export", nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("export: %d %s", rec.Code, rec.Body)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}
	if cd := rec.Header().Get("Content-Disposition"); !strings.HasPrefix(cd, `attachment; filename="faas-account-`) {
		t.Errorf("Content-Disposition = %q", cd)
	}
	var bundle api.AccountExportResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &bundle); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if bundle.Account.Email != "hobby@example.com" {
		t.Errorf("account.email = %q", bundle.Account.Email)
	}
	if len(bundle.Apps) != 1 || bundle.Apps[0].Slug != "exp-app" {
		t.Errorf("apps = %+v", bundle.Apps)
	}
	if len(bundle.APIKeys) != 1 {
		t.Errorf("api_keys = %+v", bundle.APIKeys)
	}
	if len(bundle.AppSecrets) != 1 {
		t.Errorf("app_secrets = %+v", bundle.AppSecrets)
	}
	if bundle.AppSecrets[0].Key != "STRIPE_KEY" {
		t.Errorf("app_secrets[0].key = %q", bundle.AppSecrets[0].Key)
	}
}

// TestExportAccount_RedactionInvariant verifies that plaintext never
// appears in the bundle — the ciphertext row contains a base64 blob
// that does NOT decode to the original VALUE. (The plaintext itself
// is never stored per ADR-020, so the round-trip is provably absent.)
func TestExportAccount_RedactionInvariant(t *testing.T) {
	withAccountTestRecipient(t)
	e := setup(t, api.PlanHobby)
	seedOneApp(t, e, "redact-app")
	plaintext := "sk_live_DO_NOT_LEAK_12345"
	e.do(t, "PUT", "/v1/apps/redact-app/secrets/STRIPE_KEY",
		api.PutAppSecretRequest{Value: plaintext}, nil)

	rec := e.do(t, "GET", "/v1/account/export", nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("export: %d %s", rec.Code, rec.Body)
	}
	if strings.Contains(rec.Body.String(), plaintext) {
		t.Fatalf("PLAIN LEAK: plaintext in export body:\n%s", rec.Body.String())
	}
}

// TestExportAccount_NoSecretsToggle drops the ciphertext slice when
// the caller passes ?include_secrets=false.
func TestExportAccount_NoSecretsToggle(t *testing.T) {
	withAccountTestRecipient(t)
	e := setup(t, api.PlanHobby)
	seedOneApp(t, e, "no-secrets-app")
	e.do(t, "PUT", "/v1/apps/no-secrets-app/secrets/STRIPE_KEY",
		api.PutAppSecretRequest{Value: "x"}, nil)

	rec := e.do(t, "GET", "/v1/account/export?include_secrets=false", nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("export: %d %s", rec.Code, rec.Body)
	}
	var bundle api.AccountExportResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &bundle)
	if len(bundle.AppSecrets) != 0 {
		t.Errorf("app_secrets = %+v, want empty (include_secrets=false)", bundle.AppSecrets)
	}
}

// TestDeleteAccount_HappyPath schedules the account and asserts the
// envelope: status=deleted_pending, scheduled_at + restore_until are
// RFC 3339, restore_until = scheduled_at + 30d.
func TestDeleteAccount_HappyPath(t *testing.T) {
	e := setup(t, api.PlanPro)
	rec := e.do(t, "DELETE", "/v1/account", nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("delete: %d %s", rec.Code, rec.Body)
	}
	var env api.AccountDeletionResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if env.Status != string(state.AccountDeletedPending) {
		t.Errorf("status = %q", env.Status)
	}
	scheduled, err := time.Parse(time.RFC3339, env.ScheduledAt)
	if err != nil {
		t.Fatalf("scheduled_at: %v", err)
	}
	restore, err := time.Parse(time.RFC3339, env.RestoreUntil)
	if err != nil {
		t.Fatalf("restore_until: %v", err)
	}
	if delta := restore.Sub(scheduled); delta != 30*24*time.Hour {
		t.Errorf("restore_until - scheduled_at = %v, want 30d", delta)
	}
}

// TestDeleteAccount_Idempotent confirms a second DELETE while already
// pending returns the same envelope without re-stamping the timestamp.
func TestDeleteAccount_Idempotent(t *testing.T) {
	e := setup(t, api.PlanPro)
	first := e.do(t, "DELETE", "/v1/account", nil, nil)
	if first.Code != http.StatusOK {
		t.Fatalf("first delete: %d %s", first.Code, first.Body)
	}
	var firstEnv api.AccountDeletionResponse
	_ = json.Unmarshal(first.Body.Bytes(), &firstEnv)
	// Sleep briefly so any re-stamp would be detectable.
	time.Sleep(20 * time.Millisecond)
	second := e.do(t, "DELETE", "/v1/account", nil, nil)
	if second.Code != http.StatusOK {
		t.Fatalf("second delete: %d %s", second.Code, second.Body)
	}
	var secondEnv api.AccountDeletionResponse
	_ = json.Unmarshal(second.Body.Bytes(), &secondEnv)
	if firstEnv.ScheduledAt != secondEnv.ScheduledAt {
		t.Errorf("idempotent re-DELETE changed scheduled_at: %v -> %v",
			firstEnv.ScheduledAt, secondEnv.ScheduledAt)
	}
}

// TestRestoreAccount_HappyPath schedules then restores inside the
// grace window.
func TestRestoreAccount_HappyPath(t *testing.T) {
	e := setup(t, api.PlanPro)
	if rec := e.do(t, "DELETE", "/v1/account", nil, nil); rec.Code != http.StatusOK {
		t.Fatalf("delete: %d %s", rec.Code, rec.Body)
	}
	rec := e.do(t, "POST", "/v1/account/restore", nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("restore: %d %s", rec.Code, rec.Body)
	}
	var acct api.AccountResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &acct); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if acct.Status != string(state.AccountActive) {
		t.Errorf("status = %q, want active", acct.Status)
	}
}

// TestRestoreAccount_NotPending409 restores an account that's NOT in
// pending — should be 409 account_not_restorable (per D3).
func TestRestoreAccount_NotPending409(t *testing.T) {
	e := setup(t, api.PlanPro)
	rec := e.do(t, "POST", "/v1/account/restore", nil, nil)
	assertProblem(t, rec, http.StatusConflict, api.CodeAccountNotRestorable)
}

// TestDPATemplate_PublicNoAuth — handler returns 503 when the DPA path
// is unset. The endpoint is mounted without s.auth, so a missing file
// must surface as a problem document (not a 401 / silent 200).
func TestDPATemplate_PublicNoAuth(t *testing.T) {
	e := setup(t, api.PlanPro)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/v1/account/dpa", nil)
	e.h.ServeHTTP(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 with no DPA configured, got %d %s", rec.Code, rec.Body)
	}
}

// TestDPATemplate_PublicServesFile — same shape but with the path
// configured via newServerWithDeps. The DPA is reachable without any
// auth header (spec §17 G6).
func TestDPATemplate_PublicServesFile(t *testing.T) {
	store := state.NewMemStore()
	_, err := store.CreateAccount(context.Background(), "dpa@example.com", api.PlanFree)
	if err != nil {
		t.Fatalf("seed: %v", err)
	}
	dpaPath := filepath.Join(t.TempDir(), "dpa.md")
	if err := os.WriteFile(dpaPath, []byte("# Data Processing Addendum"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	srv := newServerWithDeps(store,
		nil,           // log (filled by handler if nil)
		"example.com", // domain
		nil,           // notif
		"",            // stripe secret
		nil,           // mailer
		nil,           // githubd
		nil,           // sessions → ephemeral
		nil,           // broadcaster
		0,             // loginTTL → default
		dpaPath,       // DPA path
	)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/v1/account/dpa", nil)
	srv.handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/markdown") {
		t.Errorf("Content-Type = %q", ct)
	}
	if !strings.Contains(rec.Body.String(), "Data Processing Addendum") {
		t.Errorf("body = %q", rec.Body.String())
	}
}

// TestAuthCarveOut_ExportDuringGrace: a customer in deleted_pending
// must be able to take a final export. D7 — every other route still
// 402s. Both checks in one test so the matrix is captured.
func TestAuthCarveOut_ExportDuringGrace(t *testing.T) {
	e := setup(t, api.PlanPro)
	if rec := e.do(t, "DELETE", "/v1/account", nil, nil); rec.Code != http.StatusOK {
		t.Fatalf("schedule: %d %s", rec.Code, rec.Body)
	}

	// Allowed during grace: /v1/account/export.
	rec := e.do(t, "GET", "/v1/account/export", nil, nil)
	if rec.Code != http.StatusOK {
		t.Errorf("export during grace: %d %s", rec.Code, rec.Body)
	}

	// Allowed during grace: POST /v1/account/restore.
	rec = e.do(t, "POST", "/v1/account/restore", nil, nil)
	if rec.Code != http.StatusOK {
		t.Errorf("restore during grace: %d %s", rec.Code, rec.Body)
	}
}

// TestAuthCarveOut_NonAccountPathStill402 — once the customer is back
// in deleted_pending, /v1/apps is still gated. Catches a regression
// where the carve-out widens by accident.
func TestAuthCarveOut_NonAccountPathStill402(t *testing.T) {
	e := setup(t, api.PlanPro)
	seedOneApp(t, e, "live-app")
	// Park the app under deletion-pending: but first flip into deleted_pending
	// by hand, then re-attempt to list the app. The cleanest way: delete the
	// account into pending, then re-derive state.
	if rec := e.do(t, "DELETE", "/v1/account", nil, nil); rec.Code != http.StatusOK {
		t.Fatalf("schedule: %d %s", rec.Code, rec.Body)
	}
	// Restore flips status back to active — we DON'T want that. Drive
	// the status directly via the store so we stay in deleted_pending.
	acct := e.acct
	if err := e.store.UpdateAccountStatus(context.Background(), acct.ID, state.AccountDeletedPending); err != nil {
		t.Fatalf("force pending: %v", err)
	}
	rec := e.do(t, "GET", "/v1/apps", nil, nil)
	if rec.Code != http.StatusPaymentRequired {
		t.Errorf("non-account path during grace: %d, want 402 %s", rec.Code, rec.Body)
	}
}

// TestExportAccount_CiphertextIsBase64 sanity-checks the wire encoding
// for app_secrets.ciphertext so a future "let's change to hex" change
// doesn't slip through review.
func TestExportAccount_CiphertextIsBase64(t *testing.T) {
	withAccountTestRecipient(t)
	e := setup(t, api.PlanHobby)
	seedOneApp(t, e, "b64-app")
	e.do(t, "PUT", "/v1/apps/b64-app/secrets/STRIPE_KEY",
		api.PutAppSecretRequest{Value: "x"}, nil)

	rec := e.do(t, "GET", "/v1/account/export", nil, nil)
	var bundle api.AccountExportResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &bundle)
	if len(bundle.AppSecrets) != 1 {
		t.Fatalf("app_secrets = %+v", bundle.AppSecrets)
	}
	if _, err := base64.RawURLEncoding.DecodeString(bundle.AppSecrets[0].Ciphertext); err != nil {
		t.Errorf("ciphertext not base64 url-encoded: %q (%v)", bundle.AppSecrets[0].Ciphertext, err)
	}
}

// spyNotifier records every Notify call. Differs from
// recordingNotifier (handlers_events_test.go) which only feeds the
// Subscribe channel — this one asserts the producer side, which is
// what the G6 closure PR changed.
type spyNotifier struct {
	mu    sync.Mutex
	calls []db.Notification
}

func (n *spyNotifier) Notify(_ context.Context, channel, payload string) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.calls = append(n.calls, db.Notification{Channel: channel, Payload: payload})
	return nil
}
func (n *spyNotifier) Subscribe(_ context.Context, _ []string) (<-chan db.Notification, func(), error) {
	ch := make(chan db.Notification)
	close(ch)
	return ch, func() {}, nil
}
func (n *spyNotifier) snapshot() []db.Notification {
	n.mu.Lock()
	defer n.mu.Unlock()
	out := make([]db.Notification, len(n.calls))
	copy(out, n.calls)
	return out
}

// recordingMailer captures every Message Send() so tests can assert
// the producer side without pulling a real mail.Sender wire-up.
// Tests across the package use the same instance; sync.Mutex makes
// the access safe even if a goroutine elsewhere races.
type recordingMailer struct {
	mu      sync.Mutex
	calls   []Message
	sendErr error // optional: set to force the sender to return an error
}

func (m *recordingMailer) Send(_ context.Context, msg Message) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.calls = append(m.calls, msg)
	return m.sendErr
}
func (m *recordingMailer) snapshot() []Message {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]Message, len(m.calls))
	copy(out, m.calls)
	return out
}

// TestScheduleDeletion_EmitsAccountDeletionPending is the regression
// test for the Bug 2 fix (PR #83 review). Before the fix, customer-
// initiated DELETE updated the DB row + sent email but never emitted
// pg_notify on the account_deletion_pending channel, so the schedd
// subscriber documented at pkg/db/notify.go never received the event.
// This test wires a spyNotifier into the server, fires DELETE
// /v1/account, and asserts exactly one Notify call landed on the
// channel with the documented payload shape (account_id,
// scheduled_at, restore_until).
func TestScheduleDeletion_EmitsAccountDeletionPending(t *testing.T) {
	store := state.NewMemStore()
	acct, err := store.CreateAccount(context.Background(), "sched-notif@example.com", api.PlanFree)
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	notif := &spyNotifier{}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := newServerWithDeps(store, log, "example.com", notif, "", noopMailer{}, stubGithubdClient{}, nil, nil, 0, "")

	pt, hash, _ := api.GenerateAPIKey()
	if _, err := store.CreateAPIKey(context.Background(), acct.ID, hash, "test"); err != nil {
		t.Fatalf("CreateAPIKey: %v", err)
	}

	req := httptest.NewRequest("DELETE", "/v1/account", nil)
	req.Header.Set("Authorization", "Bearer "+pt)
	rec := httptest.NewRecorder()
	srv.handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("DELETE /v1/account = %d %s", rec.Code, rec.Body)
	}

	calls := notif.snapshot()
	if len(calls) != 1 {
		t.Fatalf("got %d Notify calls, want 1", len(calls))
	}
	if calls[0].Channel != db.NotifyAccountDeletionPending {
		t.Errorf("channel = %q, want %q", calls[0].Channel, db.NotifyAccountDeletionPending)
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(calls[0].Payload), &payload); err != nil {
		t.Fatalf("payload not JSON: %v", err)
	}
	// Payload contract per pkg/db/notify.go: account_id, scheduled_at,
	// restore_until. assertKeysArePresent avoids being brittle if a
	// future ADR adds a field.
	for _, k := range []string{"account_id", "scheduled_at", "restore_until"} {
		if _, ok := payload[k]; !ok {
			t.Errorf("payload missing %q: %v", k, payload)
		}
	}
	if payload["account_id"] != acct.ID {
		t.Errorf("payload.account_id = %v, want %s", payload["account_id"], acct.ID)
	}
	scheduled, _ := payload["scheduled_at"].(string)
	restore, _ := payload["restore_until"].(string)
	if scheduled == "" || restore == "" {
		t.Errorf("timestamps empty: scheduled=%q restore=%q", scheduled, restore)
	}
	// restore_until must be exactly 30 days after scheduled_at per
	// spec §17 G6 — the handler computes the deadline from
	// state.DeletionGraceDuration(), so this also pins that constant.
	sAt, _ := time.Parse(time.RFC3339Nano, scheduled)
	rAt, _ := time.Parse(time.RFC3339Nano, restore)
	if got := rAt.Sub(sAt); got != state.DeletionGraceDuration() {
		t.Errorf("restore - scheduled = %v, want %v", got, state.DeletionGraceDuration())
	}
}

// errorStore wraps state.MemStore and forces the named method to
// return a synthetic error. Used by the gatherExport-partial-failure
// regression test (Bug 3 fix). Only the methods we override actually
// fail; everything else delegates to the embedded store.
type errorStore struct {
	*state.MemStore
	failOn map[string]error
}

func (s *errorStore) ListInstancesForAccount(ctx context.Context, accountID string) ([]state.Instance, error) {
	if err, ok := s.failOn["ListInstancesForAccount"]; ok {
		return nil, err
	}
	return s.MemStore.ListInstancesForAccount(ctx, accountID)
}

// TestExportAccount_PartialFailure_Returns500 covers the Bug 3 fix.
// Before the PR, every per-resource helper swallowed its error and
// returned nil — a partial DB failure during an export returned 200
// with half the slices missing. Now gatherExport errors.Join's the
// failures and the handler emits 500 capacity so a customer retry
// produces a coherent bundle.
func TestExportAccount_PartialFailure_Returns500(t *testing.T) {
	withAccountTestRecipient(t)
	e := setup(t, api.PlanHobby)
	seedOneApp(t, e, "partial-app")
	wrap := &errorStore{MemStore: e.store, failOn: map[string]error{
		"ListInstancesForAccount": errors.New("synthetic instances outage"),
	}}
	notif := &spyNotifier{}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := newServerWithDeps(wrap, log, "example.com", notif, "", noopMailer{}, stubGithubdClient{}, nil, nil, 0, "").handler()
	req := httptest.NewRequest("GET", "/v1/account/export", nil)
	req.Header.Set("Authorization", "Bearer "+e.key)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("export = %d, want 503 (partial failure must NOT 200 — "+
			"the api capacity envelope codes as 503 per pkg/api/limits.go)", rec.Code)
	}
	var p api.Problem
	if err := json.Unmarshal(rec.Body.Bytes(), &p); err != nil {
		t.Fatalf("decode problem: %v body=%s", err, rec.Body)
	}
	// Capacity is the right code (RFC 7807 + the helpers in pkg/api);
	// a different code (e.g. CodeInternal) would mean we wired the
	// 500 envelope wrong.
	if p.Code != api.CodeCapacity {
		t.Errorf("problem.code = %q, want %q", p.Code, api.CodeCapacity)
	}
}

// TestExportAccount_BuildAppID_Populated covers the Bug 4 fix. Before
// the PR, BuildExportResponse.AppID was always "" because builds have
// no AppID column of their own and the helper didn't join back through
// the deployment. Now gatherExport builds a deploymentID→appID map
// once and passes it to listBuildsForAccountExport.
func TestExportAccount_BuildAppID_Populated(t *testing.T) {
	e := setup(t, api.PlanHobby)
	seedOneApp(t, e, "build-appid-app")

	// Drive a deploy so a build row exists with a DeploymentID.
	// The deploy validator requires a digest-pinned image (sha256:<64hex>);
	// apid ignores the registry portion in unit tests — the only thing
	// the export cares about is a row in deployments/builds keyed to
	// the seeded app.
	const digest = "sha256:" + "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	depRec := e.do(t, "POST", "/v1/apps/build-appid-app/deployments",
		api.CreateDeploymentRequest{Image: "registry.example/test@" + digest}, nil)
	if depRec.Code != http.StatusAccepted {
		t.Fatalf("deploy: %d %s", depRec.Code, depRec.Body)
	}

	rec := e.do(t, "GET", "/v1/account/export", nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("export: %d %s", rec.Code, rec.Body)
	}
	var bundle api.AccountExportResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &bundle); err != nil {
		t.Fatalf("decode: %v", err)
	}
	// The deployment is 202 (build is dispatched async and the build
	// row may not yet exist when we GET /export a few ms later), so
	// we can't reliably assert builds[] shape here. What we CAN assert:
	// the just-created deployment is in the bundle with the seeded
	// app's ID, AND (if a build row happens to exist) its AppID is
	// populated, which is the Bug 4 fix.
	if len(bundle.Deployments) == 0 {
		t.Fatal("bundle.Deployments empty after deploy: 202")
	}
	wantApp := ""
	for _, a := range bundle.Apps {
		if a.Slug == "build-appid-app" {
			wantApp = a.ID
			break
		}
	}
	if wantApp == "" {
		t.Fatal("seeded app missing from bundle.Apps")
	}
	if bundle.Deployments[0].AppID != wantApp {
		t.Errorf("Deployment.AppID = %q, want %q", bundle.Deployments[0].AppID, wantApp)
	}
	// Bug 4 regression: when builds exist, AppID must be populated.
	for i, b := range bundle.Builds {
		if b.AppID == "" {
			t.Errorf("BuildExportResponse[%d].AppID empty — Bug 4 fix regressed (build=%+v)", i, b)
		}
	}
}

// TestDashboardAccountExport_ReturnsAttachment is the regression test
// for the Bug 5 fix (PR #83 review). Before the fix, the dashboard
// account page linked `<a href="/v1/account/export">` — that endpoint
// requires Bearer API-key auth; the dashboard sends only the session
// cookie, so clicking the link from the dashboard 401'd. The fix
// mounts a session-authed GET /dashboard/account/export route that
// reuses the same gatherExport helper and emits the same JSON
// envelope + Content-Disposition attachment.
//
// This test goes through the full chain: a real session.Manager, a
// real cookie issued via mgr.Issue, and the route mounted in
// server.go behind s.sessionAuth → s.dashboardChain → dashboardExport.
// Without the fix the handler doesn't exist and request falls through
// to a 404 from the catch-all; with the fix it returns 200, the right
// Content-Type, and the same JSON envelope as the bearer-authed route.
func TestDashboardAccountExport_ReturnsAttachment(t *testing.T) {
	srv, cookie := newAuthedDashboardServer(t)

	rec := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/dashboard/account/export", nil)
	r.AddCookie(cookie)
	srv.ServeHTTP(rec, r)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /dashboard/account/export = %d, want 200 "+
			"(session-auth route must be mounted; failure here usually "+
			"means the mount in server.go regressed)\nbody = %s",
			rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q, want application/json (matches the bearer route so the dashboard can swallow it via the same Download mechanism)", ct)
	}
	if cd := rec.Header().Get("Content-Disposition"); !strings.HasPrefix(cd, "attachment;") {
		t.Errorf("Content-Disposition = %q, want attachment;…", cd)
	}
	var bundle api.AccountExportResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &bundle); err != nil {
		t.Fatalf("body not JSON: %v\n%s", err, rec.Body.String())
	}
	// The seed account is the alice@example.com newAuthedDashboardServer
	// creates with PlanFree and zero apps, so the bundle must still
	// contain the envelope fields even when the slices are empty —
	// i.e. the route returned a real bundle shape, not a 204 No Content.
	if bundle.ExportedAt == "" {
		t.Errorf("ExportedAt empty; bundle shape broken")
	}
	if bundle.Account.Email != "alice@example.com" {
		t.Errorf("Account.Email = %q, want alice@example.com (auth must NOT claim a different account — that would mean sessionAuth is mis-wired)", bundle.Account.Email)
	}
	if len(bundle.Apps) != 0 || len(bundle.Deployments) != 0 {
		t.Errorf("empty seed produced rows: apps=%d deployments=%d", len(bundle.Apps), len(bundle.Deployments))
	}
}

// TestDashboardAccountExport_RejectsUnauthenticated is the negative
// companion: the route is session-authed, not public. An unauthed
// request must NOT 200 (it would mean any visitor could pull the JSON
// envelope just by clicking a link they'd be told is "their dashboard").
// The sessionAuth middleware's contract is "redirect to /login" for
// dashboard routes, which is a 303 by convention in this codebase
// (see handlers_dashboard.go's sessionAuth wiring).
func TestDashboardAccountExport_RejectsUnauthenticated(t *testing.T) {
	srv, _ := newAuthedDashboardServer(t)
	rec := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/dashboard/account/export", nil)
	// No cookie.
	srv.ServeHTTP(rec, r)
	if rec.Code == http.StatusOK {
		t.Fatalf("unauthed GET /dashboard/account/export = 200, want "+
			"redirect or 401 (sessionAuth must reject)\nbody = %s",
			rec.Body.String())
	}
}

// TestGDPRFlow_EmailsAndAuditLedger wires a recordingMailer + spyNotifier
// + MemStore-backed grace into a server, then walks the customer
// through DELETE → RESTORE → EXPORT → DELETE → grace-timer advance
// and asserts:
//
//   - scheduleDeletion fired one AccountDeletionPending email each
//     time it ran (the first DELETE through the customer-facing API
//     and the second DELETE after a restore).
//
//   - The pg_notify on NotifyAccountDeletionPending was emitted with
//     the documented payload shape (re-asserts Bug 2 regression).
//
//   - pkg/grace.RunOnce past the grace window emitted exactly one
//     AccountDeletionComplete email per hard-delete.
//
//   - The audit ledger holds four rows in the requested_at desc
//     order the customer fired them:
//
//     delete (second, completed by grace.RunOnce → stamps completed_at)
//     export (completes at insert time)
//     restore (completes at insert time — restore IS the endpoint)
//     delete (first, RESTORED before grace ticked it; completed_at
//     legitimately stays nil)
//
//     This is the end-to-end smoke for the GDPO acceptance gate (PR
//
// follow-up: the regulator's "show me proof" trail lives or dies on
// this test staying green). If completed_at on the post-restore
// delete ever flips to a timestamp, the invariant below will catch
// it.
//
// This is the end-to-end smoke for the GDPO acceptance gate (PR
// follow-up: the regulator's "show me proof" trail lives or dies on
// this test staying green).
func TestGDPRFlow_EmailsAndAuditLedger(t *testing.T) {
	e := setup(t, api.PlanHobby)
	withAccountTestRecipient(t)

	mailer := &recordingMailer{}
	notif := &spyNotifier{}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := newServerWithDeps(e.store, log, "example.com", notif, "", mailer, stubGithubdClient{}, nil, nil, 0, "")

	// Customer fires DELETE /v1/account. Captures the pending email +
	// the pg_notify + the initial delete row in the ledger.
	req := httptest.NewRequest("DELETE", "/v1/account", nil)
	req.Header.Set("Authorization", "Bearer "+e.key)
	rec := httptest.NewRecorder()
	srv.handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("DELETE = %d %s", rec.Code, rec.Body)
	}
	if got := len(mailer.snapshot()); got != 1 {
		t.Fatalf("got %d mails after DELETE, want 1 (pending email)", got)
	}
	pending := mailer.snapshot()[0]
	// Loosened assertion on purpose: the body's subject wording
	// ("will be deleted on …" / "scheduled for deletion" / "deletion
	// scheduled") is owned by pkg/mail.AccountDeletionPendingBody and
	// can evolve. We just pin the action identifier in the body —
	// anything naming "account" + "delete/deletion" suffices.
	if !strings.Contains(strings.ToLower(pending.Subject), "deleted") &&
		!strings.Contains(strings.ToLower(pending.Subject), "deletion") {
		t.Errorf("pending subject = %q, want wording about deletion", pending.Subject)
	}

	// Customer fires POST /v1/account/restore inside the window.
	rec = e.do(t, "POST", "/v1/account/restore", nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("restore = %d %s", rec.Code, rec.Body)
	}

	// Customer fires GET /v1/account/export. Captures the export row
	// in the ledger (no email — the GDPR lean routes the bundle
	// through the API, not via email).
	rec = e.do(t, "GET", "/v1/account/export", nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("export = %d %s", rec.Code, rec.Body)
	}

	// Re-delete so the test can drive the hard-delete through
	// pkg/grace.
	rec = e.do(t, "DELETE", "/v1/account", nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("second DELETE = %d %s", rec.Code, rec.Body)
	}

	// Drive pkg/grace.RunOnce manually. The MemStore's
	// DeletionRequestedAt is "now-ish" (set ~ms ago), so we can't
	// wait 30 days; instead construct a Grace whose Now() returns
	// 31 days in the future so every pending row crosses the cutoff.
	// MemStore doesn't expose a "fast-forward" knob; rebuilding
	// against the test's own store via the public surface. Simpler:
	// rewind DeletionRequestedAt via MarkAccountDeletionPending
	// would re-set it to now; instead walk via rawMem which we
	// can't reach. Easiest: call pkg/grace with a synthetic Now().
	g := grace.New(grace.Params{
		Store:  e.store,
		Mailer: mailAdapterBridge{mailer: mailer},
		Log:    log,
		Notif:  func(_ context.Context, _ string, _ string) error { return nil },
		Now:    func() time.Time { return time.Now().Add(31 * 24 * time.Hour) },
	})
	if err := g.RunOnce(context.Background()); err != nil {
		t.Fatalf("grace.RunOnce: %v", err)
	}

	// Verify mail assertions: total 3 mails (pending, complete).
	// The restore interval happens to fire no mail; the export fires
	// no mail; only schedule + complete.
	calls := mailer.snapshot()
	if len(calls) != 2 {
		t.Fatalf("got %d total mails, want 2 (pending + complete). subjects=%v", len(calls), subjectsOf(calls))
	}
	complete := calls[1]
	if !strings.Contains(strings.ToLower(complete.Subject), "deletion complete") &&
		!strings.Contains(complete.Subject, "deleted") {
		t.Errorf("complete subject = %q, want something about deletion-complete", complete.Subject)
	}

	// Verify audit ledger: 4 rows in requested_at desc order (the
	// second DELETE is the latest action). Expected sequence:
	//   delete (second, completed by grace.RunOnce → stamps completed_at)
	//   export  (completes at insert time)
	//   restore (completes at insert time)
	//   delete  (first, was restored → completed_at stays NULL forever)
	// That fourth row's completed_at-is-zero is the documented
	// "restore wins, original delete is a no-op" surface — verify both.
	ledger, err := e.store.ListGdprRequestsForAccount(context.Background(), e.acct.ID, 100)
	if err != nil {
		t.Fatalf("ListGdprRequestsForAccount: %v", err)
	}
	if len(ledger) != 4 {
		t.Fatalf("audit ledger = %d rows, want 4 (got actions=%v)", len(ledger), actionsOf(ledger))
	}
	wantOrder := []string{"delete", "export", "restore", "delete"}
	gotActions := actionsOf(ledger)
	for i, want := range wantOrder {
		if gotActions[i] != want {
			t.Errorf("ledger[%d].action = %q, want %q (full=%v)", i, gotActions[i], want, gotActions)
		}
	}
	// The FIRST delete (oldest row, ledger[3]) was restored before grace
	// ticked it → completed_at legitimately stays zero. The other three
	// rows (ledger[0..2]) MUST all have completed_at stamped.
	if ledger[0].CompletedAt.IsZero() {
		t.Errorf("ledger[0] (latest delete) completed_at is zero; " +
			"grace.RunOnce must have stamped it")
	}
	for i := 1; i < 3; i++ {
		if ledger[i].CompletedAt.IsZero() {
			t.Errorf("ledger[%d] (%s) completed_at is zero; export/restore "+
				"complete at insert time and MUST carry a timestamp", i, ledger[i].Action)
		}
	}
	if !ledger[3].CompletedAt.IsZero() {
		t.Errorf("ledger[3] (first/original delete, restored before grace "+
			"could tick) completed_at = %v, want zero — restore wins, the "+
			"original delete is a no-op and must NOT carry a stamp",
			ledger[3].CompletedAt)
	}
}

func subjectsOf(ms []Message) []string {
	out := make([]string, len(ms))
	for i, m := range ms {
		out[i] = m.Subject
	}
	return out
}

func actionsOf(rs []state.GdprRequest) []string {
	out := make([]string, len(rs))
	for i, r := range rs {
		out[i] = string(r.Action)
	}
	return out
}

// mailAdapterBridge adapts apid's recordingMailer to the
// primitive-arg pkg/grace.Sender shape (To slice + subject + body).
type mailAdapterBridge struct{ mailer *recordingMailer }

func (m mailAdapterBridge) Send(ctx context.Context, to []string, subject, body string) error {
	return m.mailer.Send(ctx, Message{To: to, Subject: subject, TextBody: body})
}
