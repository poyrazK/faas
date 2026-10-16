package operatorobs

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestAlertWorkloadSummaryAgesAndFreshness(t *testing.T) {
	store := state.NewMemStore()
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Microsecond)
	never, err := AlertWorkloadSummary(ctx, store, true, now)
	if err != nil || never.Counts != nil || never.Groups == nil || never.Collection.CollectionState != "never_collected" {
		t.Fatalf("never: %+v %v", never, err)
	}
	quiet := state.NewMemStore()
	quietSnapshot, err := alertSnapshot(historyRules(now, false), now)
	if err != nil {
		t.Fatal(err)
	}
	if err := quiet.RecordOperatorAlertSnapshot(ctx, quietSnapshot); err != nil {
		t.Fatal(err)
	}
	zero, err := AlertWorkloadSummary(ctx, quiet, true, now)
	if err != nil || zero.Counts == nil || zero.Counts.Total != 0 || len(zero.Groups) != 0 || zero.Collection.CollectionState != "current" {
		t.Fatalf("known quiet: %+v %v", zero, err)
	}
	failedBeforeFirst := state.NewMemStore()
	if err := failedBeforeFirst.RecordOperatorAlertFailure(ctx, now); err != nil {
		t.Fatal(err)
	}
	noSuccess, err := AlertWorkloadSummary(ctx, failedBeforeFirst, true, now)
	if err != nil || noSuccess.Counts != nil || noSuccess.Collection.CollectionState != "failed" {
		t.Fatalf("failed before first: %+v %v", noSuccess, err)
	}
	snapshot, err := alertSnapshot(historyRules(now, true), now)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.RecordOperatorAlertSnapshot(ctx, snapshot); err != nil {
		t.Fatal(err)
	}
	id := snapshot.Episodes[0].ID
	first, err := AlertWorkloadSummary(ctx, store, true, now)
	if err != nil || first.Counts == nil || first.Counts.NeedsAttention != 1 || first.Counts.Firing != 1 || first.OldestUnacknowledged == nil || first.OldestUnowned == nil || first.OldestUnowned.AgeSeconds != 60 || first.OldestUnowned.EvidencePath != EvidencePath("platform_alert", id) {
		t.Fatalf("first: %+v %v", first, err)
	}
	actor := "11111111-1111-4111-8111-111111111111"
	for i, action := range []string{"acknowledge", "claim"} {
		if _, err := store.MutateOperatorAlertTriage(ctx, state.OperatorAlertTriageMutation{EpisodeID: id, ActorID: actor, Action: action, ExpectedRevision: int64(i), Now: now}); err != nil {
			t.Fatal(err)
		}
	}
	handled, err := AlertWorkloadSummary(ctx, store, true, now)
	if err != nil || handled.Counts.NeedsAttention != 0 || handled.OldestUnacknowledged != nil || handled.OldestUnowned != nil {
		t.Fatalf("handled: %+v %v", handled, err)
	}
	until := now.Add(time.Minute)
	if _, err := store.MutateOperatorAlertTriage(ctx, state.OperatorAlertTriageMutation{EpisodeID: id, ActorID: actor, Action: "release", ExpectedRevision: 2, Now: now}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.MutateOperatorAlertTriage(ctx, state.OperatorAlertTriageMutation{EpisodeID: id, ActorID: actor, Action: "snooze", ExpectedRevision: 3, Now: now, SnoozedUntil: &until}); err != nil {
		t.Fatal(err)
	}
	snoozed, err := AlertWorkloadSummary(ctx, store, true, now)
	if err != nil || snoozed.Counts.NeedsAttention != 0 || snoozed.OldestUnowned != nil {
		t.Fatalf("snoozed: %+v %v", snoozed, err)
	}
	expired, err := AlertWorkloadSummary(ctx, store, true, until)
	if err != nil || expired.Counts.NeedsAttention != 1 || expired.OldestUnowned == nil || expired.OldestUnowned.AgeSeconds != 120 {
		t.Fatalf("expired: %+v %v", expired, err)
	}
	if err := store.RecordOperatorAlertFailure(ctx, now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	failed, err := AlertWorkloadSummary(ctx, store, true, until)
	if err != nil || failed.Collection.CollectionState != "failed" || failed.Counts.Unknown != 1 || failed.Counts.NeedsAttention != 1 || failed.OldestUnowned.Status != "unknown" {
		t.Fatalf("failed: %+v %v", failed, err)
	}
	disabled, err := AlertWorkloadSummary(ctx, store, false, until)
	if err != nil || disabled.Collection.CollectionState != "disabled" || disabled.Counts.Unknown != 1 {
		t.Fatalf("disabled: %+v %v", disabled, err)
	}
	body, err := json.Marshal(disabled)
	if err != nil || strings.Contains(string(body), actor) || strings.Contains(string(body), "owner_account_id") {
		t.Fatalf("identity: %s %v", body, err)
	}
}

func TestAlertWorkloadOldestTieAndClock(t *testing.T) {
	now := time.Now().UTC()
	a := state.OperatorAlertWorkloadBucket{OldestID: strings.Repeat("a", 64), OldestStartedAt: now.Add(-time.Minute), Status: "unknown", Severity: "critical"}
	b := a
	b.OldestID = strings.Repeat("b", 64)
	oldest := olderWorkload(olderWorkload(nil, b, now), a, now)
	if oldest.IncidentID != "platform_alert/"+a.OldestID {
		t.Fatalf("tie: %+v", oldest)
	}
	b.OldestStartedAt = now.Add(time.Second)
	future := olderWorkload(nil, b, now)
	if future.AgeSeconds != 0 {
		t.Fatalf("negative age: %+v", future)
	}
	// Shared deadline is passed through to the reader rather than detached.
	probe := &workloadBudgetProbe{Store: state.NewMemStore()}
	if _, err := AlertWorkloadSummary(context.Background(), probe, true, now); err != nil || !probe.bounded {
		t.Fatalf("budget: %v %t", err, probe.bounded)
	}
}

type workloadBudgetProbe struct {
	Store   state.Store
	bounded bool
}

func (p *workloadBudgetProbe) OperatorAlertWorkload(ctx context.Context, f state.OperatorAlertWorkloadFilter) (state.OperatorAlertWorkloadSnapshot, error) {
	deadline, ok := ctx.Deadline()
	p.bounded = ok && time.Until(deadline) <= api.ObsAlertWorkloadBudget
	return p.Store.OperatorAlertWorkload(ctx, f)
}
