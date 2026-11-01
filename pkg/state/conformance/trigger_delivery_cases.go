package conformance

import (
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// newDeliveryTrigger creates a disabled NATS trigger on the fixture's app;
// the cases drive its records through the store directly.
func newDeliveryTrigger(t *testing.T, fx *Fixture) string {
	t.Helper()
	row, err := fx.Store.CreateTriggerIfUnderQuota(fx.Ctx, fx.App.ID, "nats", "delivery-"+uuid.NewString()[:8], false,
		[]byte(`{}`), "", 10, 1000, 5, 1024, "commit", api.MustLimitsFor(api.PlanPro))
	if err != nil {
		t.Fatal(err)
	}
	return row.ID.String()
}

// claimDeliveryRecord inserts item and claims it, returning its id and claim
// generation.
func claimDeliveryRecord(t *testing.T, fx *Fixture, triggerID, item string, metadata []byte) (string, int64) {
	t.Helper()
	id, err := fx.Store.InsertTriggerRecord(fx.Ctx, triggerID, item, []byte(`{}`), nil, metadata)
	if err != nil {
		t.Fatal(err)
	}
	claimed, err := fx.Store.(state.TriggerBatchClaimer).ClaimTriggerRecordsByItems(fx.Ctx, triggerID, []string{item})
	if err != nil || len(claimed) != 1 || claimed[0].ID.String() != id {
		t.Fatalf("claim %s = %+v, %v", item, claimed, err)
	}
	return id, claimed[0].ClaimGeneration
}

// A redelivered broker record is settled from its stored state: terminal
// records (dead letters with their reason) are found, a cooling retry reports
// when it is due, and the metadata topic maps an earlier release's item
// identifier (ADR-20261101093512).
func testTriggerRecordStatesByItems(t *testing.T, fx *Fixture) {
	triggerID := newDeliveryTrigger(t, fx)
	finisher := fx.Store.(state.TriggerClaimFinisher)
	reader, ok := fx.Store.(state.TriggerRecordStateReader)
	if !ok {
		t.Fatal("store does not read trigger record states")
	}
	doneID, doneGen := claimDeliveryRecord(t, fx, triggerID, "done", nil)
	if err := finisher.CompleteClaimedTriggerRecord(fx.Ctx, doneID, doneGen); err != nil {
		t.Fatal(err)
	}
	for item, reason := range map[string]string{"poison": "poison_record", "exhausted": "max_attempts"} {
		id, gen := claimDeliveryRecord(t, fx, triggerID, item, nil)
		if err := finisher.RouteClaimedTriggerDeadLetter(fx.Ctx, id, gen, triggerID, reason, []byte(`{}`)); err != nil {
			t.Fatal(err)
		}
	}
	due := time.Now().Add(time.Hour)
	coolingID, coolingGen := claimDeliveryRecord(t, fx, triggerID, "cooling", nil)
	if err := finisher.RetryClaimedTriggerRecord(fx.Ctx, coolingID, coolingGen, "handler failed", due); err != nil {
		t.Fatal(err)
	}
	if _, err := fx.Store.InsertTriggerRecord(fx.Ctx, triggerID, "fresh", []byte(`{}`), nil, []byte(`{"topic":"orders","partition":0}`)); err != nil {
		t.Fatal(err)
	}
	rows, err := reader.TriggerRecordStatesByItems(fx.Ctx, triggerID,
		[]string{"done", "poison", "exhausted", "cooling", "fresh", "missing"})
	if err != nil {
		t.Fatal(err)
	}
	got := make(map[string]state.TriggerRecordItemState, len(rows))
	for _, row := range rows {
		got[row.Item] = row
	}
	if len(got) != 5 {
		t.Fatalf("states = %+v, want five stored records", rows)
	}
	want := map[string][2]string{
		"done": {"succeeded", ""}, "poison": {"dead_letter", "poison_record"},
		"exhausted": {"dead_letter", "max_attempts"}, "cooling": {"retry", ""}, "fresh": {"pending", ""},
	}
	for item, w := range want {
		if got[item].State != w[0] || got[item].DeadLetterReason != w[1] {
			t.Fatalf("%s = %+v, want state %s reason %q", item, got[item], w[0], w[1])
		}
	}
	if d := got["cooling"].NextFireAt.Sub(due); d < -time.Second || d > time.Second {
		t.Fatalf("cooling next_fire_at = %s, want %s", got["cooling"].NextFireAt, due)
	}
	if got["fresh"].Topic != "orders" || got["done"].Topic != "" {
		t.Fatalf("topics = %q %q, want orders and none", got["fresh"].Topic, got["done"].Topic)
	}
}

// A running batch renews the claims it holds; a stale generation, a settled
// record or another trigger's record is not renewed (ADR-20261101093512).
func testTriggerRecordClaimRenewal(t *testing.T, fx *Fixture) {
	triggerID, otherID := newDeliveryTrigger(t, fx), newDeliveryTrigger(t, fx)
	renewer, ok := fx.Store.(state.TriggerClaimRenewer)
	if !ok {
		t.Fatal("store does not renew trigger claims")
	}
	liveID, liveGen := claimDeliveryRecord(t, fx, triggerID, "live", nil)
	doneID, doneGen := claimDeliveryRecord(t, fx, triggerID, "done", nil)
	if err := fx.Store.(state.TriggerClaimFinisher).CompleteClaimedTriggerRecord(fx.Ctx, doneID, doneGen); err != nil {
		t.Fatal(err)
	}
	n, err := renewer.RenewTriggerRecordClaims(fx.Ctx, triggerID,
		[]string{liveID, liveID, doneID}, []int64{liveGen, liveGen + 1, doneGen}, time.Hour)
	if err != nil || n != 1 {
		t.Fatalf("renewed %d, %v; want only the live claim", n, err)
	}
	if n, err := renewer.RenewTriggerRecordClaims(fx.Ctx, otherID, []string{liveID}, []int64{liveGen}, time.Hour); err != nil || n != 0 {
		t.Fatalf("renewed %d through another trigger, %v", n, err)
	}
	if _, err := renewer.RenewTriggerRecordClaims(fx.Ctx, triggerID, []string{liveID}, nil, time.Hour); err == nil {
		t.Fatal("mismatched generations accepted")
	}
	// The renewed claim still settles under its generation.
	if err := fx.Store.(state.TriggerClaimFinisher).CompleteClaimedTriggerRecord(fx.Ctx, liveID, liveGen); err != nil {
		t.Fatalf("complete renewed claim: %v", err)
	}
}
