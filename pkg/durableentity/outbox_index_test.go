// adr: 933
package durableentity

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

func TestOutboxDiscoveryRepairsLostHintsAndPrunesAcknowledgedWork(t *testing.T) {
	f := newFixture(t)
	f.manager.store = cleanupFaultStore{memoryStore: f.store, put: func(ctx context.Context, key string, body []byte, version string) (string, error) {
		if strings.HasPrefix(key, outboxIndexPrefix) {
			return "", errors.New("index unavailable")
		}
		return f.store.Put(ctx, key, body, version)
	}}
	if _, err := f.manager.Execute(t.Context(), f.claim, request("confirmation"), withOutbox(outboxIntent())); err != nil {
		t.Fatal(err)
	}
	restarted := openManager(t, f.store, f.clock)
	if page, err := restarted.ScanIndexedDueOutbox(t.Context(), ""); err != nil || len(page.Work) != 0 {
		t.Fatal(page, err)
	}
	page, err := restarted.ScanDueOutbox(t.Context(), "")
	if err != nil || len(page.Work) != 1 || page.Failed != 0 {
		t.Fatal(page, err)
	}
	indexed, err := restarted.ScanIndexedDueOutbox(t.Context(), "")
	if err != nil || len(indexed.Work) != 1 || indexed.Work[0] != page.Work[0] {
		t.Fatal(indexed, err)
	}
	reservation, err := restarted.ReserveOutbox(t.Context(), f.claim, page.Work[0].MessageID)
	if err != nil {
		t.Fatal(err)
	}
	if page, err := restarted.ScanIndexedDueOutbox(t.Context(), ""); err != nil || len(page.Work) != 0 {
		t.Fatal("backoff dispatched work", page, err)
	}
	if err := restarted.AcknowledgeOutbox(t.Context(), f.claim, reservation.Message.ID, reservation.Token); err != nil {
		t.Fatal(err)
	}
	f.clock.Add(int64(api.DurableEntityOutboxRetryBase))
	if page, err := restarted.ScanIndexedDueOutbox(t.Context(), ""); err != nil || len(page.Work) != 0 || page.Failed != 0 {
		t.Fatal(page, err)
	}
	objects, err := f.store.ListEntityObjects(t.Context(), outboxIndexPrefix, "", api.DurableEntityCleanupPageSize)
	if err != nil || len(objects.Keys) != 0 {
		t.Fatal("obsolete hints survived", objects, err)
	}
}

func TestOutboxDiscoveryPaginatesAndValidatesHintsAgainstCommittedHead(t *testing.T) {
	f := newFixture(t)
	for i := range 12 {
		id := f.id
		id.Key = fmt.Sprintf("pending-%d", i)
		if _, err := f.manager.Invoke(t.Context(), id, "writer", request("confirmation"), withOutbox(outboxIntent())); err != nil {
			t.Fatal(err)
		}
	}
	for _, scan := range []func(context.Context, string) (OutboxPage, error){f.manager.ScanDueOutbox, f.manager.ScanIndexedDueOutbox} {
		seen := map[ID]bool{}
		cursor := ""
		for {
			page, err := scan(t.Context(), cursor)
			if err != nil || page.Failed != 0 || len(page.Work) > api.DurableEntityOutboxScanPageSize {
				t.Fatal(page, err)
			}
			for _, work := range page.Work {
				if seen[work.Entity] {
					t.Fatal("duplicate discovery", work)
				}
				seen[work.Entity] = true
			}
			cursor = page.NextCursor
			if cursor == "" {
				break
			}
		}
		if len(seen) != 12 {
			t.Fatal("rotation omitted entities", len(seen))
		}
	}
	// A forged hint cannot authorize a different message or scope.
	fake := outboxIndexEntry{Work: OutboxWork{Entity: f.id, MessageID: uuid.NewString()}}
	fake.DueAt = time.Unix(0, 0).UTC()
	body, _ := json.Marshal(fake)
	if _, err := f.store.Put(t.Context(), fake.key(), body, ""); err != nil {
		t.Fatal(err)
	}
	_, due, _, err := f.manager.indexedOutbox(t.Context(), f.store, fake.key())
	if err != nil || due {
		t.Fatal("forged hint became dispatch authority", err)
	}
	if _, _, err := f.store.Get(t.Context(), fake.key(), api.MaxDurableEntityOutboxIndexBytes); !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
}

func TestOutboxCorruptReservationFailsClosed(t *testing.T) {
	for _, kind := range []string{"schema", "head", "token", "attempts", "deadline"} {
		t.Run(kind, func(t *testing.T) {
			f, message := committedOutboxFixture(t, 1)
			if _, err := f.manager.ReserveOutbox(t.Context(), f.claim, message.ID); err != nil {
				t.Fatal(err)
			}
			base, etag, err := f.manager.readManifest(t.Context(), f.id)
			if err != nil {
				t.Fatal(err)
			}
			switch kind {
			case "schema":
				base.Schema = 5
			case "head":
				base.OutboxDelivery.MessageID = uuid.NewString()
			case "token":
				base.OutboxDelivery.Token = "invalid"
			case "attempts":
				base.OutboxDelivery.Attempts = api.MaxDurableEntityOutboxAttempts + 1
			case "deadline":
				base.OutboxDelivery.NextAttemptAt = time.Time{}
			}
			body, err := json.Marshal(base)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := f.store.Put(t.Context(), f.id.prefix()+"manifest.json", body, etag); err != nil {
				t.Fatal(err)
			}
			if _, err := f.manager.InspectOutbox(t.Context(), f.id); !errors.Is(err, ErrCorrupt) {
				t.Fatal(err)
			}
		})
	}
}
