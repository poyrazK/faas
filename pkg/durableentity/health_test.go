// adr: 937
package durableentity

import (
	"reflect"
	"testing"
)

func TestHealthIncludesExhaustedWorkWithoutWrites(t *testing.T) {
	for _, target := range []string{"alarm", "outbox"} {
		t.Run(target, func(t *testing.T) {
			f, request := exhaustedRecoveryFixture(t, target)
			before := map[string]memoryObject{}
			f.store.mu.Lock()
			for key, value := range f.store.objects {
				before[key] = value
			}
			f.store.mu.Unlock()
			page, err := f.manager.ScanHealth(t.Context(), "", func(id ID) bool { return id == f.id })
			if err != nil || page.Failed != 0 || page.NextCursor != "" || len(page.Samples) != 1 {
				t.Fatal(page, err)
			}
			sample := page.Samples[0]
			if target == "alarm" && (!sample.AlarmExhausted || !sample.AlarmPending || !sample.AlarmAt.Equal(*request.AlarmAt)) || target == "outbox" && (!sample.OutboxExhausted || sample.OutboxPending != 2 || sample.OutboxUnknownAge != 0 || sample.OldestOutboxAt == nil) {
				t.Fatal(sample)
			}
			excluded, err := f.manager.ScanHealth(t.Context(), "", func(ID) bool { return false })
			if err != nil || len(excluded.Samples) != 0 {
				t.Fatal(excluded, err)
			}
			f.store.mu.Lock()
			defer f.store.mu.Unlock()
			if !reflect.DeepEqual(before, f.store.objects) {
				t.Fatal("health scan changed bucket state")
			}
		})
	}
}

func TestHealthLegacyAgeIsUnknownAndCorruptStateFailsClosed(t *testing.T) {
	f, message := committedOutboxFixture(t, 2)
	base, _, err := f.manager.readManifest(t.Context(), f.id)
	if err != nil {
		t.Fatal(err)
	}
	state, err := f.manager.readSnapshot(t.Context(), base)
	if err != nil {
		t.Fatal(err)
	}
	state.Outbox[0].CommittedAt = nil
	if !validOutbox(state) {
		t.Fatal("legacy message rejected")
	}
	sample := healthSample(base, state)
	if sample.OutboxUnknownAge != 1 || sample.OldestOutboxAt == nil {
		t.Fatal(message, sample)
	}
	for i := range state.Outbox {
		state.Outbox[i].CommittedAt = nil
	}
	sample = healthSample(base, state)
	if sample.OutboxUnknownAge != 2 || sample.OldestOutboxAt != nil {
		t.Fatal(sample)
	}
	f.store.mu.Lock()
	delete(f.store.objects, base.SnapshotKey)
	f.store.mu.Unlock()
	page, err := f.manager.ScanHealth(t.Context(), "", func(ID) bool { return true })
	if err != nil || page.Failed != 1 || len(page.Samples) != 0 {
		t.Fatal(page, err)
	}
}

func TestOutboxTimestampSurvivesReplayAndUnrelatedCommit(t *testing.T) {
	f, message := committedOutboxFixture(t, 1)
	if message.CommittedAt == nil {
		t.Fatal("new committed work has no timestamp")
	}
	at := *message.CommittedAt
	f.clock.Add(1)
	if _, err := f.manager.Execute(t.Context(), f.claim, request("ordinary"), increment); err != nil {
		t.Fatal(err)
	}
	if result, err := f.manager.Execute(t.Context(), f.claim, request("confirmation"), withOutbox(outboxIntent())); err != nil || !result.Replayed {
		t.Fatal(result, err)
	}
	pending := pendingOutbox(t, f.manager, f.id, 2, 1)
	if !pending.Messages[0].CommittedAt.Equal(at) || pending.Messages[0].ID != message.ID {
		t.Fatal("replay or state update reset age")
	}
}
