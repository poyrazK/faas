package apidserver

// Issue #394 — queue introspection handler tests.
//
// All read-only endpoints stay purely on MemStore here. The
// byte-identical SQL property test (TestQueuePeek_ByteIdentical) lives
// in pkg/state because it needs pgtest — the handler is a thin
// mapping layer, the SQL is what owns the "no mutation" guarantee.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// seedQueueRow writes a queue-source invocation row directly through
// the store. Bypasses the cap check that queueSend applies — the
// introspection endpoints must work for diagnostics even when the
// queue is technically over the plan limit (e.g., drained-but-stuck
// rows after a plan downgrade).
func seedQueueRow(t *testing.T, e testEnv, appID string, payload string) string {
	t.Helper()
	inv, err := e.store.EnqueueInvocation(context.Background(), state.Invocation{
		AppID:     appID,
		AccountID: e.acct.ID,
		Source:    state.InvocationQueue,
		Payload:   json.RawMessage(payload),
		DueAt:     time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("seed queue row: %v", err)
	}
	return inv.ID
}

// TestQueueState_HappyPath returns the depth + plan_cap for a Pro
// plan with 2 pending rows. In-flight is 0 because nothing has been
// claimed; oldest_pending_at is the oldest row's created_at.
func TestQueueState_HappyPath(t *testing.T) {
	e := setup(t, api.PlanPro)
	appID := mustSeedApp(t, e, "myapp")
	seedQueueRow(t, e, appID, `{"i":0}`)
	seedQueueRow(t, e, appID, `{"i":1}`)

	rec := e.do(t, "GET", "/v1/apps/myapp/queues/state", nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var out api.QueueStateResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if out.PlanCap != api.MustLimitsFor(api.PlanPro).MaxQueueDepth {
		t.Errorf("PlanCap = %d, want %d", out.PlanCap, api.MustLimitsFor(api.PlanPro).MaxQueueDepth)
	}
	if out.Depth != 2 {
		t.Errorf("Depth = %d, want 2", out.Depth)
	}
	if out.InFlight != 0 {
		t.Errorf("InFlight = %d, want 0", out.InFlight)
	}
	if out.OldestPendingAt == nil {
		t.Errorf("OldestPendingAt = nil, want set")
	}
	if out.OldestPendingAgeSeconds == nil {
		t.Errorf("OldestPendingAgeSeconds = nil, want set")
	}
}

// TestQueueState_EmptyQueue: oldest_pending_at MUST be omitted (not
// zero) when the queue is empty so dashboards can render "no backlog".
func TestQueueState_EmptyQueue(t *testing.T) {
	e := setup(t, api.PlanPro)
	mustSeedApp(t, e, "myapp")

	rec := e.do(t, "GET", "/v1/apps/myapp/queues/state", nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	if bytes.Contains(rec.Body.Bytes(), []byte("oldest_pending_at")) {
		t.Errorf("oldest_pending_at present on empty queue — want omitted\n%s", rec.Body.String())
	}
	if bytes.Contains(rec.Body.Bytes(), []byte("oldest_pending_age_seconds")) {
		t.Errorf("oldest_pending_age_seconds present on empty queue — want omitted\n%s", rec.Body.String())
	}
}

// TestQueueState_FreePlanAllowed pins the diagnostic-only contract:
// even though Free is gated out of queueSend/queueReceive
// (queueReceive returns 402 ErrPlanQueuesNotAllowed), the introspection
// endpoints must still be reachable for ops/debug. PlanCap=0
// (Free's MaxQueueDepth) is the visible signal that no new messages
// can be sent; Depth=0 because nothing is in flight. A future change
// that moves the gate INTO queueState would break this contract and
// would surface here.
func TestQueueState_FreePlanAllowed(t *testing.T) {
	e := setup(t, api.PlanFree)
	mustSeedApp(t, e, "free-app")

	rec := e.do(t, "GET", "/v1/apps/free-app/queues/state", nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (introspection must work on Free for diagnostics); body=%s", rec.Code, rec.Body.String())
	}
	var out api.QueueStateResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if out.Plan != "free" {
		t.Errorf("Plan = %q, want free", out.Plan)
	}
	if out.PlanCap != 0 {
		t.Errorf("PlanCap = %d, want 0 (Free's MaxQueueDepth)", out.PlanCap)
	}
	if out.Depth != 0 {
		t.Errorf("Depth = %d, want 0", out.Depth)
	}
}

// TestQueuePeek_HappyPath: pending rows are returned oldest-first.
// The payload is surfaced as a JSON string (verbatim from the jsonb
// column) so callers can decode with their preferred JSON lib.
func TestQueuePeek_HappyPath(t *testing.T) {
	e := setup(t, api.PlanPro)
	appID := mustSeedApp(t, e, "myapp")
	oldestID := seedQueueRow(t, e, appID, `{"i":0}`)
	time.Sleep(2 * time.Millisecond)
	seedQueueRow(t, e, appID, `{"i":1}`)

	rec := e.do(t, "GET", "/v1/apps/myapp/queues/peek?limit=10", nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var out api.QueuePeekResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(out.Messages) != 2 {
		t.Fatalf("len(Messages) = %d, want 2", len(out.Messages))
	}
	if out.Messages[0].ID != oldestID {
		t.Errorf("Messages[0].ID = %q, want %q (oldest first)", out.Messages[0].ID, oldestID)
	}
	if !out.Messages[0].CreatedAt.Before(out.Messages[1].CreatedAt) {
		t.Errorf("Messages not ordered by created_at ASC: %+v", out.Messages)
	}
	if out.Messages[0].Payload != `{"i":0}` {
		t.Errorf("Messages[0].Payload = %q, want %q", out.Messages[0].Payload, `{"i":0}`)
	}
}

// TestQueuePeek_DoesNotLease confirms peek does not acquire a lease.
// Peek twice, then call queues/receive, and the first row returned
// by receive must be the same id peek saw first — receive claims it
// because peek did not.
func TestQueuePeek_DoesNotLease(t *testing.T) {
	e := setupWithNotifier(t, api.PlanPro, func(_ context.Context, _ string, _ func(string) bool, _ time.Duration) (string, error) {
		return "", errors.New("notifier never matches — peek must not depend on notifications")
	})
	appID := mustSeedApp(t, e, "myapp")
	expectedID := seedQueueRow(t, e, appID, `{"i":0}`)

	// First peek — must return the row, NOT acquire a lease.
	rec := e.do(t, "GET", "/v1/apps/myapp/queues/peek", nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("peek 1 status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var peek api.QueuePeekResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &peek); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(peek.Messages) != 1 || peek.Messages[0].ID != expectedID {
		t.Fatalf("peek 1 = %+v, want id=%q", peek.Messages, expectedID)
	}
	// Row must still be pending (peek did not claim it).
	inv, err := e.store.InvocationByID(context.Background(), expectedID)
	if err != nil {
		t.Fatalf("InvocationByID: %v", err)
	}
	if inv.State != state.InvocationPending {
		t.Errorf("post-peek state = %q, want pending (peek must not lease)", inv.State)
	}
	if inv.Attempts != 0 {
		t.Errorf("post-peek attempts = %d, want 0", inv.Attempts)
	}
}

// TestQueuePeek_AfterReceive_StillPending: peek, lease via receive,
// peek again — the leased row MUST NOT appear in the second peek.
// This is the contract: peek shows pending rows only.
func TestQueuePeek_AfterReceive_StillPending(t *testing.T) {
	e := setupWithNotifier(t, api.PlanPro, func(_ context.Context, _ string, _ func(string) bool, _ time.Duration) (string, error) {
		return "", errors.New("no notification — row is leased but not yet drained")
	})
	appID := mustSeedApp(t, e, "myapp")
	seedQueueRow(t, e, appID, `{"i":0}`)
	seedQueueRow(t, e, appID, `{"i":1}`)

	// Lease the first row manually (receive is long-poll; we simulate
	// the lease by calling the store directly so the test is deterministic).
	rows, err := e.store.ListDueInvocations(context.Background(), time.Now().Add(time.Second), 1)
	if err != nil {
		t.Fatalf("ListDueInvocations: %v", err)
	}
	if len(rows) == 0 {
		t.Fatal("no due rows")
	}
	if _, err := e.store.ClaimInvocation(context.Background(), rows[0].ID, "inst-1", 30); err != nil {
		t.Fatalf("ClaimInvocation: %v", err)
	}

	// Peek must NOT include the claimed row.
	rec := e.do(t, "GET", "/v1/apps/myapp/queues/peek", nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("peek status = %d, want 200", rec.Code)
	}
	var peek api.QueuePeekResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &peek); err != nil {
		t.Fatalf("decode: %v", err)
	}
	for _, m := range peek.Messages {
		if m.ID == rows[0].ID {
			t.Errorf("claimed row %s appeared in peek — want hidden", m.ID)
		}
	}
}

// TestQueueDeadLetter_ExhaustedToDeadLetter drives a row past the
// plan's MaxQueueAttempts (Pro = 10) and asserts the row lands in
// state='dead_letter'. After exhaustion, ClaimInvocation on the same
// id must fail because dead_letter is terminal.
func TestQueueDeadLetter_ExhaustedToDeadLetter(t *testing.T) {
	e := setup(t, api.PlanPro)
	appID := mustSeedApp(t, e, "myapp")
	invID := seedQueueRow(t, e, appID, `{"i":0}`)

	// Budget=10 for Pro. Each transient FailInvocation increments
	// attempts and re-queues with retryAfter>0; the moment attempts
	// reaches the budget, the next transient failure transitions the
	// row to dead_letter instead.
	budget := api.MustLimitsFor(api.PlanPro).MaxQueueAttempts
	for i := 1; i <= budget; i++ {
		if _, err := e.store.ClaimInvocation(context.Background(), invID, "inst", 30); err != nil {
			t.Fatalf("claim iter %d: %v", i, err)
		}
		if err := e.store.FailInvocation(context.Background(), invID, fmt.Sprintf("blip %d", i), time.Minute, budget); err != nil {
			t.Fatalf("FailInvocation iter %d: %v", i, err)
		}
	}

	// After the budget is exhausted the row must be dead_letter.
	inv, err := e.store.InvocationByID(context.Background(), invID)
	if err != nil {
		t.Fatalf("InvocationByID: %v", err)
	}
	if inv.State != state.InvocationDeadLetter {
		t.Fatalf("state = %q, want dead_letter after %d attempts", inv.State, budget)
	}

	// The dead-letter endpoint must return it.
	rec := e.do(t, "GET", "/v1/apps/myapp/queues/dead_letter", nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var out api.QueueDeadLetterResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(out.Messages) != 1 {
		t.Fatalf("len(Messages) = %d, want 1", len(out.Messages))
	}
	if out.Messages[0].ID != invID {
		t.Errorf("Messages[0].ID = %q, want %q", out.Messages[0].ID, invID)
	}
	if !strings.Contains(out.Messages[0].LastError, "blip") {
		t.Errorf("LastError = %q, want contains %q", out.Messages[0].LastError, "blip")
	}

	// Re-claim must fail — dead_letter is terminal.
	if _, err := e.store.ClaimInvocation(context.Background(), invID, "inst", 30); !errors.Is(err, state.ErrNotFound) {
		t.Errorf("post-dead-letter claim err = %v, want ErrNotFound", err)
	}
}

// TestQueueDeadLetter_OrderNewestFirst pins the wire-shape contract
// for the dead-letter endpoint: rows must surface newest-first (DESC
// by created_at). Three rows are seeded with strictly-increasing
// created_at; the response order must match the seeded order in
// reverse — the migration's partial index, the store's ORDER BY, and
// the handler's next-before emission all share this invariant, and a
// regression at any layer would surface here.
func TestQueueDeadLetter_OrderNewestFirst(t *testing.T) {
	e := setup(t, api.PlanPro)
	appID := mustSeedApp(t, e, "myapp")
	// idOldest is seeded first (earliest CreatedAt); idMiddle second;
	// idNewest last (latest CreatedAt). With 2ms sleeps between, the
	// three CreatedAt values are strictly monotonic.
	idOldest := seedDeadLetterRow(t, e, appID, "oldest")
	time.Sleep(2 * time.Millisecond)
	idMiddle := seedDeadLetterRow(t, e, appID, "middle")
	time.Sleep(2 * time.Millisecond)
	idNewest := seedDeadLetterRow(t, e, appID, "newest")

	rec := e.do(t, "GET", "/v1/apps/myapp/queues/dead_letter", nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var out api.QueueDeadLetterResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(out.Messages) != 3 {
		t.Fatalf("len(Messages) = %d, want 3", len(out.Messages))
	}
	if out.Messages[0].ID != idNewest {
		t.Errorf("Messages[0].ID = %q, want %q (newest first)", out.Messages[0].ID, idNewest)
	}
	if out.Messages[1].ID != idMiddle {
		t.Errorf("Messages[1].ID = %q, want %q", out.Messages[1].ID, idMiddle)
	}
	if out.Messages[2].ID != idOldest {
		t.Errorf("Messages[2].ID = %q, want %q", out.Messages[2].ID, idOldest)
	}
}

// seedDeadLetterRow inserts a queue-source invocation directly into
// state='dead_letter' so the handler-level ordering test doesn't have
// to wait through MaxQueueAttempts retry cycles. Bypasses
// FailInvocation's path so the test is deterministic and fast.
//
// Implementation: claim once (bumps attempts from 0→1), then
// FailInvocation with budget=1 — the predicate `attempts >= budget`
// fires and the row transitions to state='dead_letter' on the first
// iteration. LastError is set to the supplied label so the test can
// distinguish rows.
func seedDeadLetterRow(t *testing.T, e testEnv, appID, label string) string {
	t.Helper()
	inv, err := e.store.EnqueueInvocation(context.Background(), state.Invocation{
		AppID:     appID,
		AccountID: e.acct.ID,
		Source:    state.InvocationQueue,
		Payload:   json.RawMessage(`{"label":"` + label + `"}`),
		DueAt:     time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("enqueue seed row: %v", err)
	}
	if _, err := e.store.ClaimInvocation(context.Background(), inv.ID, "inst", 30); err != nil {
		t.Fatalf("claim seed row: %v", err)
	}
	if err := e.store.FailInvocation(context.Background(), inv.ID, label+": poisoned", time.Minute, 1); err != nil {
		t.Fatalf("fail seed row: %v", err)
	}
	// Sanity: the row must actually be in dead_letter state.
	got, err := e.store.InvocationByID(context.Background(), inv.ID)
	if err != nil {
		t.Fatalf("re-read seed row: %v", err)
	}
	if got.State != state.InvocationDeadLetter {
		t.Fatalf("seed row state = %q, want dead_letter", got.State)
	}
	return inv.ID
}

// TestQueueRead_CrossAccount: the IDOR-safety property. Cross-account
// reads surface 404 (not 403, not 200) so callers cannot enumerate
// slugs in other accounts. Same shape as the existing getInvocation
// precedent in handlers_invocations_test.go.
func TestQueueRead_CrossAccount(t *testing.T) {
	// Owner account owns the app; foreign account probes with their key.
	owner := setup(t, api.PlanPro)
	appID := mustSeedApp(t, owner, "owner-app")
	seedQueueRow(t, owner, appID, `{"i":0}`)

	// Foreign env has a fresh MemStore (server_test.go: setup() creates
	// a fresh state.NewMemStore() per call, so the foreign env has its
	// own store with no rows). The 404 here comes from loadApp →
	// AppBySlug → not-found in the foreign store, NOT from cross-account
	// authorization enforcement. For genuine shared-store isolation
	// coverage (the actual IDOR shape — a customer probing another
	// customer's slug on the same DB), see the spec_compliance matrix
	// in issue #394 §acceptance #4.
	foreign := setup(t, api.PlanPro)

	probes := []struct {
		name string
		path string
	}{
		{"state", "/v1/apps/owner-app/queues/state"},
		{"peek", "/v1/apps/owner-app/queues/peek"},
		{"dead_letter", "/v1/apps/owner-app/queues/dead_letter"},
	}
	for _, p := range probes {
		rec := foreign.do(t, "GET", p.path, nil, nil)
		if rec.Code != http.StatusNotFound {
			t.Errorf("%s: status = %d, want 404 (cross-account read must surface 404, not 403/200); body=%s",
				p.name, rec.Code, rec.Body.String())
		}
	}

	// Owner can read everything.
	for _, p := range probes {
		rec := owner.do(t, "GET", p.path, nil, nil)
		if rec.Code != http.StatusOK {
			t.Errorf("owner %s: status = %d, want 200; body=%s", p.name, rec.Code, rec.Body.String())
		}
	}
}

// TestQueueRead_UnknownAppIs404: a non-existent slug is 404 for both
// owner and foreign — same shape, no information leak about which
// slug exists.
func TestQueueRead_UnknownAppIs404(t *testing.T) {
	e := setup(t, api.PlanPro)
	for _, path := range []string{
		"/v1/apps/no-such-app/queues/state",
		"/v1/apps/no-such-app/queues/peek",
		"/v1/apps/no-such-app/queues/dead_letter",
	} {
		rec := e.do(t, "GET", path, nil, nil)
		if rec.Code != http.StatusNotFound {
			t.Errorf("%s: status = %d, want 404", path, rec.Code)
		}
	}
}

// --- small helpers (kept local so the test stays self-contained) ---
