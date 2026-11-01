package apidserver_test

// adr: 20261101093812

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/workpolicy"
)

// Account replay-all took the first limit open entries and skipped the
// expired keyed ones among them, so a first page of expired keyed work
// replayed nothing on every call and never reached the entries behind it.
// It now pages past them.
func TestAccountDeadLetterReplayAllPagesPastExpiredKeyedWork(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	ctx := context.Background()
	appID := apidserver.MustSeedApp(t, e, "account-dlq-keyed")
	replayable := apidserver.SeedDeadLetterRow(t, e, appID, "replayable")
	// The keyed entry fails later, so it heads the newest-first ledger.
	time.Sleep(10 * time.Millisecond)
	policy := workpolicy.Policy{Name: "expiry", MaxRunningPerKey: 1, ExpiresAfter: 2 * time.Second}
	keyed, err := (*e.Store()).EnqueueKeyedInvocation(ctx, state.Invocation{AccountID: (*e.Acct()).ID, AppID: appID,
		Source: state.InvocationAsyncInvoke, DueAt: time.Now()}, policy, "s:expired")
	if err != nil {
		t.Fatal(err)
	}
	claimed, err := (*e.Store()).ClaimInvocationWithCap(ctx, keyed.ID, "test-inst", 30, 10)
	if err != nil {
		t.Fatal(err)
	}
	if err := (*e.Store()).FailInvocation(ctx, keyed.ID, "keyed handler failure", time.Minute, 1, state.WithClaimAttempt(claimed.Attempts)); err != nil {
		t.Fatal(err)
	}
	if got, err := (*e.Store()).InvocationByID(ctx, keyed.ID); err != nil || got.State != state.InvocationDeadLetter {
		t.Fatalf("keyed seed = %+v, %v; want dead_letter", got.State, err)
	}
	time.Sleep(time.Until(*keyed.WorkExpiresAt) + time.Millisecond)

	rec := e.Do(t, http.MethodPost, "/v1/account/dlq:replay_all?limit=1", nil,
		map[string]string{"Idempotency-Key": "account-dlq-replay-past-expired"})
	if rec.Code != http.StatusAccepted {
		t.Fatalf("account replay-all status = %d; body=%s", rec.Code, rec.Body.String())
	}
	var out api.AccountDeadLetterReplayAllResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil || out.Replayed != 1 {
		t.Fatalf("account replay-all = %+v, %v; want the entry behind the expired keyed one", out, err)
	}
	if got, err := (*e.Store()).InvocationByID(ctx, replayable); err != nil || got.State != state.InvocationPending {
		t.Fatalf("replayable entry = %+v, %v; want pending", got.State, err)
	}
	if got, err := (*e.Store()).InvocationByID(ctx, keyed.ID); err != nil || got.State != state.InvocationDeadLetter {
		t.Fatalf("expired keyed entry = %+v, %v; want it left in the dead letter", got.State, err)
	}
}

// A dead-lettered queue message replayed into a full queue gets the limit
// problem a send gets, and stays in the dead letter.
func TestDeadLetterReplayIntoFullQueueIsRefused(t *testing.T) {
	e := apidserver.Setup(t, api.PlanHobby)
	ctx := context.Background()
	appID := apidserver.MustSeedApp(t, e, "dlq-full-queue")
	poisoned := apidserver.SeedDeadLetterRow(t, e, appID, "poisoned")
	depth := api.MustLimitsFor(api.PlanHobby).MaxQueueDepth
	for range depth {
		if _, err := (*e.Store()).EnqueueInvocation(ctx, state.Invocation{AppID: appID, AccountID: (*e.Acct()).ID,
			Source: state.InvocationQueue, Payload: json.RawMessage(`{}`), DueAt: time.Now().UTC()}); err != nil {
			t.Fatal(err)
		}
	}
	list := e.Do(t, http.MethodGet, "/v1/apps/dlq-full-queue/dlq", nil, nil)
	var page api.DeadLetterEventsResponse
	if err := json.Unmarshal(list.Body.Bytes(), &page); err != nil || len(page.Events) != 1 {
		t.Fatalf("dead letters = %s, %v; want the poisoned message", list.Body, err)
	}
	for _, path := range []string{"/v1/apps/dlq-full-queue/dlq/" + page.Events[0].ID + "/replay", "/v1/apps/dlq-full-queue/dlq:replay_all"} {
		rec := e.Do(t, http.MethodPost, path, nil, map[string]string{"Idempotency-Key": "full-queue-" + path})
		var problem api.Problem
		if rec.Code != http.StatusForbidden || json.Unmarshal(rec.Body.Bytes(), &problem) != nil || problem.Code != api.CodePlanQueueDepth {
			t.Fatalf("%s into a full queue: %d %s; want %s", path, rec.Code, rec.Body, api.CodePlanQueueDepth)
		}
	}
	if got, err := (*e.Store()).InvocationByID(ctx, poisoned); err != nil || got.State != state.InvocationDeadLetter {
		t.Fatalf("refused message = %+v, %v; want it left in the dead letter", got.State, err)
	}
}
