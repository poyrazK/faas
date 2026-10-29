// handlers_admin_operator_intent_test.go — pins the contract
// of the GET /v1/admin/operator-intents/{id} polling endpoint
// added in Commit P2.3 of the operator-side observability mega-PR
// (PR #1099).
//
// Table-driven cases cover the three load-bearing edges:
//
//  1. happy path — handler reads the row from the MemStore and
//     returns 200 with the full OperatorIntentResponse DTO
//     (status, target_id, requested_at, snap_ids_marked_stale
//     if populated).
//  2. not-found — handler returns 404 operator_intent_not_found
//     when the uuid is well-formed but no row exists. IDOR
//     closure: 404 (not 403) for missing-or-wrong-owner — the
//     byte-identical posture prevents an admin from
//     distinguishing "wrong id" from "wrong owner" by status
//     code alone.
//  3. invalid-uuid — handler returns 404 (not 400) for a
//     malformed path id so the route surface is binary — any
//     non-existing target is a 404.

package apidserver_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/wire"
)

// seedOperatorIntent inserts a force_park intent row into the
// MemStore and returns its UUID. Helper for the happy-path case.
func seedOperatorIntent(t *testing.T, store *state.MemStore, actorID, accountID, targetID string) string {
	t.Helper()
	id, err := store.InsertOperatorIntent(
		context.Background(),
		state.OperatorIntentKindForcePark,
		targetID,
		&accountID,
		actorID,
		"test_reason",
		nil,
		nil, // traceID — pre-C6; tests below pin trace_id round-trip explicitly.
	)
	if err != nil {
		t.Fatalf("InsertOperatorIntent: %v", err)
	}
	return id
}

func TestGetOperatorIntent_TableDriven(t *testing.T) {
	t.Run("found_returns_200_with_full_dto", func(t *testing.T) {
		store := state.NewMemStore()
		opsAcct, err := store.CreateAccount(context.Background(), "ops@example.com", api.PlanPro)
		if err != nil {
			t.Fatal(err)
		}
		tenant, err := store.CreateAccount(context.Background(), "tenant@example.com", api.PlanHobby)
		if err != nil {
			t.Fatal(err)
		}
		intentID := seedOperatorIntent(t, store, opsAcct.ID, tenant.ID, "11111111-1111-1111-1111-111111111111")

		ops := wire.NewOpsMetrics("apid_intent_test")
		srv := apidserver.NewServer(store, slog.New(slog.NewTextHandler(io.Discard, nil)), "gregale.dev", apidserver.NoopNotifier{}).
			WithOpsMetrics(context.Background(), ops).
			WithAdminAllowlist("ops@example.com")

		req := httptest.NewRequest(http.MethodGet, "/v1/admin/operator-intents/"+intentID, nil)
		req.AddCookie(operatorSessionCookie(t, store, srv.Sessions(), opsAcct.ID))
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
		}
		var body api.OperatorIntentResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("decode body: %v body=%s", err, rec.Body.String())
		}
		if body.IntentID != intentID {
			t.Errorf("intent_id = %q, want %q", body.IntentID, intentID)
		}
		if body.Kind != "force_park" {
			t.Errorf("kind = %q, want force_park", body.Kind)
		}
		if body.Status != string(state.OperatorIntentPending) {
			t.Errorf("status = %q, want pending", body.Status)
		}
		if body.TargetID != "11111111-1111-1111-1111-111111111111" {
			t.Errorf("target_id = %q, want instance uuid", body.TargetID)
		}
		if body.AccountID != tenant.ID {
			t.Errorf("account_id = %q, want %q", body.AccountID, tenant.ID)
		}
		if body.RequestedAt.IsZero() {
			t.Errorf("requested_at is zero")
		}
	})

	t.Run("not_found_returns_404", func(t *testing.T) {
		store := state.NewMemStore()
		opsAcct, err := store.CreateAccount(context.Background(), "ops@example.com", api.PlanPro)
		if err != nil {
			t.Fatal(err)
		}
		ops := wire.NewOpsMetrics("apid_intent_test")
		srv := apidserver.NewServer(store, slog.New(slog.NewTextHandler(io.Discard, nil)), "gregale.dev", apidserver.NoopNotifier{}).
			WithOpsMetrics(context.Background(), ops).
			WithAdminAllowlist("ops@example.com")

		// Well-formed uuid, no row — handler must 404
		// operator_intent_not_found. IDOR closure: same response
		// shape as wrong-owner case below.
		req := httptest.NewRequest(http.MethodGet,
			"/v1/admin/operator-intents/00000000-0000-0000-0000-000000000000", nil)
		req.AddCookie(operatorSessionCookie(t, store, srv.Sessions(), opsAcct.ID))
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, req)

		if rec.Code != http.StatusNotFound {
			t.Fatalf("status = %d, want 404; body=%s", rec.Code, rec.Body.String())
		}
		var prob api.Problem
		if err := json.Unmarshal(rec.Body.Bytes(), &prob); err != nil {
			t.Fatalf("decode problem: %v body=%s", err, rec.Body.String())
		}
		if prob.Code != api.CodeNotFound {
			t.Errorf("code = %q, want %q", prob.Code, api.CodeNotFound)
		}
		if prob.Title != "operator_intent_not_found" {
			t.Errorf("title = %q, want operator_intent_not_found", prob.Title)
		}
	})

	t.Run("invalid_uuid_returns_404", func(t *testing.T) {
		store := state.NewMemStore()
		opsAcct, err := store.CreateAccount(context.Background(), "ops@example.com", api.PlanPro)
		if err != nil {
			t.Fatal(err)
		}
		ops := wire.NewOpsMetrics("apid_intent_test")
		srv := apidserver.NewServer(store, slog.New(slog.NewTextHandler(io.Discard, nil)), "gregale.dev", apidserver.NoopNotifier{}).
			WithOpsMetrics(context.Background(), ops).
			WithAdminAllowlist("ops@example.com")

		// Malformed path id — handler must 404 (NOT 400) so the
		// route surface is binary: any non-existing target is a
		// 404. 400 leaks "this is a malformed uuid" vs "this is
		// a valid uuid but missing" — same IDOR concern.
		req := httptest.NewRequest(http.MethodGet,
			"/v1/admin/operator-intents/not-a-uuid", nil)
		req.AddCookie(operatorSessionCookie(t, store, srv.Sessions(), opsAcct.ID))
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, req)

		if rec.Code != http.StatusNotFound {
			t.Fatalf("status = %d, want 404; body=%s", rec.Code, rec.Body.String())
		}
	})
}
