package state_test

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/workpolicy"
)

func TestPgKeyedTriggerAdmissionSharesInvocationLane(t *testing.T) {
	store, pool, ctx := pgStoreWithPool(t)
	accountID, appID, _ := seedLiveDeploy(t, store, ctx)
	policy, err := store.UpsertAppWorkPolicy(ctx, accountID, appID, workpolicy.Policy{
		Name: "documents", MaxRunningPerKey: 1, PendingUpdates: workpolicy.PendingKeepLatest,
		MaxRunningPerFairnessKey: 2, ExpiresAfter: time.Minute,
	})
	if err != nil {
		t.Fatal(err)
	}
	trigger, err := store.CreateTriggerIfUnderQuota(ctx, appID, "kafka", "documents", true,
		[]byte(`{}`), "", 10, 1000, 3, 1<<20, "commit", api.MustLimitsFor(api.PlanPro))
	if err != nil {
		t.Fatal(err)
	}
	first, err := store.EnqueueKeyedInvocation(ctx, state.Invocation{
		AccountID: accountID, AppID: appID, Source: state.InvocationAsyncInvoke,
		Payload: json.RawMessage(`{"version":1}`),
	}, policy.Policy, "s:document-1", "s:tenant-1")
	if err != nil {
		t.Fatal(err)
	}
	admit := func(item string) string {
		t.Helper()
		id, err := store.InsertKeyedTriggerRecord(ctx, trigger.ID.String(), item,
			[]byte(`{"document_id":"document-1"}`), nil, nil,
			policy, "s:document-1", "s:tenant-1")
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	firstBroker := admit("partition-0-offset-1")
	if got, err := store.InvocationByID(ctx, first.ID); err != nil || got.State != state.InvocationSuperseded {
		t.Fatalf("broker admission did not replace pending invocation: %+v, err=%v", got, err)
	}
	secondBroker := admit("partition-0-offset-2")
	var firstState, secondState string
	var sequence, revision int64
	var fairnessLimit int
	var keyBytes, fairnessBytes int
	var expiresAt time.Time
	if err := pool.QueryRow(ctx, `select state from trigger_records where id=$1`, firstBroker).Scan(&firstState); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `select state, work_sequence, work_policy_revision,
		work_fairness_limit, length(work_key_digest), length(work_fairness_digest), work_expires_at
		from trigger_records where id=$1`, secondBroker).Scan(&secondState, &sequence,
		&revision, &fairnessLimit, &keyBytes, &fairnessBytes, &expiresAt); err != nil {
		t.Fatal(err)
	}
	if firstState != "superseded" || secondState != "pending" || sequence != first.WorkSequence+2 ||
		revision != policy.Revision || fairnessLimit != 2 || keyBytes != 32 || fairnessBytes != 32 ||
		!expiresAt.After(time.Now()) {
		t.Fatalf("broker lane snapshot: first=%s second=%s seq=%d revision=%d fairness=%d digest=%d/%d expiry=%s",
			firstState, secondState, sequence, revision, fairnessLimit, keyBytes, fairnessBytes, expiresAt)
	}
	if replay := admit("partition-0-offset-1"); replay != firstBroker {
		t.Fatalf("broker replay id = %s, want %s", replay, firstBroker)
	}
	if err := pool.QueryRow(ctx, `select state from trigger_records where id=$1`, secondBroker).Scan(&secondState); err != nil || secondState != "pending" {
		t.Fatalf("broker replay replaced newer work: state=%s err=%v", secondState, err)
	}
	// A later invocation must wait behind the broker record in this lane.
	later, err := store.EnqueueKeyedInvocation(ctx, state.Invocation{
		AccountID: accountID, AppID: appID, Source: state.InvocationAsyncInvoke,
	}, workpolicy.Policy{Name: "documents", MaxRunningPerKey: 1}, "s:document-1")
	if err != nil {
		t.Fatal(err)
	}
	if later.WorkSequence != sequence+1 {
		t.Fatalf("invocation did not share broker sequence: %d after %d", later.WorkSequence, sequence)
	}
}

func TestPgKeyedTriggerClaimOrdersBrokerAndInvocationWork(t *testing.T) {
	store, pool, ctx := pgStoreWithPool(t)
	accountID, appID, _ := seedLiveDeploy(t, store, ctx)
	policy, err := store.UpsertAppWorkPolicy(ctx, accountID, appID, workpolicy.Policy{
		Name: "orders", MaxRunningPerKey: 1, MaxRunningPerFairnessKey: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	trigger, err := store.CreateTriggerIfUnderQuota(ctx, appID, "kafka", "orders", true,
		[]byte(`{}`), "", 10, 1000, 3, 1<<20, "commit", api.MustLimitsFor(api.PlanPro))
	if err != nil {
		t.Fatal(err)
	}
	insert := func(item, key string) string {
		t.Helper()
		id, err := store.InsertKeyedTriggerRecord(ctx, trigger.ID.String(), item,
			[]byte(`{"order_id":"one"}`), nil, nil, policy, key, "s:tenant-one")
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	firstID := insert("partition-0-offset-1", "s:order-one")
	secondID := insert("partition-0-offset-2", "s:order-one")
	thirdID := insert("partition-0-offset-3", "s:order-two")
	later, err := store.EnqueueKeyedInvocation(ctx, state.Invocation{
		AccountID: accountID, AppID: appID, Source: state.InvocationAsyncInvoke,
	}, policy.Policy, "s:order-one", "s:tenant-one")
	if err != nil {
		t.Fatal(err)
	}
	claim := func(items ...string) []string {
		t.Helper()
		records, err := store.ClaimTriggerRecordsByItems(ctx, trigger.ID.String(), items)
		if err != nil {
			t.Fatal(err)
		}
		ids := make([]string, 0, len(records))
		for _, record := range records {
			ids = append(ids, record.ID.String())
		}
		return ids
	}
	if got := claim("partition-0-offset-2", "partition-0-offset-3"); len(got) != 1 || got[0] != thirdID {
		t.Fatalf("out-of-order/fairness claim = %v", got)
	}
	if got := claim("partition-0-offset-1"); len(got) != 0 {
		t.Fatalf("fairness cap did not block first lane: %v", got)
	}
	var thirdGeneration int64
	if err := pool.QueryRow(ctx, `select claim_generation from trigger_records where id=$1`, thirdID).Scan(&thirdGeneration); err != nil {
		t.Fatal(err)
	}
	if err := store.CompleteClaimedTriggerRecord(ctx, thirdID, thirdGeneration); err != nil {
		t.Fatal(err)
	}
	if got := claim("partition-0-offset-1"); len(got) != 1 || got[0] != firstID {
		t.Fatalf("first broker claim = %v", got)
	}
	if due, err := store.ListDueInvocationsAfter(ctx, time.Now(), state.InvocationDueCursor{}, 64); err != nil || len(due) != 0 {
		t.Fatalf("invocation escaped older broker claim: %v, %v", due, err)
	}
	if got := claim("partition-0-offset-2"); len(got) != 0 {
		t.Fatalf("second broker escaped older claim: %v", got)
	}
	var firstGeneration int64
	if err := pool.QueryRow(ctx, `select claim_generation from trigger_records where id=$1`, firstID).Scan(&firstGeneration); err != nil {
		t.Fatal(err)
	}
	if err := store.CompleteClaimedTriggerRecord(ctx, firstID, firstGeneration); err != nil {
		t.Fatal(err)
	}
	if got := claim("partition-0-offset-2"); len(got) != 1 || got[0] != secondID {
		t.Fatalf("second broker claim = %v", got)
	}
	var secondGeneration int64
	if err := pool.QueryRow(ctx, `select claim_generation from trigger_records where id=$1`, secondID).Scan(&secondGeneration); err != nil {
		t.Fatal(err)
	}
	if err := store.CompleteClaimedTriggerRecord(ctx, secondID, secondGeneration); err != nil {
		t.Fatal(err)
	}
	if due, err := store.ListDueInvocationsAfter(ctx, time.Now(), state.InvocationDueCursor{}, 64); err != nil || len(due) != 1 || due[0].ID != later.ID {
		t.Fatalf("invocation did not follow broker claims: %v, %v", due, err)
	}
}

func TestPgPendingTriggerDeadLetterCannotOverrideClaim(t *testing.T) {
	store, pool, ctx := pgStoreWithPool(t)
	accountID, appID, _ := seedLiveDeploy(t, store, ctx)
	policy, err := store.UpsertAppWorkPolicy(ctx, accountID, appID,
		workpolicy.Policy{Name: "rate-limit", MaxRunningPerKey: 1})
	if err != nil {
		t.Fatal(err)
	}
	trigger, err := store.CreateTriggerIfUnderQuota(ctx, appID, "kafka", "rate-limit", false,
		[]byte(`{}`), "", 10, 1000, 3, 1<<20, "commit", api.MustLimitsFor(api.PlanPro))
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range []string{"pending", "claimed"} {
		if _, err := store.InsertKeyedTriggerRecord(ctx, trigger.ID.String(), item,
			[]byte(`{"order_id":"one"}`), nil, nil, policy, "s:"+item); err != nil {
			t.Fatal(err)
		}
	}
	claimed, err := store.ClaimTriggerRecordsByItems(ctx, trigger.ID.String(), []string{"claimed"})
	if err != nil || len(claimed) != 1 {
		t.Fatalf("claim = %v, %v", claimed, err)
	}
	if changed, err := store.RoutePendingTriggerDeadLetterByItem(ctx, trigger.ID.String(), "claimed",
		"rate_limited", []byte("denied")); err != nil || changed {
		t.Fatalf("active claim was dead-lettered: changed=%v err=%v", changed, err)
	}
	if changed, err := store.RoutePendingTriggerDeadLetterByItem(ctx, trigger.ID.String(), "pending",
		"rate_limited", []byte("denied")); err != nil || !changed {
		t.Fatalf("pending dead-letter = changed=%v err=%v", changed, err)
	}
	var pendingState, claimedState string
	var claimedDLQ int
	if err := pool.QueryRow(ctx, `select state from trigger_records where trigger_id=$1 and item_identifier='pending'`,
		trigger.ID).Scan(&pendingState); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `select r.state,
		(select count(*) from trigger_dead_letter d where d.record_id=r.id)
		from trigger_records r where r.trigger_id=$1 and r.item_identifier='claimed'`,
		trigger.ID).Scan(&claimedState, &claimedDLQ); err != nil {
		t.Fatal(err)
	}
	if pendingState != "dead_letter" || claimedState != "claimed" || claimedDLQ != 0 {
		t.Fatalf("states pending=%s claimed=%s claimedDLQ=%d", pendingState, claimedState, claimedDLQ)
	}
}
