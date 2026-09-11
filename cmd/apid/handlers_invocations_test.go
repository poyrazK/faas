//go:build !no_pg

// handlers_invocations_test.go — issue #315 / tier-2 DX.
//
// Tests for the POST /v1/invocations/{id}/replay handler. Mirrors
// the IDOR-safe read pattern that getInvocation established (handlers
// _invocations.go:466): a cross-tenant probe is indistinguishable
// from a missing-id 404 — never 403, never 200. The replay's state
// allow-list {failed, dead_letter} is asserted on both the happy
// path and the 409 path.
//
// Test environment uses the in-memory store (MemStore) so the test
// is hermetic — no Postgres dependency, runs under `-tags=!no_pg`
// OR under the default Go build. Mirrors handlers_queues_read_test.go
// for the cross-account 404 shape.
package main

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// seedInvocation is a local helper that inserts an Invocation row
// with a known id + state. Returns the id so the test can probe
// against the freshly-seeded row.
//
// Lives here (not in handlers_ext_test.go) because it's specific to
// the invocation surface and the other tests don't need it.
//
// Note: EnqueueInvocation always inserts the row in state=pending
// (the store mutates state to InvocationPending on its own), so the
// `stateName` parameter is honoured by a follow-up
// forceInvocationState call that drives the row through the
// production drain's claim → terminate sequence.
func seedInvocation(t *testing.T, e testEnv, stateName string, source state.InvocationSource) (string, string) {
	t.Helper()
	appID := mustSeedApp(t, e, "replay-app")
	now := time.Now().UTC()
	inv, err := e.store.EnqueueInvocation(context.Background(), state.Invocation{
		AppID:     appID,
		AccountID: e.acct.ID,
		Source:    source,
		Method:    "POST",
		Path:      "/api/charge",
		Payload:   json.RawMessage(`{"amount":42}`),
		DueAt:     now,
		CreatedAt: now,
	})
	if err != nil {
		t.Fatalf("seed invocation: %v", err)
	}
	// Flip to the target terminal state if it's anything other
	// than the default pending. The store's FailInvocation /
	// DeadLetter helpers do this for the production path; here
	// we use the public mark-failed / mark-dead-letter surfaces
	// when present, or fall back to UpdateInvocation. The replay
	// handler only reads State, so any path that lands the row in
	// the target state is acceptable.
	if stateName != "pending" {
		if err := forceInvocationState(t, e, inv.ID, stateName); err != nil {
			t.Fatalf("flip invocation state to %s: %v", stateName, err)
		}
	}
	return inv.ID, appID
}

// forceInvocationState updates the row to the named state via the
// production store methods (Claim + Complete / Fail) so the test
// exercises the same write path the drain uses. Skipping the
// claim → terminate sequence (and instead mutating the struct in
// memory) would let a test pass against a buggy MemStore that
// allows illegal transitions; this approach pins the real contract.
//
// The drain's claim → complete/fail dance:
//
//	pending → ClaimInvocation → dispatching → CompleteInvocation → completed
//	pending → ClaimInvocation → dispatching → FailInvocation(0,0) → failed
//	pending → ClaimInvocation → dispatching → FailInvocation(retry,1) → dead_letter
//
// For states the replay route already rejects (pending, dispatching,
// cancelled) we just don't drive any transition — the freshly
// enqueued row IS in pending.
func forceInvocationState(t *testing.T, e testEnv, id, stateName string) error {
	t.Helper()
	ctx := context.Background()
	switch stateName {
	case "pending":
		// EnqueueInvocation already leaves the row at pending — no-op.
		return nil
	case "dispatching":
		_, err := e.store.ClaimInvocation(ctx, id, "test-inst", 30)
		return err
	case "completed":
		if _, err := e.store.ClaimInvocation(ctx, id, "test-inst", 30); err != nil {
			return err
		}
		return e.store.CompleteInvocation(ctx, id, json.RawMessage(`{"status":200}`))
	case "failed":
		if _, err := e.store.ClaimInvocation(ctx, id, "test-inst", 30); err != nil {
			return err
		}
		return e.store.FailInvocation(ctx, id, "test failure", 0, 0)
	case "dead_letter":
		if _, err := e.store.ClaimInvocation(ctx, id, "test-inst", 30); err != nil {
			return err
		}
		// retryAfter > 0 with budget == 1 + Attempts already 1 (Claim
		// incremented it) lands the row in dead_letter.
		return e.store.FailInvocation(ctx, id, "test budget exhausted", time.Second, 1)
	case "cancelled":
		return e.store.CancelInvocation(ctx, id)
	default:
		t.Fatalf("unknown test state %q", stateName)
		return nil
	}
}

// TestReplayInvocation_HappyPath pins the load-bearing replay flow:
//  1. POST /v1/invocations/{id}/replay with the original in state
//     "failed" returns 202 + AsyncInvokeResponse.
//  2. The new row exists in the store with Source="replay".
//  3. The new row's payload/method/path match the original verbatim.
func TestReplayInvocation_HappyPath(t *testing.T) {
	e := setup(t, api.PlanPro)
	id, _ := seedInvocation(t, e, "failed", state.InvocationAsyncInvoke)

	rec := e.do(t, "POST", "/v1/invocations/"+id+"/replay", nil, nil)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202; body=%s", rec.Code, rec.Body.String())
	}
	var resp api.AsyncInvokeResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal response: %v\nbody=%s", err, rec.Body.String())
	}
	if resp.ID == "" {
		t.Errorf("response.id is empty")
	}
	if resp.StatusURL != "/v1/invocations/"+resp.ID {
		t.Errorf("response.status_url = %q, want %q", resp.StatusURL, "/v1/invocations/"+resp.ID)
	}

	// The new row exists and carries the replay stamp.
	got, err := e.store.InvocationByID(context.Background(), resp.ID)
	if err != nil {
		t.Fatalf("InvocationByID(newID): %v", err)
	}
	if got.Source != state.InvocationReplay {
		t.Errorf("new row Source = %q, want %q", got.Source, state.InvocationReplay)
	}
	if got.AccountID != e.acct.ID {
		t.Errorf("new row AccountID = %q, want %q (must be the replayer's account, not the original's)",
			got.AccountID, e.acct.ID)
	}
	if got.Method != "POST" || got.Path != "/api/charge" {
		t.Errorf("new row method/path = %s/%s, want POST//api/charge (original's verbatim)",
			got.Method, got.Path)
	}
	if string(got.Payload) != `{"amount":42}` {
		t.Errorf("new row payload = %s, want {\"amount\":42}", string(got.Payload))
	}
}

// TestReplayInvocation_DeadLetterAllowed: state=dead_letter also
// passes the allow-list (issue #394 terminal failure mode after the
// retry budget is spent).
func TestReplayInvocation_DeadLetterAllowed(t *testing.T) {
	e := setup(t, api.PlanPro)
	id, _ := seedInvocation(t, e, "dead_letter", state.InvocationQueue)

	rec := e.do(t, "POST", "/v1/invocations/"+id+"/replay", nil, nil)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202; body=%s", rec.Code, rec.Body.String())
	}
}

// TestReplayInvocation_NotReplayableStates: any state outside the
// allow-list {failed, dead_letter} returns 409 with the
// invocation_not_replayable code. The detail message surfaces the
// current state so the customer's CLI template can render it
// without parsing prose.
func TestReplayInvocation_NotReplayableStates(t *testing.T) {
	cases := []struct {
		state string
	}{
		{"completed"},
		{"pending"},
		{"dispatching"},
		{"cancelled"},
	}
	for _, tc := range cases {
		t.Run(tc.state, func(t *testing.T) {
			e := setup(t, api.PlanPro)
			id, _ := seedInvocation(t, e, tc.state, state.InvocationAsyncInvoke)

			rec := e.do(t, "POST", "/v1/invocations/"+id+"/replay", nil, nil)
			if rec.Code != http.StatusConflict {
				t.Fatalf("status = %d, want 409; body=%s", rec.Code, rec.Body.String())
			}
			var p api.Problem
			if err := json.Unmarshal(rec.Body.Bytes(), &p); err != nil {
				t.Fatalf("unmarshal problem: %v\nbody=%s", err, rec.Body.String())
			}
			if p.Code != api.CodeInvocationNotReplayable {
				t.Errorf("problem.code = %q, want %q", p.Code, api.CodeInvocationNotReplayable)
			}
			if !strings.Contains(p.Detail, tc.state) {
				t.Errorf("problem.detail = %q, must contain current state %q", p.Detail, tc.state)
			}
		})
	}
}

// TestReplayInvocation_CrossAccount: the foreign tenant probes the
// replay route with their key. The store has no row for the foreign
// account, so the IDOR-safe path returns 404 (not 403 — same shape
// as getInvocation).
//
// The seed row is created on the owner side; the foreign env has a
// fresh MemStore, so the foreign probe lands on the
// InvocationByID → not-found branch.
func TestReplayInvocation_CrossAccount(t *testing.T) {
	owner := setup(t, api.PlanPro)
	id, _ := seedInvocation(t, owner, "failed", state.InvocationAsyncInvoke)

	foreign := setup(t, api.PlanPro)
	rec := foreign.do(t, "POST", "/v1/invocations/"+id+"/replay", nil, nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("cross-account replay status = %d, want 404 (IDOR-safe); body=%s",
			rec.Code, rec.Body.String())
	}
}

// TestReplayInvocation_UnknownID: a 404 with the
// invocation_not_found code. Mirrors the read-side 404 — the replay
// route cannot leak the existence of an id to a stranger.
func TestReplayInvocation_UnknownID(t *testing.T) {
	e := setup(t, api.PlanPro)
	rec := e.do(t, "POST", "/v1/invocations/nonexistent-id/replay", nil, nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("unknown id replay status = %d, want 404; body=%s",
			rec.Code, rec.Body.String())
	}
}

// TestReplayInvocation_AppTransferredAway: the peer review (PR #733
// finding F1) surfaced a gap where the replay handler only checked
// orig.AccountID == acct.ID. After an app transfer/reassignment, an
// old failed invocation retained under the original account could
// be replayed against the now-foreign app. The fix re-loads the
// app by orig.AppID and verifies app.AccountID == acct.ID; this
// test pins the contract.
//
// Set-up: the env's account owns the invocation row, but the
// referenced app belongs to a foreign account. We construct that
// scenario directly via the store (CreateApp takes a full App
// struct, AccountID included; UpdateApp is a PATCH-style API that
// does not expose AccountID by design — the real transfer path is
// a separate, audited operation that's out of scope here).
//
// The replay from the env's account must now return 404, matching
// the IDOR-safe pattern (never 403, never 200).
func TestReplayInvocation_AppTransferredAway(t *testing.T) {
	e := setup(t, api.PlanPro)
	// Seed a foreign app on a different account.
	foreignApp, err := e.store.CreateApp(context.Background(), state.App{
		AccountID: "00000000-0000-0000-0000-000000000099",
		Slug:      "foreign-app",
		Type:      state.AppTypeApp,
		Status:    state.AppActive,
	})
	if err != nil {
		t.Fatalf("CreateApp foreign: %v", err)
	}
	// Insert an invocation that points at the foreign app but
	// carries the env's account_id (the post-transfer state).
	now := time.Now().UTC()
	inv, err := e.store.EnqueueInvocation(context.Background(), state.Invocation{
		AppID:     foreignApp.ID,
		AccountID: e.acct.ID, // the env's account still owns the row
		Source:    state.InvocationAsyncInvoke,
		Method:    "POST",
		Path:      "/api/charge",
		Payload:   json.RawMessage(`{"amount":42}`),
		DueAt:     now,
		CreatedAt: now,
	})
	if err != nil {
		t.Fatalf("EnqueueInvocation: %v", err)
	}
	// Flip the row to failed so it would pass the state allow-list
	// if the app-ownership check didn't catch the mismatch first.
	if _, err := e.store.ClaimInvocation(context.Background(), inv.ID, "test-inst", 30); err != nil {
		t.Fatalf("ClaimInvocation: %v", err)
	}
	if err := e.store.FailInvocation(context.Background(), inv.ID, "test failure", 0, 0); err != nil {
		t.Fatalf("FailInvocation: %v", err)
	}

	rec := e.do(t, "POST", "/v1/invocations/"+inv.ID+"/replay", nil, nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("replay against foreign app status = %d, want 404 (IDOR-safe); body=%s",
			rec.Code, rec.Body.String())
	}
}

// TestListInvocations_PaginatesWithNextBefore proves that the public
// cursor is discoverable and advances without repeating the anchor row.
func TestListInvocations_PaginatesWithNextBefore(t *testing.T) {
	e := setup(t, api.PlanHobby)
	appID := mustSeedApp(t, e, "invocation-pagination")
	ctx := context.Background()
	now := time.Now().UTC()
	for i := 0; i < 3; i++ {
		_, err := e.store.EnqueueInvocation(ctx, state.Invocation{
			AppID: appID, AccountID: e.acct.ID, Source: state.InvocationAsyncInvoke,
			Method: "POST", Path: "/invoke", DueAt: now,
			CreatedAt: now.Add(time.Duration(i) * time.Second),
		})
		if err != nil {
			t.Fatalf("enqueue %d: %v", i, err)
		}
	}

	first := e.do(t, http.MethodGet, "/v1/invocations?limit=2", nil, nil)
	if first.Code != http.StatusOK {
		t.Fatalf("first page status = %d: %s", first.Code, first.Body)
	}
	var page1 api.ListInvocationsResponse
	if err := json.Unmarshal(first.Body.Bytes(), &page1); err != nil {
		t.Fatalf("decode first page: %v", err)
	}
	if len(page1.Invocations) != 2 || page1.NextBefore == "" {
		t.Fatalf("first page = %+v, want 2 rows and a cursor", page1)
	}

	second := e.do(t, http.MethodGet, "/v1/invocations?limit=2&before="+page1.NextBefore, nil, nil)
	if second.Code != http.StatusOK {
		t.Fatalf("second page status = %d: %s", second.Code, second.Body)
	}
	var page2 api.ListInvocationsResponse
	if err := json.Unmarshal(second.Body.Bytes(), &page2); err != nil {
		t.Fatalf("decode second page: %v", err)
	}
	if len(page2.Invocations) != 1 || page2.Invocations[0].ID == page1.Invocations[0].ID || page2.NextBefore != "" {
		t.Fatalf("second page = %+v, want one older non-overlapping row and no cursor", page2)
	}
}
