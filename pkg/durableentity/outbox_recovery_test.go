// adr: 829
package durableentity

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestOutboxPublicationFailuresNeverExposeOrDuplicateUncommittedWork(t *testing.T) {
	for _, stage := range []string{"receipt-upload", "snapshot-ack", "manifest-rejection", "manifest-before-write", "manifest-ack"} {
		t.Run(stage, func(t *testing.T) {
			f := newFixture(t)
			f.manager.store = wrappedStore{ObjectStore: f.store, put: func(ctx context.Context, key string, body []byte, etag string) (string, error) {
				if stage == "receipt-upload" && strings.Contains(key, "/receipts/") || stage == "manifest-before-write" && strings.HasSuffix(key, "/manifest.json") {
					return "", errors.New("injected transport failure")
				}
				if stage == "manifest-rejection" && strings.HasSuffix(key, "/manifest.json") {
					return "", ErrConflict
				}
				version, err := f.store.Put(ctx, key, body, etag)
				if err == nil && (stage == "snapshot-ack" && strings.Contains(key, "/snapshots/") || stage == "manifest-ack" && strings.HasSuffix(key, "/manifest.json")) {
					return "", errors.New("injected lost acknowledgement")
				}
				return version, err
			}}
			want := ErrUncertain
			if stage == "manifest-rejection" {
				want = ErrConflict
			}
			if _, err := f.manager.Execute(t.Context(), f.claim, request("confirmation"), withOutbox(outboxIntent())); !errors.Is(err, want) {
				t.Fatal(err)
			}
			committed := stage == "manifest-ack"
			count := 0
			if committed {
				count = 1
			}
			pendingOutbox(t, f.manager, f.id, uint64(count), count)
			assertCount(t, t.Context(), f.manager, f.id, count, uint64(count))
			// Restart under a different owner after a process loss. Only a rooted
			// receipt may suppress the callback; uploaded snapshots cannot do so.
			f.clock.Add(int64(api.MaxDurableEntityLease + time.Second))
			restarted := openManager(t, f.store, f.clock)
			claim, err := restarted.Acquire(t.Context(), f.id, "takeover")
			if err != nil {
				t.Fatal(err)
			}
			calls := 0
			result, err := restarted.Execute(t.Context(), claim, request("confirmation"), func(ctx context.Context, v View) (Transition, error) {
				calls++
				return withOutbox(outboxIntent())(ctx, v)
			})
			if err != nil || result.Replayed != committed || calls != 1-count {
				t.Fatal(result, calls, err)
			}
			collectAll(t, restarted, claim)
			pendingOutbox(t, restarted, f.id, 1, 1)
			assertCount(t, t.Context(), restarted, f.id, 1, 1)
		})
	}
}

func TestOutboxTakeoverFencesObsoleteIntentAtPublicationCAS(t *testing.T) {
	f := newFixture(t)
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
	if _, err := f.manager.Execute(t.Context(), f.claim, request("obsolete"), withOutbox(outboxIntent())); !errors.Is(err, ErrConflict) {
		t.Fatal("obsolete outgoing intent was published", err)
	}
	pendingOutbox(t, winner, f.id, 1, 0)
	assertCount(t, t.Context(), winner, f.id, 1, 1)
}

func TestPendingOutboxReclamationRaceReturnsConflict(t *testing.T) {
	f := newFixture(t)
	if _, err := f.manager.Execute(t.Context(), f.claim, request("first"), withOutbox(outboxIntent())); err != nil {
		t.Fatal(err)
	}
	base, _, err := f.manager.readManifest(t.Context(), f.id)
	if err != nil {
		t.Fatal(err)
	}
	reader := openManager(t, &readRaceStore{ObjectStore: f.store, key: base.SnapshotKey, before: func() {
		if _, err := f.manager.Execute(t.Context(), f.claim, request("second"), increment); err != nil {
			t.Fatal(err)
		}
		collectAll(t, f.manager, f.claim)
	}}, f.clock)
	if _, err := reader.PendingOutbox(t.Context(), f.id); !errors.Is(err, ErrConflict) {
		t.Fatal("reclaimed snapshot was treated as committed corruption", err)
	}
	pendingOutbox(t, reader, f.id, 2, 1)
}

func TestOutboxRestoreRejectsCorruptCommittedMetadata(t *testing.T) {
	for _, kind := range []string{"id", "scope", "version", "ordinal", "duplicate", "order", "schema", "old-manifest", "webhook", "payload-size", "count", "bytes", "missing", "checksum"} {
		t.Run(kind, func(t *testing.T) {
			f := newFixture(t)
			if _, err := f.manager.Execute(t.Context(), f.claim, request("first"), withOutbox(outboxIntent(), outboxIntent())); err != nil {
				t.Fatal(err)
			}
			base, etag, err := f.manager.readManifest(t.Context(), f.id)
			if err != nil {
				t.Fatal(err)
			}
			state, err := f.manager.readSnapshot(t.Context(), base)
			if err != nil {
				t.Fatal(err)
			}
			switch kind {
			case "id":
				state.Outbox[0].ID = "invalid"
			case "scope":
				other := f.id
				other.TenantID = "another-customer"
				state.Outbox[0].ID = outboxMessageID(other, 1, 0)
			case "version":
				state.Outbox[0].Version = 2
			case "ordinal":
				state.Outbox[0].Ordinal = -1
			case "duplicate":
				state.Outbox[1] = state.Outbox[0]
			case "order":
				state.Outbox[0], state.Outbox[1] = state.Outbox[1], state.Outbox[0]
			case "schema":
				state.Schema = 2
			case "old-manifest":
				base.Schema = 4
			case "webhook":
				state.Outbox[0].Intent.WebhookID = "arbitrary-url"
			case "payload-size":
				state.Outbox[0].Intent.Payload = json.RawMessage(`"` + strings.Repeat("x", api.MaxDurableEntityOutboxPayloadBytes) + `"`)
			case "count":
				state.Outbox = make([]OutboxMessage, api.MaxDurableEntityOutboxPending+1)
			case "checksum":
				state.Data = json.RawMessage(`{"count":999}`)
			case "bytes":
				for i := range 5 {
					intent := outboxIntent()
					intent.Payload = json.RawMessage(`"` + strings.Repeat("x", api.MaxDurableEntityOutboxPayloadBytes-2) + `"`)
					state.Outbox = append(state.Outbox, OutboxMessage{ID: outboxMessageID(f.id, 1, i+2), Version: 1, Ordinal: i + 2, Intent: intent})
				}
			}
			body, err := json.Marshal(state)
			if err != nil {
				t.Fatal(err)
			}
			f.store.mu.Lock()
			f.store.objects[base.SnapshotKey] = memoryObject{body: body, version: digest(body)}
			if kind == "missing" {
				delete(f.store.objects, base.SnapshotKey)
			}
			f.store.mu.Unlock()
			if kind != "checksum" {
				base.SnapshotHash = digest(body)
				base.StorageUsage.SnapshotBytes = int64(len(body))
				sealStorageUsage(&base)
			}
			// Raw CAS preserves the intentionally old schema for the upgrade case.
			manifestBody, err := json.Marshal(base)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := f.store.Put(t.Context(), f.id.prefix()+"manifest.json", manifestBody, etag); err != nil {
				t.Fatal(err)
			}
			if _, err := f.manager.PendingOutbox(t.Context(), f.id); !errors.Is(err, ErrCorrupt) {
				t.Fatal("corrupt pending work was exposed", err)
			}
			if _, err := f.manager.Execute(t.Context(), f.claim, request("second"), increment); !errors.Is(err, ErrCorrupt) {
				t.Fatal("corrupt pending work was replaced", err)
			}
		})
	}
}
