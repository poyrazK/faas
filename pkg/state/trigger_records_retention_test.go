package state_test

import (
	"slices"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestDeleteExpiredTriggerRecordsAppliesDefaultAges(t *testing.T) {
	s, pool, ctx := pgStoreWithPool(t)
	_, appID, _ := seedLiveDeploy(t, s, ctx, "-trigger-retention")
	limits, _ := api.LimitsFor(api.PlanPro)
	trigger, err := s.CreateTriggerIfUnderQuota(ctx, appID, string(api.TriggerKindQueue), "jobs", false,
		[]byte(`{"mode":"queue"}`), "queue", 1, 1000, 3, 6*1024*1024, api.BrokerPoisonStrategyCommit, limits)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	day := 24 * time.Hour
	rows := []struct {
		item       string
		state      string
		dispatched time.Duration // age of last dispatch
		pinned     *time.Duration
		workPolicy bool
		reaped     bool
	}{
		{item: "fresh-succeeded", state: "succeeded", dispatched: day},
		{item: "old-succeeded", state: "succeeded", dispatched: 8 * day, reaped: true},
		{item: "old-superseded", state: "superseded", dispatched: 8 * day, reaped: true},
		{item: "pending-stays", state: "pending", dispatched: 60 * day},
		{item: "dead-letter-kept", state: "dead_letter", dispatched: 8 * day},
		{item: "old-dead-letter", state: "dead_letter", dispatched: 31 * day, reaped: true},
		{item: "pinned-past", state: "succeeded", dispatched: day, pinned: new(-time.Hour), reaped: true},
		{item: "pinned-future", state: "succeeded", dispatched: 60 * day, pinned: new(day)},
		{item: "work-policy-kept", state: "succeeded", dispatched: 60 * day, workPolicy: true},
	}
	for _, r := range rows {
		id, err := s.InsertTriggerRecord(ctx, trigger.ID.String(), r.item, []byte(`{}`), []byte(`{}`), []byte(`{}`))
		if err != nil {
			t.Fatal(err)
		}
		var pinned *time.Time
		if r.pinned != nil {
			at := now.Add(*r.pinned)
			pinned = &at
		}
		if _, err := pool.Exec(ctx, `update trigger_records set state=$2, last_dispatched_at=$3, result_retention_until=$4 where id=$1`,
			id, r.state, now.Add(-r.dispatched), pinned); err != nil {
			t.Fatal(err)
		}
		if r.workPolicy {
			if _, err := pool.Exec(ctx, `update trigger_records set work_policy_name='p', work_key_digest=decode(repeat('ab',32),'hex'),
 work_sequence=1, work_policy_revision=1 where id=$1`, id); err != nil {
				t.Fatal(err)
			}
		}
	}
	deleted, err := s.DeleteExpiredTriggerRecords(ctx, now, 2)
	if err != nil {
		t.Fatal(err)
	}
	if deleted != 2 {
		t.Fatalf("first batch deleted %d, want the batch limit 2", deleted)
	}
	for {
		n, err := s.DeleteExpiredTriggerRecords(ctx, now, 2)
		if err != nil {
			t.Fatal(err)
		}
		if n == 0 {
			break
		}
	}
	remaining := []string{}
	rowsLeft, err := pool.Query(ctx, `select item_identifier from trigger_records where trigger_id=$1 order by item_identifier`, trigger.ID.String())
	if err != nil {
		t.Fatal(err)
	}
	for rowsLeft.Next() {
		var item string
		if err := rowsLeft.Scan(&item); err != nil {
			t.Fatal(err)
		}
		remaining = append(remaining, item)
	}
	rowsLeft.Close()
	var want []string
	for _, r := range rows {
		if !r.reaped {
			want = append(want, r.item)
		}
	}
	slices.Sort(want)
	if !slices.Equal(remaining, want) {
		t.Fatalf("remaining = %v, want %v", remaining, want)
	}
}
