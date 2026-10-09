// adr: 843
package durableentity

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func outboxIntent() OutboxIntent {
	return OutboxIntent{WebhookID: "6dd283da-3c14-40de-9d47-bb71fb35be9a", EventType: "reservation.confirmed", Payload: json.RawMessage(`{"reservation":"123"}`)}
}

func withOutbox(intents ...OutboxIntent) func(context.Context, View) (Transition, error) {
	return func(ctx context.Context, view View) (Transition, error) {
		transition, err := increment(ctx, view)
		transition.Outbox = intents
		return transition, err
	}
}

func pendingOutbox(t *testing.T, m *Manager, id ID, version uint64, count int) OutboxView {
	t.Helper()
	view, err := m.PendingOutbox(t.Context(), id)
	if err != nil || view.Version != version || len(view.Messages) != count || view.Messages == nil {
		t.Fatalf("pending outbox = %+v, %v", view, err)
	}
	return view
}

func TestOutboxSurvivesStateChangesRestartReplayAndCleanup(t *testing.T) {
	f := newFixture(t)
	pendingOutbox(t, f.manager, f.id, 0, 0)
	alarm := time.Unix(0, f.clock.Load()).Add(time.Hour)
	first, err := f.manager.Execute(t.Context(), f.claim, request("reservation"), func(ctx context.Context, v View) (Transition, error) {
		transition, err := withOutbox(outboxIntent(), outboxIntent())(ctx, v)
		transition.AlarmAt = &alarm
		return transition, err
	})
	if err != nil || first.Replayed {
		t.Fatal(first, err)
	}
	original := pendingOutbox(t, f.manager, f.id, 1, 2)
	if original.Messages[0].ID == original.Messages[1].ID {
		t.Fatal("distinct batch ordinals shared a delivery identity")
	}
	if _, err := f.manager.Execute(t.Context(), f.claim, request("ordinary"), increment); err != nil {
		t.Fatal(err)
	}
	if _, err := f.manager.Execute(t.Context(), f.claim, request("second-batch"), withOutbox(outboxIntent())); err != nil {
		t.Fatal(err)
	}
	if deleted := collectAll(t, f.manager, f.claim); deleted < 2 {
		t.Fatal("superseded snapshots were retained", deleted)
	}
	usage := inventoryAll(t, f, f.store)
	if !sameUsage(usage.Usage, reachableUsage(t, f)) {
		t.Fatal("outbox snapshot bytes escaped committed accounting", usage)
	}
	if err := f.manager.Release(t.Context(), f.claim); err != nil {
		t.Fatal(err)
	}
	restarted := openManager(t, f.store, f.clock)
	claim, err := restarted.Acquire(t.Context(), f.id, "restarted")
	if err != nil {
		t.Fatal(err)
	}
	replayed, err := restarted.Execute(t.Context(), claim, request("reservation"), func(context.Context, View) (Transition, error) {
		t.Fatal("receipt replay recomputed outgoing work")
		return Transition{}, nil
	})
	if err != nil || !replayed.Replayed || replayed.Version != first.Version || string(replayed.Value) != string(first.Value) {
		t.Fatal(replayed, err)
	}
	pending := pendingOutbox(t, restarted, f.id, 3, 3)
	for i := range 2 {
		if pending.Messages[i].ID != original.Messages[i].ID || string(pending.Messages[i].Intent.Payload) != string(original.Messages[i].Intent.Payload) {
			t.Fatal("committed work changed across restart/cleanup", pending)
		}
	}
	if pending.Messages[2].ID == pending.Messages[0].ID || pending.Messages[2].Version != 3 {
		t.Fatal("later batch reused an original identity", pending)
	}
	pending.Messages[0].Intent.Payload[0] = 'x'
	if fresh := pendingOutbox(t, restarted, f.id, 3, 3); !json.Valid(fresh.Messages[0].Intent.Payload) {
		t.Fatal("inspection changed committed work")
	}
	assertCount(t, t.Context(), restarted, f.id, 3, 3)
	view, err := restarted.Read(t.Context(), f.id)
	if err != nil || view.AlarmAt == nil || !view.AlarmAt.Equal(alarm) {
		t.Fatal(view, err)
	}
}

func TestOutboxRejectsInvalidAndUnboundedWorkBeforeUploads(t *testing.T) {
	for _, kind := range []string{"webhook", "event", "payload", "payload-bytes", "batch-count", "pending-count", "pending-bytes"} {
		t.Run(kind, func(t *testing.T) {
			f := newFixture(t)
			intent, want := outboxIntent(), ErrInvalid
			intents := []OutboxIntent{intent}
			switch kind {
			case "webhook":
				intents[0].WebhookID = "https://example.com/"
			case "event":
				intents[0].EventType = " "
			case "payload":
				intents[0].Payload = json.RawMessage(`{`)
			case "payload-bytes":
				intents[0].Payload = json.RawMessage(`"` + strings.Repeat("x", api.MaxDurableEntityOutboxPayloadBytes) + `"`)
				want = ErrLimit
			case "batch-count":
				intents = make([]OutboxIntent, api.MaxDurableEntityOutboxPerTransition+1)
				want = ErrLimit
			case "pending-count":
				batch := make([]OutboxIntent, api.MaxDurableEntityOutboxPerTransition)
				for i := range batch {
					batch[i] = intent
				}
				for i := range api.MaxDurableEntityOutboxPending / len(batch) {
					if _, err := f.manager.Execute(t.Context(), f.claim, request(fmt.Sprint(i)), withOutbox(batch...)); err != nil {
						t.Fatal(err)
					}
				}
				want = ErrLimit
			case "pending-bytes":
				intent.Payload = json.RawMessage(`"` + strings.Repeat("x", api.MaxDurableEntityOutboxPayloadBytes-2) + `"`)
				intents = []OutboxIntent{intent, intent, intent, intent}
				want = ErrLimit
			}
			before, err := f.manager.PendingOutbox(t.Context(), f.id)
			if err != nil {
				t.Fatal(err)
			}
			f.manager.store = wrappedStore{ObjectStore: f.store, put: func(context.Context, string, []byte, string) (string, error) {
				t.Fatal("invalid or over-budget work dispatched an upload")
				return "", ErrInvalid
			}}
			if _, err := f.manager.Execute(t.Context(), f.claim, request("rejected"), withOutbox(intents...)); !errors.Is(err, want) {
				t.Fatalf("error = %v, want %v", err, want)
			}
			pendingOutbox(t, f.manager, f.id, before.Version, len(before.Messages))
			assertCount(t, t.Context(), f.manager, f.id, int(before.Version), before.Version)
		})
	}
}

func TestOutboxFullQueuePreservesOrdinaryWorkAndReceiptReplay(t *testing.T) {
	f := newFixture(t)
	batch := make([]OutboxIntent, api.MaxDurableEntityOutboxPerTransition)
	for i := range batch {
		batch[i] = outboxIntent()
	}
	for i := range api.MaxDurableEntityOutboxPending / len(batch) {
		if _, err := f.manager.Execute(t.Context(), f.claim, request(fmt.Sprint(i)), withOutbox(batch...)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := f.manager.Execute(t.Context(), f.claim, request("ordinary"), increment); err != nil {
		t.Fatal(err)
	}
	if result, err := f.manager.Execute(t.Context(), f.claim, request("0"), withOutbox(outboxIntent())); err != nil || !result.Replayed {
		t.Fatal("full queue blocked original replay", result, err)
	}
	pendingOutbox(t, f.manager, f.id, uint64(api.MaxDurableEntityOutboxPending/len(batch)+1), api.MaxDurableEntityOutboxPending)
}

func TestOutboxStorageCapRejectsBeforeUploadAndReplaysBelowLoweredCap(t *testing.T) {
	f := newFixture(t)
	if _, err := f.manager.Execute(t.Context(), f.claim, request("first"), withOutbox(outboxIntent())); err != nil {
		t.Fatal(err)
	}
	if err := f.manager.SetStorageLimit(t.Context(), f.claim, 1); err != nil {
		t.Fatal(err)
	}
	f.manager.store = wrappedStore{ObjectStore: f.store, put: func(context.Context, string, []byte, string) (string, error) {
		t.Fatal("storage cap failure dispatched an upload")
		return "", ErrInvalid
	}}
	if _, err := f.manager.Execute(t.Context(), f.claim, request("second"), withOutbox(outboxIntent())); !errors.Is(err, ErrLimit) {
		t.Fatal(err)
	}
	if result, err := f.manager.Execute(t.Context(), f.claim, request("first"), withOutbox(outboxIntent())); err != nil || !result.Replayed {
		t.Fatal(result, err)
	}
	pendingOutbox(t, f.manager, f.id, 1, 1)
}

func TestOutboxSharesSnapshotCeilingWithBusinessState(t *testing.T) {
	f := newFixture(t)
	intent := outboxIntent()
	intent.Payload = json.RawMessage(`"` + strings.Repeat("x", 32<<10) + `"`)
	f.manager.store = wrappedStore{ObjectStore: f.store, put: func(context.Context, string, []byte, string) (string, error) {
		t.Fatal("combined snapshot ceiling dispatched an upload")
		return "", ErrInvalid
	}}
	_, err := f.manager.Execute(t.Context(), f.claim, request("large"), func(context.Context, View) (Transition, error) {
		return Transition{Data: json.RawMessage(`"` + strings.Repeat("x", api.MaxDurableEntitySnapshotBytes-(32<<10)) + `"`), Result: json.RawMessage(`null`), Outbox: []OutboxIntent{intent}}, nil
	})
	var limit *LimitError
	if !errors.As(err, &limit) || limit.Budget != "snapshot_bytes" {
		t.Fatal(err)
	}
	pendingOutbox(t, f.manager, f.id, 0, 0)
}

func TestOutboxIdentityIncludesEveryEntityScopeAndBatchPosition(t *testing.T) {
	f := newFixture(t)
	f.id.EnvironmentID, f.id.TenantID = "environment-a", "tenant-a"
	seen := make(map[string]bool)
	for _, field := range []string{"base", "account", "app", "environment", "tenant", "namespace", "key", "version", "ordinal"} {
		id, version, ordinal := f.id, uint64(1), 0
		switch field {
		case "account":
			id.AccountID = "account-b"
		case "app":
			id.AppID = "app-b"
		case "environment":
			id.EnvironmentID = "environment-b"
		case "tenant":
			id.TenantID = "tenant-b"
		case "namespace":
			id.Namespace = "namespace-b"
		case "key":
			id.Key = "key-b"
		case "version":
			version++
		case "ordinal":
			ordinal++
		}
		identity := outboxMessageID(id, version, ordinal)
		if !validUUID(identity) || seen[identity] || identity != outboxMessageID(id, version, ordinal) {
			t.Fatal("unstable or shared delivery identity", field, identity)
		}
		seen[identity] = true
	}
}

func TestGuestProtocolRejectsOutboxUntilDeliveryIsAvailable(t *testing.T) {
	for _, body := range []string{`{"data":{},"result":null,"outbox":[]}`, `{"data":{},"result":null,"Outbox":[]}`} {
		if _, err := DecodeTransition([]byte(body)); !errors.Is(err, ErrInvalid) {
			t.Fatal("guest could enqueue undeliverable messages", err)
		}
	}
}
