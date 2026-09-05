// handlers_admin_force_test.go — pins the contract of the P2
// (force-park + force-cold-boot) admin recovery handlers added
// in Commit P2.3 of the operator-side observability mega-PR
// (PR #1099) AND the P2d (force-restart) follow-on in PR #1105.
//
// The redesign (post-depguard R6 fix) routes P2 through an
// operator_intents table + pg_notify seam — apid is no longer
// the entity calling schedd over gRPC. The handler writes an
// intent row, fires a pg_notify, stamps an audit row with
// result="enqueued", and returns 202 Accepted. Schedd is the
// only consumer.
//
// The table-driven cases below cover the seven load-bearing
// edges for force-park:
//
//  1. confirm-required tripwire — without ?confirm=true the
//     handler returns 400 validation_failed (no intent insert,
//     no notify, no audit row).
//  2. reason shape — non-[a-z0-9_] chars or >64 chars returns
//     400 (no intent insert).
//  3. instance uuid — malformed UUID returns 404 with no insert.
//  4. state gate — instance state ∉ {RUNNING, WAKING,
//     COLD_BOOTING} returns 409 instance_not_parkable WITHOUT
//     writing an intent row. Audit row stamped with
//     result="rejected" so the operator's "I checked" is durable.
//  5. store error — InsertOperatorIntent returns error → 500.
//  6. happy path — handler inserts an intent row, fires notify,
//     returns 202 Accepted with intent_id + status_url.
//  7. notify failure is logged but does NOT 5xx — the intent
//     row is the source of truth; the 30s safety tick reclaims
//     any notify-dropped row (same precedent as cron_run_now).
//
// Plus five for force-cold-boot:
//  1. confirm-required tripwire (400).
//  2. reason shape (400).
//  3. unknown slug returns 404 with no insert.
//  4. happy path inserts an intent row + emits notify + 202.
//  5. store error returns 500.
//
// The fake fakeStoreForIntent (defined below) captures the
// intent-insert call args so each test can assert (a) that
// the handler inserted the expected row, (b) fired the
// expected notify, and (c) did NOT insert when pre-conditions
// were not met. The intent itself is a real MemStore row;
// the fake only exists to record call args + inject errors.
//
// newForceHarness is also used by handlers_admin_sweep_builds_test.go
// (P2c) — that handler does not touch the intent path, so the
// sweep-builds tests pass nil for the fake. Keeping the
// signature `newForceHarness(t, *fakeStoreForIntent)` preserves
// the build for both files.
//
// Tests for the gregalectl CLI wrapper (commands_instances.go)
// live in cmd/gregalectl/commands_instances_test.go.

package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/wire"
)

// intentInsertCall captures the args to one Store.InsertOperatorIntent
// invocation. The handler is supposed to insert exactly one row per
// successful request; tests assert insertCalls length is 0 on
// rejected paths and 1 on happy paths.
type intentInsertCall struct {
	Kind    state.OperatorIntentKind
	Target  string
	Account *string
	Actor   string
	Reason  string
	// TraceID is the OTel W3C trace_id passed by the handler.
	// Captured so PR-#TBD / fix-cluster B's middleware.TraceIDFrom
	// wiring can be pinned end-to-end (header → ctx → InsertOperatorIntent
	// → operator_intents.trace_id column).
	TraceID *string
}

// fakeStoreForIntent wraps the MemStore so the test can observe
// InsertOperatorIntent calls and inject errors. The wrapper
// embeds state.Store so all Store methods auto-delegate to the
// inner MemStore; only the 5 operator-intent methods are
// overridden (InsertOperatorIntent records the call + delegates
// or returns the injected error; the other 4 fall through to
// the inner store's real impls).
//
// This is the same pattern as fakeBillingProvider at
// handlers_admin_billing_test.go but bounded to the operator-
// intent surface so the test can inject errors without standing
// up a fake for every Store method.
type fakeStoreForIntent struct {
	state.Store

	inner *state.MemStore

	insertCalls []intentInsertCall

	insertErr error

	// nextIntentID, if non-empty, overrides the auto-generated
	// UUID returned from InsertOperatorIntent (lets a test pin
	// a deterministic status_url for body assertions).
	nextIntentID string
}

// InsertOperatorIntent implements state.Store. It records the
// call args + delegates to the inner MemStore (or returns
// insertErr / nextIntentID as the fake dictates).
func (f *fakeStoreForIntent) InsertOperatorIntent(
	ctx context.Context,
	kind state.OperatorIntentKind,
	targetID string,
	accountID *string,
	actorID, reason string,
	metadata json.RawMessage,
	traceID *string,
) (string, error) {
	f.insertCalls = append(f.insertCalls, intentInsertCall{
		Kind:    kind,
		Target:  targetID,
		Account: accountID,
		Actor:   actorID,
		Reason:  reason,
		TraceID: traceID,
	})
	if f.insertErr != nil {
		return "", f.insertErr
	}
	if f.nextIntentID != "" {
		return f.nextIntentID, nil
	}
	return f.inner.InsertOperatorIntent(ctx, kind, targetID, accountID, actorID, reason, metadata, traceID)
}

// newForceHarness wires a server with a MemStore + an optional
// fakeStoreForIntent. The admin allowlist is set to the caller's
// email so adminAllowlist (compute_nodes.go:74-86) passes —
// without it the request would 403 before reaching the handler.
// The harness mints a verified operator session with a fresh
// step-up stamp. The notifier is overridden
// with the existing captureNotifier (handlers_traffic_notify_test.go)
// when fake != nil so tests can assert Notify calls; otherwise
// the default noopNotifier is used (covers sweep-builds tests
// which do not emit any intent).
//
// When fake != nil the server's store is the wrapper (which
// delegates everything except operator-intent ops to the inner
// MemStore). Tests interact with the wrapper via srv.store()
// (a small accessor returning the active Store) or by reading
// fake.insertCalls directly. The inner MemStore is still exposed
// via the returned *state.MemStore so seedRunningInstance /
// seedAppAndDeployment can call store methods like
// CreateAccount / CreateApp that the wrapper delegates.
func newForceHarness(t *testing.T, fake *fakeStoreForIntent) (*server, *state.MemStore, *http.Cookie) {
	t.Helper()
	store := state.NewMemStore()
	acct, err := store.CreateAccount(context.Background(), "ops@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	ops := wire.NewOpsMetrics("apid_force_test")
	var st state.Store = store
	if fake != nil {
		fake.Store = store
		fake.inner = store
		st = fake
	}
	srv := newServer(st, slog.New(slog.NewTextHandler(io.Discard, nil)), "gregale.dev", noopNotifier{}).
		WithOpsMetrics(context.Background(), ops).
		WithAdminAllowlist("ops@example.com")
	if fake != nil {
		srv.notif = &captureNotifier{}
	}
	sid := uuid.NewString()
	if _, err := store.CreateSession(context.Background(), sid, acct.ID, "192.0.2.10", "force-test-ua"); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	token, err := srv.sessions.IssueWithSessionAndBindingHashAndStepUp(sid, acct.ID, "", time.Now(), false)
	if err != nil {
		t.Fatalf("IssueWithSessionAndBindingHashAndStepUp: %v", err)
	}
	return srv, store, &http.Cookie{Name: sessionCookie, Value: token}
}

// notifyCountByChannel returns the number of captured Notify
// calls on the given channel. Mirrors the existing
// captureNotifier.byChannel helper but is package-local so the
// test can read it without re-importing the alias.
func notifyCountByChannel(srv *server) int {
	cap, ok := srv.notif.(*captureNotifier)
	if !ok {
		return -1
	}
	return len(cap.byChannel(db.NotifyOperatorIntent))
}

// seedRunningInstance inserts an app + deployment + instance row
// triple into the MemStore and returns the (instance id, app id)
// tuple. The instance's state defaults to "RUNNING"; callers can
// override via stateStr. The MemStore's CreateInstance takes
// positional args (mirrors the pgstore's INSERT), so this helper
// stays shape-compatible.
func seedRunningInstance(t *testing.T, store *state.MemStore, stateStr string) (string, string) {
	t.Helper()
	tenant, err := store.CreateAccount(context.Background(), "tenant@example.com", api.PlanHobby)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(context.Background(), state.App{
		AccountID: tenant.ID,
		Slug:      "tenant-app",
		RAMMB:     128,
		Runtime:   "node22",
		Type:      state.AppTypeFunction,
	})
	if err != nil {
		t.Fatal(err)
	}
	dep, err := store.CreateDeployment(context.Background(), state.Deployment{
		AppID:       app.ID,
		ImageDigest: "deadbeef",
	})
	if err != nil {
		t.Fatal(err)
	}
	ins, err := store.CreateInstance(context.Background(), app.ID, dep.ID, stateStr, 128, "", "")
	if err != nil {
		t.Fatal(err)
	}
	return ins.ID, app.ID
}

// seedAppAndDeployment inserts an app + deployment row pair into
// the MemStore (no instance) and returns (app_id, deployment_id,
// account_id). Used by the force-cold-boot tests, which resolve
// the slug → latest deployment rather than acting on an instance.
func seedAppAndDeployment(t *testing.T, store *state.MemStore) (string, string, string) {
	t.Helper()
	tenant, err := store.CreateAccount(context.Background(), "tenant@example.com", api.PlanHobby)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(context.Background(), state.App{
		AccountID: tenant.ID,
		Slug:      "tenant-app",
		RAMMB:     128,
		Runtime:   "node22",
		Type:      state.AppTypeFunction,
	})
	if err != nil {
		t.Fatal(err)
	}
	dep, err := store.CreateDeployment(context.Background(), state.Deployment{
		AppID:       app.ID,
		ImageDigest: "deadbeef",
	})
	if err != nil {
		t.Fatal(err)
	}
	return app.ID, dep.ID, tenant.ID
}

// TestPostForcePark_TableDriven pins the seven edges of the
// force-park handler.
func TestPostForcePark_TableDriven(t *testing.T) {
	t.Run("missing_confirm_returns_400", func(t *testing.T) {
		fake := &fakeStoreForIntent{}
		srv, store, cookie := newForceHarness(t, fake)
		insID, _ := seedRunningInstance(t, store, "RUNNING")

		req := httptest.NewRequest(http.MethodPost,
			"/v1/admin/instances/"+insID+"/force-park", nil)
		req.AddCookie(cookie)
		req.Header.Set("Idempotency-Key", "test-admin-mutation")
		rec := httptest.NewRecorder()
		srv.handler().ServeHTTP(rec, req)

		if rec.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400; body=%s", rec.Code, rec.Body.String())
		}
		if len(fake.insertCalls) != 0 {
			t.Errorf("intent should not have been inserted; got %d calls", len(fake.insertCalls))
		}
		if n := notifyCountByChannel(srv); n != 0 {
			t.Errorf("notify should not have been emitted; got %d calls", n)
		}
	})

	t.Run("invalid_reason_returns_400", func(t *testing.T) {
		fake := &fakeStoreForIntent{}
		srv, store, cookie := newForceHarness(t, fake)
		insID, _ := seedRunningInstance(t, store, "RUNNING")

		// Space + punctuation are not in [a-z0-9_]; handler must 400.
		req := httptest.NewRequest(http.MethodPost,
			"/v1/admin/instances/"+insID+"/force-park?confirm=true&reason=has%20space", nil)
		req.AddCookie(cookie)
		req.Header.Set("Idempotency-Key", "test-admin-mutation")
		rec := httptest.NewRecorder()
		srv.handler().ServeHTTP(rec, req)

		if rec.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400; body=%s", rec.Code, rec.Body.String())
		}
		if len(fake.insertCalls) != 0 {
			t.Errorf("intent should not have been inserted on bad reason; got %d calls", len(fake.insertCalls))
		}
	})

	t.Run("invalid_uuid_returns_404", func(t *testing.T) {
		fake := &fakeStoreForIntent{}
		srv, _, cookie := newForceHarness(t, fake)

		req := httptest.NewRequest(http.MethodPost,
			"/v1/admin/instances/not-a-uuid/force-park?confirm=true", nil)
		req.AddCookie(cookie)
		req.Header.Set("Idempotency-Key", "test-admin-mutation")
		rec := httptest.NewRecorder()
		srv.handler().ServeHTTP(rec, req)

		if rec.Code != http.StatusNotFound {
			t.Fatalf("status = %d, want 404; body=%s", rec.Code, rec.Body.String())
		}
		if len(fake.insertCalls) != 0 {
			t.Errorf("intent should not have been inserted on bad uuid; got %d calls", len(fake.insertCalls))
		}
	})

	t.Run("nil_instance_returns_404", func(t *testing.T) {
		// The MemStore has no instance at this uuid — gate-time
		// read fails, handler returns 404 with no intent insert.
		fake := &fakeStoreForIntent{}
		srv, _, cookie := newForceHarness(t, fake)

		req := httptest.NewRequest(http.MethodPost,
			"/v1/admin/instances/00000000-0000-0000-0000-000000000000/force-park?confirm=true", nil)
		req.AddCookie(cookie)
		req.Header.Set("Idempotency-Key", "test-admin-mutation")
		rec := httptest.NewRecorder()
		srv.handler().ServeHTTP(rec, req)

		if rec.Code != http.StatusNotFound {
			t.Fatalf("status = %d, want 404; body=%s", rec.Code, rec.Body.String())
		}
		if len(fake.insertCalls) != 0 {
			t.Errorf("intent should not have been inserted on missing instance; got %d calls", len(fake.insertCalls))
		}
	})

	t.Run("parked_state_returns_409_no_intent", func(t *testing.T) {
		fake := &fakeStoreForIntent{}
		srv, store, cookie := newForceHarness(t, fake)
		insID, _ := seedRunningInstance(t, store, "PARKED")

		req := httptest.NewRequest(http.MethodPost,
			"/v1/admin/instances/"+insID+"/force-park?confirm=true&reason=already_parked", nil)
		req.AddCookie(cookie)
		req.Header.Set("Idempotency-Key", "test-admin-mutation")
		rec := httptest.NewRecorder()
		srv.handler().ServeHTTP(rec, req)

		if rec.Code != http.StatusConflict {
			t.Fatalf("status = %d, want 409; body=%s", rec.Code, rec.Body.String())
		}
		var prob api.Problem
		if err := json.Unmarshal(rec.Body.Bytes(), &prob); err != nil {
			t.Fatalf("decode problem: %v body=%s", err, rec.Body.String())
		}
		if prob.Code != "instance_not_parkable" {
			t.Errorf("code = %q, want instance_not_parkable", prob.Code)
		}
		if len(fake.insertCalls) != 0 {
			t.Errorf("intent should not have been inserted on 409; got %d calls", len(fake.insertCalls))
		}
	})

	t.Run("store_returns_error_returns_500", func(t *testing.T) {
		fake := &fakeStoreForIntent{insertErr: errors.New("connection refused")}
		srv, store, cookie := newForceHarness(t, fake)
		insID, _ := seedRunningInstance(t, store, "RUNNING")

		req := httptest.NewRequest(http.MethodPost,
			"/v1/admin/instances/"+insID+"/force-park?confirm=true", nil)
		req.AddCookie(cookie)
		req.Header.Set("Idempotency-Key", "test-admin-mutation")
		rec := httptest.NewRecorder()
		srv.handler().ServeHTTP(rec, req)

		if rec.Code != http.StatusInternalServerError {
			t.Fatalf("status = %d, want 500; body=%s", rec.Code, rec.Body.String())
		}
		var prob api.Problem
		if err := json.Unmarshal(rec.Body.Bytes(), &prob); err != nil {
			t.Fatalf("decode problem: %v body=%s", err, rec.Body.String())
		}
		if prob.Code != "internal_error" {
			t.Errorf("code = %q, want internal_error", prob.Code)
		}
	})

	t.Run("happy_path_inserts_intent_returns_202", func(t *testing.T) {
		fake := &fakeStoreForIntent{nextIntentID: "11111111-1111-1111-1111-111111111111"}
		srv, store, cookie := newForceHarness(t, fake)
		insID, _ := seedRunningInstance(t, store, "RUNNING")

		req := httptest.NewRequest(http.MethodPost,
			"/v1/admin/instances/"+insID+"/force-park?confirm=true&reason=incident_42", nil)
		req.AddCookie(cookie)
		req.Header.Set("Idempotency-Key", "test-admin-mutation")
		rec := httptest.NewRecorder()
		srv.handler().ServeHTTP(rec, req)

		if rec.Code != http.StatusAccepted {
			t.Fatalf("status = %d, want 202; body=%s", rec.Code, rec.Body.String())
		}
		if len(fake.insertCalls) != 1 {
			t.Fatalf("intent insert calls = %d, want 1", len(fake.insertCalls))
		}
		if fake.insertCalls[0].Kind != state.OperatorIntentKindForcePark {
			t.Errorf("intent kind = %q, want force_park", fake.insertCalls[0].Kind)
		}
		if fake.insertCalls[0].Target != insID {
			t.Errorf("intent target = %q, want %q", fake.insertCalls[0].Target, insID)
		}
		if fake.insertCalls[0].Reason != "incident_42" {
			t.Errorf("intent reason = %q, want incident_42", fake.insertCalls[0].Reason)
		}
		if n := notifyCountByChannel(srv); n != 1 {
			t.Fatalf("notify calls on operator_intent channel = %d, want 1", n)
		}
		var body map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("decode body: %v body=%s", err, rec.Body.String())
		}
		if body["ok"] != true {
			t.Errorf("body.ok = %v, want true", body["ok"])
		}
		if body["intent_id"] != "11111111-1111-1111-1111-111111111111" {
			t.Errorf("body.intent_id = %v, want 11111111-...", body["intent_id"])
		}
		if body["status_url"] != "/v1/admin/operator-intents/11111111-1111-1111-1111-111111111111" {
			t.Errorf("body.status_url = %v", body["status_url"])
		}
		if body["previous_state"] != "RUNNING" {
			t.Errorf("body.previous_state = %v, want RUNNING", body["previous_state"])
		}
		if body["kind"] != "force_park" {
			t.Errorf("body.kind = %v, want force_park", body["kind"])
		}
	})

	t.Run("notify_failure_returns_202_with_intent_id", func(t *testing.T) {
		// pg_notify drop is non-fatal: the intent row is durable,
		// the 30s safety tick reclaims it, the response is still
		// 202 Accepted. Same precedent as handlers_cron_run.go.
		// We swap in a failingNotifier that returns the drop error
		// so we can pin the "logged but not surfaced" semantics.
		fake := &fakeStoreForIntent{nextIntentID: "22222222-2222-2222-2222-222222222222"}
		srv, store, cookie := newForceHarness(t, fake)
		srv.notif = &failingNotifier{err: errors.New("pg notify dropped")}
		insID, _ := seedRunningInstance(t, store, "RUNNING")

		req := httptest.NewRequest(http.MethodPost,
			"/v1/admin/instances/"+insID+"/force-park?confirm=true", nil)
		req.AddCookie(cookie)
		req.Header.Set("Idempotency-Key", "test-admin-mutation")
		rec := httptest.NewRecorder()
		srv.handler().ServeHTTP(rec, req)

		if rec.Code != http.StatusAccepted {
			t.Fatalf("status = %d, want 202; body=%s", rec.Code, rec.Body.String())
		}
		if len(fake.insertCalls) != 1 {
			t.Fatalf("intent insert calls = %d, want 1", len(fake.insertCalls))
		}
		var body map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("decode body: %v body=%s", err, rec.Body.String())
		}
		if body["intent_id"] != "22222222-2222-2222-2222-222222222222" {
			t.Errorf("body.intent_id = %v, want 22222222-...", body["intent_id"])
		}
	})
}

// TestPostForceColdBoot_TableDriven pins the four edges of the
// force-cold-boot handler. The cold-boot path has no "not-
// eligible" state to gate on (an already-stale deployment is a
// no-op success at engine time, not a rejection), so there is
// no parked_state_returns_409 case — only the four contract
// edges.
func TestPostForceColdBoot_TableDriven(t *testing.T) {
	t.Run("missing_confirm_returns_400", func(t *testing.T) {
		fake := &fakeStoreForIntent{}
		srv, _, cookie := newForceHarness(t, fake)

		req := httptest.NewRequest(http.MethodPost,
			"/v1/admin/apps/tenant-app/force-cold-boot", nil)
		req.AddCookie(cookie)
		req.Header.Set("Idempotency-Key", "test-admin-mutation")
		rec := httptest.NewRecorder()
		srv.handler().ServeHTTP(rec, req)

		if rec.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400; body=%s", rec.Code, rec.Body.String())
		}
		if len(fake.insertCalls) != 0 {
			t.Errorf("intent should not have been inserted; got %d calls", len(fake.insertCalls))
		}
	})

	t.Run("invalid_reason_returns_400", func(t *testing.T) {
		fake := &fakeStoreForIntent{}
		srv, _, cookie := newForceHarness(t, fake)

		req := httptest.NewRequest(http.MethodPost,
			"/v1/admin/apps/tenant-app/force-cold-boot?confirm=true&reason=has%20space", nil)
		req.AddCookie(cookie)
		req.Header.Set("Idempotency-Key", "test-admin-mutation")
		rec := httptest.NewRecorder()
		srv.handler().ServeHTTP(rec, req)

		if rec.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400; body=%s", rec.Code, rec.Body.String())
		}
		if len(fake.insertCalls) != 0 {
			t.Errorf("intent should not have been inserted on bad reason; got %d calls", len(fake.insertCalls))
		}
	})

	t.Run("unknown_slug_returns_404", func(t *testing.T) {
		fake := &fakeStoreForIntent{}
		srv, _, cookie := newForceHarness(t, fake)

		req := httptest.NewRequest(http.MethodPost,
			"/v1/admin/apps/does-not-exist/force-cold-boot?confirm=true", nil)
		req.AddCookie(cookie)
		req.Header.Set("Idempotency-Key", "test-admin-mutation")
		rec := httptest.NewRecorder()
		srv.handler().ServeHTTP(rec, req)

		if rec.Code != http.StatusNotFound {
			t.Fatalf("status = %d, want 404; body=%s", rec.Code, rec.Body.String())
		}
		if len(fake.insertCalls) != 0 {
			t.Errorf("intent should not have been inserted on 404; got %d calls", len(fake.insertCalls))
		}
	})

	t.Run("happy_path_inserts_intent_returns_202", func(t *testing.T) {
		fake := &fakeStoreForIntent{nextIntentID: "33333333-3333-3333-3333-333333333333"}
		srv, store, cookie := newForceHarness(t, fake)
		appID, depID, _ := seedAppAndDeployment(t, store)

		req := httptest.NewRequest(http.MethodPost,
			"/v1/admin/apps/tenant-app/force-cold-boot?confirm=true&reason=incident_42", nil)
		req.AddCookie(cookie)
		req.Header.Set("Idempotency-Key", "test-admin-mutation")
		rec := httptest.NewRecorder()
		srv.handler().ServeHTTP(rec, req)

		if rec.Code != http.StatusAccepted {
			t.Fatalf("status = %d, want 202; body=%s", rec.Code, rec.Body.String())
		}
		if len(fake.insertCalls) != 1 {
			t.Fatalf("intent insert calls = %d, want 1", len(fake.insertCalls))
		}
		if fake.insertCalls[0].Kind != state.OperatorIntentKindForceColdBoot {
			t.Errorf("intent kind = %q, want force_cold_boot", fake.insertCalls[0].Kind)
		}
		if fake.insertCalls[0].Target != depID {
			t.Errorf("intent target = %q, want %q (deployment_id)", fake.insertCalls[0].Target, depID)
		}
		if fake.insertCalls[0].Reason != "incident_42" {
			t.Errorf("intent reason = %q, want incident_42", fake.insertCalls[0].Reason)
		}
		if n := notifyCountByChannel(srv); n != 1 {
			t.Fatalf("notify calls on operator_intent channel = %d, want 1", n)
		}
		var body map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("decode body: %v body=%s", err, rec.Body.String())
		}
		if body["ok"] != true {
			t.Errorf("body.ok = %v, want true", body["ok"])
		}
		if body["intent_id"] != "33333333-3333-3333-3333-333333333333" {
			t.Errorf("body.intent_id = %v, want 33333333-...", body["intent_id"])
		}
		if body["kind"] != "force_cold_boot" {
			t.Errorf("body.kind = %v, want force_cold_boot", body["kind"])
		}
		if body["app_id"] != appID {
			t.Errorf("body.app_id = %v, want %q", body["app_id"], appID)
		}
		if body["deployment_id"] != depID {
			t.Errorf("body.deployment_id = %v, want %q", body["deployment_id"], depID)
		}
		// snap_ids_marked_stale is NOT populated at enqueue time
		// (the engine walk hasn't happened yet); the operator
		// learns via GET /v1/admin/operator-intents/{id}.
		if _, present := body["snap_ids_marked_stale"]; present {
			t.Errorf("body.snap_ids_marked_stale should not be present at enqueue time; got %v", body["snap_ids_marked_stale"])
		}
	})

	t.Run("store_returns_error_returns_500", func(t *testing.T) {
		fake := &fakeStoreForIntent{insertErr: errors.New("connection refused")}
		srv, store, cookie := newForceHarness(t, fake)
		_, _, _ = seedAppAndDeployment(t, store)

		req := httptest.NewRequest(http.MethodPost,
			"/v1/admin/apps/tenant-app/force-cold-boot?confirm=true", nil)
		req.AddCookie(cookie)
		req.Header.Set("Idempotency-Key", "test-admin-mutation")
		rec := httptest.NewRecorder()
		srv.handler().ServeHTTP(rec, req)

		if rec.Code != http.StatusInternalServerError {
			t.Fatalf("status = %d, want 500; body=%s", rec.Code, rec.Body.String())
		}
	})
}

// failingNotifier is a db.Notifier that returns a canned error
// from every Notify call. Used by the notify_failure_returns_202
// case to confirm that pg_notify drops are non-fatal (logged but
// not surfaced) — the intent row is the source of truth and the
// 30s safety tick reclaims any notify-dropped row.
//
// Subscribe + WaitFor are no-ops; the handler under test only
// calls Notify on this path.
type failingNotifier struct {
	err error
}

func (f *failingNotifier) Notify(_ context.Context, _, _ string) error {
	return f.err
}
func (f *failingNotifier) Subscribe(_ context.Context, _ []string) (<-chan db.Notification, func(), error) {
	return nil, func() {}, nil
}
func (f *failingNotifier) WaitFor(_ context.Context, _ string, _ func(payload string) bool, _ time.Duration) (string, error) {
	return "", db.ErrWaitTimeout
}

// TestPostForceRestart_TableDriven pins the seven edges of the
// force-restart handler (P2d follow-on to PR #1099). The
// state-gate set is identical to force-park ({RUNNING, WAKING,
// COLD_BOOTING}) — only the audit kind and 409 error code
// differ ("operator.action.restart_instance" + "restart_instance
// .outcome" vs "operator.action.park_instance" + "park_instance
// .outcome"; "instance_not_restartable" vs "instance_not_parkable").
//
//  1. confirm-required tripwire — without ?confirm=true the
//     handler returns 400 validation_failed (no intent insert,
//     no notify, no audit row).
//  2. reason shape — non-[a-z0-9_] chars or >64 chars returns
//     400 (no intent insert).
//  3. instance uuid — malformed UUID returns 404 with no insert.
//  4. state gate — instance state ∉ {RUNNING, WAKING,
//     COLD_BOOTING} returns 409 instance_not_restartable
//     WITHOUT writing an intent row. Audit row stamped with
//     result="rejected" so the operator's "I checked" is durable.
//  5. store error — InsertOperatorIntent returns error → 500.
//  6. happy path — handler inserts an intent row, fires notify,
//     returns 202 Accepted with intent_id + status_url.
//  7. notify failure is logged but does NOT 5xx — the intent
//     row is the source of truth; the 30s safety tick reclaims
//     any notify-dropped row (same precedent as cron_run_now).
func TestPostForceRestart_TableDriven(t *testing.T) {
	t.Run("missing_confirm_returns_400", func(t *testing.T) {
		fake := &fakeStoreForIntent{}
		srv, store, cookie := newForceHarness(t, fake)
		insID, _ := seedRunningInstance(t, store, "RUNNING")

		req := httptest.NewRequest(http.MethodPost,
			"/v1/admin/instances/"+insID+"/force-restart", nil)
		req.AddCookie(cookie)
		req.Header.Set("Idempotency-Key", "test-admin-mutation")
		rec := httptest.NewRecorder()
		srv.handler().ServeHTTP(rec, req)

		if rec.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400; body=%s", rec.Code, rec.Body.String())
		}
		if len(fake.insertCalls) != 0 {
			t.Errorf("intent should not have been inserted; got %d calls", len(fake.insertCalls))
		}
		if n := notifyCountByChannel(srv); n != 0 {
			t.Errorf("notify should not have been emitted; got %d calls", n)
		}
	})

	t.Run("invalid_reason_returns_400", func(t *testing.T) {
		fake := &fakeStoreForIntent{}
		srv, store, cookie := newForceHarness(t, fake)
		insID, _ := seedRunningInstance(t, store, "RUNNING")

		// Space + punctuation are not in [a-z0-9_]; handler must 400.
		req := httptest.NewRequest(http.MethodPost,
			"/v1/admin/instances/"+insID+"/force-restart?confirm=true&reason=has%20space", nil)
		req.AddCookie(cookie)
		req.Header.Set("Idempotency-Key", "test-admin-mutation")
		rec := httptest.NewRecorder()
		srv.handler().ServeHTTP(rec, req)

		if rec.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400; body=%s", rec.Code, rec.Body.String())
		}
		if len(fake.insertCalls) != 0 {
			t.Errorf("intent should not have been inserted on bad reason; got %d calls", len(fake.insertCalls))
		}
	})

	t.Run("invalid_uuid_returns_404", func(t *testing.T) {
		fake := &fakeStoreForIntent{}
		srv, _, cookie := newForceHarness(t, fake)

		req := httptest.NewRequest(http.MethodPost,
			"/v1/admin/instances/not-a-uuid/force-restart?confirm=true", nil)
		req.AddCookie(cookie)
		req.Header.Set("Idempotency-Key", "test-admin-mutation")
		rec := httptest.NewRecorder()
		srv.handler().ServeHTTP(rec, req)

		if rec.Code != http.StatusNotFound {
			t.Fatalf("status = %d, want 404; body=%s", rec.Code, rec.Body.String())
		}
		if len(fake.insertCalls) != 0 {
			t.Errorf("intent should not have been inserted on bad uuid; got %d calls", len(fake.insertCalls))
		}
	})

	t.Run("nil_instance_returns_404", func(t *testing.T) {
		// The MemStore has no instance at this uuid — gate-time
		// read fails, handler returns 404 with no intent insert.
		fake := &fakeStoreForIntent{}
		srv, _, cookie := newForceHarness(t, fake)

		req := httptest.NewRequest(http.MethodPost,
			"/v1/admin/instances/00000000-0000-0000-0000-000000000000/force-restart?confirm=true", nil)
		req.AddCookie(cookie)
		req.Header.Set("Idempotency-Key", "test-admin-mutation")
		rec := httptest.NewRecorder()
		srv.handler().ServeHTTP(rec, req)

		if rec.Code != http.StatusNotFound {
			t.Fatalf("status = %d, want 404; body=%s", rec.Code, rec.Body.String())
		}
		if len(fake.insertCalls) != 0 {
			t.Errorf("intent should not have been inserted on missing instance; got %d calls", len(fake.insertCalls))
		}
	})

	t.Run("parked_state_returns_409_no_intent", func(t *testing.T) {
		fake := &fakeStoreForIntent{}
		srv, store, cookie := newForceHarness(t, fake)
		insID, _ := seedRunningInstance(t, store, "PARKED")

		req := httptest.NewRequest(http.MethodPost,
			"/v1/admin/instances/"+insID+"/force-restart?confirm=true&reason=already_parked", nil)
		req.AddCookie(cookie)
		req.Header.Set("Idempotency-Key", "test-admin-mutation")
		rec := httptest.NewRecorder()
		srv.handler().ServeHTTP(rec, req)

		if rec.Code != http.StatusConflict {
			t.Fatalf("status = %d, want 409; body=%s", rec.Code, rec.Body.String())
		}
		var prob api.Problem
		if err := json.Unmarshal(rec.Body.Bytes(), &prob); err != nil {
			t.Fatalf("decode problem: %v body=%s", err, rec.Body.String())
		}
		if prob.Code != "instance_not_restartable" {
			t.Errorf("code = %q, want instance_not_restartable", prob.Code)
		}
		if len(fake.insertCalls) != 0 {
			t.Errorf("intent should not have been inserted on 409; got %d calls", len(fake.insertCalls))
		}
	})

	t.Run("waking_state_returns_409_no_intent", func(t *testing.T) {
		// P2d apid gate is intentionally TIGHTER than force-park's
		// ({RUNNING, WAKING, COLD_BOOTING}); the engine's state-
		// machine validation at engine.go:5299 rejects non-RUNNING
		// states, so accepting them at the gate would yield a
		// misleading 202-then-fail. WAKING instances get 409
		// instance_not_restartable with NO intent row written.
		fake := &fakeStoreForIntent{}
		srv, store, cookie := newForceHarness(t, fake)
		insID, _ := seedRunningInstance(t, store, "WAKING")

		req := httptest.NewRequest(http.MethodPost,
			"/v1/admin/instances/"+insID+"/force-restart?confirm=true", nil)
		req.AddCookie(cookie)
		req.Header.Set("Idempotency-Key", "test-admin-mutation")
		rec := httptest.NewRecorder()
		srv.handler().ServeHTTP(rec, req)

		if rec.Code != http.StatusConflict {
			t.Fatalf("status = %d, want 409; body=%s", rec.Code, rec.Body.String())
		}
		var prob api.Problem
		if err := json.Unmarshal(rec.Body.Bytes(), &prob); err != nil {
			t.Fatalf("decode problem: %v body=%s", err, rec.Body.String())
		}
		if prob.Code != "instance_not_restartable" {
			t.Errorf("code = %q, want instance_not_restartable", prob.Code)
		}
		if len(fake.insertCalls) != 0 {
			t.Errorf("intent should not have been inserted on 409; got %d calls", len(fake.insertCalls))
		}
	})

	t.Run("cold_booting_state_returns_409_no_intent", func(t *testing.T) {
		// Same as WAKING above — COLD_BOOTING instances are not
		// eligible for force-restart because the engine's locked
		// re-read rejects them as state.ErrInstanceNotRunning.
		fake := &fakeStoreForIntent{}
		srv, store, cookie := newForceHarness(t, fake)
		insID, _ := seedRunningInstance(t, store, "COLD_BOOTING")

		req := httptest.NewRequest(http.MethodPost,
			"/v1/admin/instances/"+insID+"/force-restart?confirm=true", nil)
		req.AddCookie(cookie)
		req.Header.Set("Idempotency-Key", "test-admin-mutation")
		rec := httptest.NewRecorder()
		srv.handler().ServeHTTP(rec, req)

		if rec.Code != http.StatusConflict {
			t.Fatalf("status = %d, want 409; body=%s", rec.Code, rec.Body.String())
		}
		var prob api.Problem
		if err := json.Unmarshal(rec.Body.Bytes(), &prob); err != nil {
			t.Fatalf("decode problem: %v body=%s", err, rec.Body.String())
		}
		if prob.Code != "instance_not_restartable" {
			t.Errorf("code = %q, want instance_not_restartable", prob.Code)
		}
		if len(fake.insertCalls) != 0 {
			t.Errorf("intent should not have been inserted on 409; got %d calls", len(fake.insertCalls))
		}
	})

	t.Run("store_returns_error_returns_500", func(t *testing.T) {
		fake := &fakeStoreForIntent{insertErr: errors.New("connection refused")}
		srv, store, cookie := newForceHarness(t, fake)
		insID, _ := seedRunningInstance(t, store, "RUNNING")

		req := httptest.NewRequest(http.MethodPost,
			"/v1/admin/instances/"+insID+"/force-restart?confirm=true", nil)
		req.AddCookie(cookie)
		req.Header.Set("Idempotency-Key", "test-admin-mutation")
		rec := httptest.NewRecorder()
		srv.handler().ServeHTTP(rec, req)

		if rec.Code != http.StatusInternalServerError {
			t.Fatalf("status = %d, want 500; body=%s", rec.Code, rec.Body.String())
		}
		var prob api.Problem
		if err := json.Unmarshal(rec.Body.Bytes(), &prob); err != nil {
			t.Fatalf("decode problem: %v body=%s", err, rec.Body.String())
		}
		if prob.Code != "internal_error" {
			t.Errorf("code = %q, want internal_error", prob.Code)
		}
	})

	t.Run("happy_path_inserts_intent_returns_202", func(t *testing.T) {
		fake := &fakeStoreForIntent{nextIntentID: "44444444-4444-4444-4444-444444444444"}
		srv, store, cookie := newForceHarness(t, fake)
		insID, _ := seedRunningInstance(t, store, "RUNNING")

		req := httptest.NewRequest(http.MethodPost,
			"/v1/admin/instances/"+insID+"/force-restart?confirm=true&reason=incident_42", nil)
		req.AddCookie(cookie)
		req.Header.Set("Idempotency-Key", "test-admin-mutation")
		rec := httptest.NewRecorder()
		srv.handler().ServeHTTP(rec, req)

		if rec.Code != http.StatusAccepted {
			t.Fatalf("status = %d, want 202; body=%s", rec.Code, rec.Body.String())
		}
		if len(fake.insertCalls) != 1 {
			t.Fatalf("intent insert calls = %d, want 1", len(fake.insertCalls))
		}
		if fake.insertCalls[0].Kind != state.OperatorIntentKindForceRestart {
			t.Errorf("intent kind = %q, want force_restart", fake.insertCalls[0].Kind)
		}
		if fake.insertCalls[0].Target != insID {
			t.Errorf("intent target = %q, want %q", fake.insertCalls[0].Target, insID)
		}
		if fake.insertCalls[0].Reason != "incident_42" {
			t.Errorf("intent reason = %q, want incident_42", fake.insertCalls[0].Reason)
		}
		if n := notifyCountByChannel(srv); n != 1 {
			t.Fatalf("notify calls on operator_intent channel = %d, want 1", n)
		}
		var body map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("decode body: %v body=%s", err, rec.Body.String())
		}
		if body["ok"] != true {
			t.Errorf("body.ok = %v, want true", body["ok"])
		}
		if body["intent_id"] != "44444444-4444-4444-4444-444444444444" {
			t.Errorf("body.intent_id = %v, want 44444444-...", body["intent_id"])
		}
		if body["status_url"] != "/v1/admin/operator-intents/44444444-4444-4444-4444-444444444444" {
			t.Errorf("body.status_url = %v", body["status_url"])
		}
		if body["previous_state"] != "RUNNING" {
			t.Errorf("body.previous_state = %v, want RUNNING", body["previous_state"])
		}
		if body["kind"] != "force_restart" {
			t.Errorf("body.kind = %v, want force_restart", body["kind"])
		}
	})

	t.Run("notify_failure_returns_202_with_intent_id", func(t *testing.T) {
		// pg_notify drop is non-fatal: the intent row is durable,
		// the 30s safety tick reclaims it, the response is still
		// 202 Accepted. Same precedent as handlers_cron_run.go.
		fake := &fakeStoreForIntent{nextIntentID: "55555555-5555-5555-5555-555555555555"}
		srv, store, cookie := newForceHarness(t, fake)
		srv.notif = &failingNotifier{err: errors.New("pg notify dropped")}
		insID, _ := seedRunningInstance(t, store, "RUNNING")

		req := httptest.NewRequest(http.MethodPost,
			"/v1/admin/instances/"+insID+"/force-restart?confirm=true", nil)
		req.AddCookie(cookie)
		req.Header.Set("Idempotency-Key", "test-admin-mutation")
		rec := httptest.NewRecorder()
		srv.handler().ServeHTTP(rec, req)

		if rec.Code != http.StatusAccepted {
			t.Fatalf("status = %d, want 202; body=%s", rec.Code, rec.Body.String())
		}
		if len(fake.insertCalls) != 1 {
			t.Fatalf("intent insert calls = %d, want 1", len(fake.insertCalls))
		}
		var body map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("decode body: %v body=%s", err, rec.Body.String())
		}
		if body["intent_id"] != "55555555-5555-5555-5555-555555555555" {
			t.Errorf("body.intent_id = %v, want 55555555-...", body["intent_id"])
		}
	})
}
