// adr: 933
package durableentity

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

func committedOutboxFixture(t *testing.T, count int) (fixture, OutboxMessage) {
	t.Helper()
	f := newFixture(t)
	intents := make([]OutboxIntent, count)
	for i := range intents {
		intents[i] = outboxIntent()
	}
	if _, err := f.manager.Execute(t.Context(), f.claim, request("confirmation"), withOutbox(intents...)); err != nil {
		t.Fatal(err)
	}
	return f, pendingOutbox(t, f.manager, f.id, 1, count).Messages[0]
}

func TestOutboxAcknowledgementPreservesStateReceiptsAlarmAndAccounting(t *testing.T) {
	f, message := committedOutboxFixture(t, 2)
	reservation, err := f.manager.ReserveOutbox(t.Context(), f.claim, message.ID)
	if err != nil {
		t.Fatal(err)
	}
	at := time.Unix(0, f.clock.Load()).Add(-time.Second)
	if _, err := f.manager.Execute(t.Context(), f.claim, request("ordinary"), func(ctx context.Context, view View) (Transition, error) {
		transition, err := increment(ctx, view)
		transition.AlarmAt = &at
		return transition, err
	}); err != nil {
		t.Fatal(err)
	}
	if err := f.manager.reserveAlarmAttempt(t.Context(), f.claim, Alarm{Entity: f.id, Version: 2, At: at}); err != nil {
		t.Fatal(err)
	}
	before, _, err := f.manager.readManifest(t.Context(), f.id)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.manager.SetStorageLimit(t.Context(), f.claim, 1); err != nil {
		t.Fatal(err)
	}
	if err := f.manager.AcknowledgeOutbox(t.Context(), f.claim, message.ID, reservation.Token); err != nil {
		t.Fatal(err)
	}
	after, _, err := f.manager.readManifest(t.Context(), f.id)
	if err != nil || after.Version != 2 || after.OutboxDelivery != nil || after.AlarmDelivery == nil || after.AlarmDelivery.Attempts != 1 ||
		after.StorageUsage.ReceiptCount != before.StorageUsage.ReceiptCount || after.StorageUsage.TotalBytes() >= before.StorageUsage.TotalBytes() || !sameUsage(*after.StorageUsage, reachableUsage(t, f)) {
		t.Fatal("ack changed business/receipt/alarm authority or accounting", after, err)
	}
	assertCount(t, t.Context(), f.manager, f.id, 2, 2)
	pendingOutbox(t, f.manager, f.id, 2, 1)
	result, err := f.manager.Execute(t.Context(), f.claim, request("confirmation"), withOutbox(outboxIntent()))
	if err != nil || !result.Replayed || result.Version != 1 {
		t.Fatal(result, err)
	}
	collectAll(t, f.manager, f.claim)
	restarted := openManager(t, f.store, f.clock)
	pendingOutbox(t, restarted, f.id, 2, 1)
	if err := restarted.AcknowledgeOutbox(t.Context(), f.claim, message.ID, reservation.Token); !errors.Is(err, ErrOutboxObsolete) {
		t.Fatal("old acknowledgement removed the next head", err)
	}
}

func TestOutboxUncertainReservationNeverAuthorizesAcceptance(t *testing.T) {
	for _, stage := range []string{"rejected", "before-write", "lost-ack"} {
		t.Run(stage, func(t *testing.T) {
			f, message := committedOutboxFixture(t, 1)
			if err := f.manager.Release(t.Context(), f.claim); err != nil {
				t.Fatal(err)
			}
			f.manager.store = wrappedStore{ObjectStore: f.store, put: func(ctx context.Context, key string, body []byte, etag string) (string, error) {
				var value manifest
				reservation := strings.HasSuffix(key, "/manifest.json") && json.Unmarshal(body, &value) == nil && value.OutboxDelivery != nil && value.OwnerID != ""
				if reservation && stage == "rejected" {
					return "", ErrConflict
				}
				if reservation && stage == "before-write" {
					return "", errors.New("injected failure")
				}
				version, err := f.store.Put(ctx, key, body, etag)
				if reservation && stage == "lost-ack" && err == nil {
					return "", errors.New("lost reservation acknowledgement")
				}
				return version, err
			}}
			calls := 0
			err := f.manager.RelayOutbox(t.Context(), f.id, message.ID, "relay", func(context.Context, OutboxMessage) error { calls++; return nil })
			if calls != 0 || stage == "rejected" && !errors.Is(err, ErrConflict) || stage != "rejected" && !errors.Is(err, ErrUncertain) {
				t.Fatal(calls, err)
			}
			status, err := f.manager.InspectOutbox(t.Context(), f.id)
			want := 0
			if stage == "lost-ack" {
				want = 1
			}
			if err != nil || status.Attempts != want || status.Pending != 1 {
				t.Fatal(status, err)
			}
			f.clock.Add(int64(api.MaxDurableEntityLease + time.Second))
			restarted := openManager(t, f.store, f.clock)
			if err := restarted.RelayOutbox(t.Context(), f.id, message.ID, "restarted", func(context.Context, OutboxMessage) error { calls++; return nil }); err != nil {
				t.Fatal(err)
			}
			if calls != 1 {
				t.Fatal(calls)
			}
			pendingOutbox(t, restarted, f.id, 1, 0)
		})
	}
}

func TestOutboxAcknowledgementFailuresLeaveRestartableWork(t *testing.T) {
	for _, stage := range []string{"snapshot-ack", "manifest-rejection", "manifest-before-write", "manifest-ack"} {
		t.Run(stage, func(t *testing.T) {
			f, message := committedOutboxFixture(t, 1)
			reservation, err := f.manager.ReserveOutbox(t.Context(), f.claim, message.ID)
			if err != nil {
				t.Fatal(err)
			}
			accepted := map[string]int{message.ID: 1} // Transport accepted before process/ACK failure.
			f.manager.store = wrappedStore{ObjectStore: f.store, put: func(ctx context.Context, key string, body []byte, etag string) (string, error) {
				if strings.HasSuffix(key, "/manifest.json") && stage == "manifest-rejection" {
					return "", ErrConflict
				}
				if strings.HasSuffix(key, "/manifest.json") && stage == "manifest-before-write" {
					return "", errors.New("injected failure")
				}
				version, err := f.store.Put(ctx, key, body, etag)
				if err == nil && (stage == "snapshot-ack" && strings.Contains(key, "/snapshots/") || stage == "manifest-ack" && strings.HasSuffix(key, "/manifest.json")) {
					return "", errors.New("lost acknowledgement")
				}
				return version, err
			}}
			want := ErrUncertain
			if stage == "manifest-rejection" {
				want = ErrConflict
			}
			if err := f.manager.AcknowledgeOutbox(t.Context(), f.claim, message.ID, reservation.Token); !errors.Is(err, want) {
				t.Fatal(err)
			}
			f.clock.Add(int64(api.MaxDurableEntityLease + time.Second))
			restarted := openManager(t, f.store, f.clock)
			page, err := restarted.ScanDueOutbox(t.Context(), "")
			if err != nil || page.Failed != 0 {
				t.Fatal(page, err)
			}
			if stage == "manifest-ack" && len(page.Work) != 0 || stage != "manifest-ack" && len(page.Work) != 1 {
				t.Fatal(page)
			}
			for _, work := range page.Work {
				if err := restarted.RelayOutbox(t.Context(), work.Entity, work.MessageID, "restarted", func(_ context.Context, message OutboxMessage) error {
					if accepted[message.ID] == 0 {
						accepted[message.ID]++
					}
					return nil
				}); err != nil {
					t.Fatal(err)
				}
			}
			if accepted[message.ID] != 1 {
				t.Fatal("transport acceptance duplicated", accepted)
			}
			pendingOutbox(t, restarted, f.id, 1, 0)
			assertCount(t, t.Context(), restarted, f.id, 1, 1)
		})
	}
}

func TestOutboxExhaustionAndOperatorReplayRetainIdentity(t *testing.T) {
	f, message := committedOutboxFixture(t, 2)
	f.manager.lease = api.MaxDurableEntityLease
	var previous OutboxReservation
	for attempt := 1; attempt <= api.MaxDurableEntityOutboxAttempts; attempt++ {
		claim, err := f.manager.Renew(t.Context(), f.claim)
		if err != nil {
			t.Fatal(err)
		}
		f.claim = claim
		reservation, err := f.manager.ReserveOutbox(t.Context(), f.claim, message.ID)
		if err != nil {
			t.Fatal(attempt, err)
		}
		if previous.Token != "" && reservation.Token == previous.Token {
			t.Fatal("attempt token reused")
		}
		previous = reservation
		if _, err := f.manager.ReserveOutbox(t.Context(), f.claim, message.ID); attempt < api.MaxDurableEntityOutboxAttempts && !errors.Is(err, ErrOutboxBackoff) || attempt == api.MaxDurableEntityOutboxAttempts && !errors.Is(err, ErrOutboxExhausted) {
			t.Fatal(err)
		}
		if attempt < api.MaxDurableEntityOutboxAttempts {
			f.clock.Add(int64(outboxRetryDelay(attempt)))
		}
	}
	status, err := f.manager.InspectOutbox(t.Context(), f.id)
	if err != nil || !status.Exhausted || status.Pending != 2 || status.Attempts != api.MaxDurableEntityOutboxAttempts {
		t.Fatal(status, err)
	}
	if page, err := f.manager.ScanDueOutbox(t.Context(), ""); err != nil || len(page.Work) != 0 {
		t.Fatal(page, err)
	}
	if err := f.manager.RetryOutbox(t.Context(), f.claim, uuid.NewString()); !errors.Is(err, ErrOutboxObsolete) {
		t.Fatal(err)
	}
	if err := f.manager.RetryOutbox(t.Context(), f.claim, message.ID); err != nil {
		t.Fatal(err)
	}
	if err := f.manager.AcknowledgeOutbox(t.Context(), f.claim, message.ID, previous.Token); !errors.Is(err, ErrOutboxObsolete) {
		t.Fatal("pre-replay attempt retained authority", err)
	}
	reservation, err := f.manager.ReserveOutbox(t.Context(), f.claim, message.ID)
	if err != nil || reservation.Message.ID != message.ID {
		t.Fatal(reservation, err)
	}
	if err := f.manager.AcknowledgeOutbox(t.Context(), f.claim, message.ID, reservation.Token); err != nil {
		t.Fatal(err)
	}
	pendingOutbox(t, f.manager, f.id, 1, 1)
}

func TestOutboxTakeoverAtAcknowledgementCASPreservesPendingWork(t *testing.T) {
	f, message := committedOutboxFixture(t, 1)
	reservation, err := f.manager.ReserveOutbox(t.Context(), f.claim, message.ID)
	if err != nil {
		t.Fatal(err)
	}
	winner := openManager(t, f.store, f.clock)
	f.manager.store = wrappedStore{ObjectStore: f.store, put: func(ctx context.Context, key string, body []byte, etag string) (string, error) {
		if strings.HasSuffix(key, "/manifest.json") {
			f.clock.Add(int64(api.MaxDurableEntityLease + time.Second))
			claim, err := winner.Acquire(ctx, f.id, "winner")
			if err != nil {
				t.Fatal(err)
			}
			if _, err := winner.Execute(ctx, claim, request("winner"), increment); err != nil {
				t.Fatal(err)
			}
		}
		return f.store.Put(ctx, key, body, etag)
	}}
	if err := f.manager.AcknowledgeOutbox(t.Context(), f.claim, message.ID, reservation.Token); !errors.Is(err, ErrConflict) {
		t.Fatal(err)
	}
	pendingOutbox(t, winner, f.id, 2, 1)
	assertCount(t, t.Context(), winner, f.id, 2, 2)
	if err := winner.AcknowledgeOutbox(t.Context(), f.claim, message.ID, reservation.Token); !errors.Is(err, ErrStaleOwner) {
		t.Fatal(err)
	}
}

func TestOutboxEncodedPayloadExpansionRejectedBeforePublication(t *testing.T) {
	f := newFixture(t)
	intent := outboxIntent()
	intent.Payload = json.RawMessage(`"` + strings.Repeat("<", api.MaxDurableEntityOutboxPayloadBytes/2) + `"`)
	if _, err := f.manager.Execute(t.Context(), f.claim, request("escaped"), withOutbox(intent)); !errors.Is(err, ErrLimit) {
		t.Fatal(err)
	}
	pendingOutbox(t, f.manager, f.id, 0, 0)
}

func TestOutboxAcknowledgementRejectsCleanupBarrierDuringUpload(t *testing.T) {
	f, message := committedOutboxFixture(t, 1)
	reservation, err := f.manager.ReserveOutbox(t.Context(), f.claim, message.ID)
	if err != nil {
		t.Fatal(err)
	}
	collector := openManager(t, f.store, f.clock)
	f.manager.store = wrappedStore{ObjectStore: f.store, put: func(ctx context.Context, key string, body []byte, etag string) (string, error) {
		version, err := f.store.Put(ctx, key, body, etag)
		if err == nil && strings.Contains(key, "/snapshots/") {
			if _, err := collector.Collect(ctx, f.claim, ""); err != nil {
				t.Fatal(err)
			}
		}
		return version, err
	}}
	if err := f.manager.AcknowledgeOutbox(t.Context(), f.claim, message.ID, reservation.Token); !errors.Is(err, ErrConflict) {
		t.Fatal("acknowledgement crossed reclamation barrier", err)
	}
	pendingOutbox(t, collector, f.id, 1, 1)
	f.manager.store = f.store
	if err := f.manager.AcknowledgeOutbox(t.Context(), f.claim, message.ID, reservation.Token); err != nil {
		t.Fatal(err)
	}
	pendingOutbox(t, f.manager, f.id, 1, 0)
	assertCount(t, t.Context(), f.manager, f.id, 1, 1)
}
