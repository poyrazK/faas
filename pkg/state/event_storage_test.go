package state_test

// adr: 604

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

type eventStorageTestStore interface {
	state.Store
	state.EventStorageUsageStore
	state.PublishedEventWorkStore
	state.PublishedEventRetentionStore
	state.EventReceiptAcceptanceStore
	state.EventSubscriptionStore
	AppendEventAt(context.Context, string, string, *string, []byte, time.Time) error
}

func forEventStorageStores(t *testing.T, test func(*testing.T, eventStorageTestStore, *pgxpool.Pool)) {
	t.Run("memory", func(t *testing.T) { test(t, state.NewMemStore(), nil) })
	t.Run("postgres", func(t *testing.T) { store, pool, _ := pgStoreWithPool(t); test(t, store, pool) })
}

func storagePayload(id, source, data string) []byte {
	b, _ := json.Marshal(map[string]any{"id": id, "source": source, "type": "created", "data": json.RawMessage(data)})
	return b
}

func TestEventStorageAtomicCountAdmission(t *testing.T) {
	forEventStorageStores(t, func(t *testing.T, store eventStorageTestStore, pool *pgxpool.Pool) {
		ctx := context.Background()
		account, err := store.CreateAccount(ctx, "storage-"+uuid.NewString()+"@example.com", api.PlanFree)
		if err != nil {
			t.Fatal(err)
		}
		limit := api.MustLimitsFor(api.PlanFree).EventStorage.RetainedEvents
		if pool != nil {
			// Existing retained receipts are charged too, including rows created
			// by older writers. Seed those without an expensive API loop.
			_, err = pool.Exec(ctx, `INSERT INTO event_fanout_outbox(account_id,source,event_id,event_type,event_data,payload,recipient_snapshot)
                SELECT $1::uuid,'orders','seed-'||n,'created','{}'::jsonb,
                jsonb_build_object('id','seed-'||n,'source','orders','type','created','data','{}'::jsonb),'[]'::jsonb
                FROM generate_series(1,$2::bigint) n`, account.ID, limit-1)
			if err != nil {
				t.Fatal(err)
			}
		} else {
			for n := int64(1); n < limit; n++ {
				if err := store.AppendEvent(ctx, "apid", "event.published", &account.ID, storagePayload(fmt.Sprintf("seed-%d", n), "orders", "{}")); err != nil {
					t.Fatal(err)
				}
			}
		}
		before, _ := store.ListEvents(ctx, account.ID, int(limit+10))
		var wg sync.WaitGroup
		results := make(chan struct {
			id  string
			err error
		}, 32)
		for n := 0; n < 32; n++ {
			wg.Add(1)
			go func(n int) {
				defer wg.Done()
				id := fmt.Sprintf("burst-%d", n)
				results <- struct {
					id  string
					err error
				}{id, store.AppendEvent(ctx, "apid", "event.published", &account.ID, storagePayload(id, "orders", "{}"))}
			}(n)
		}
		wg.Wait()
		close(results)
		var accepted string
		for result := range results {
			if result.err == nil {
				if accepted != "" {
					t.Fatal("over-admitted concurrent publishes")
				}
				accepted = result.id
			} else if !errors.Is(result.err, state.ErrEventStorageCapacity) {
				t.Fatal(result.err)
			}
		}
		if accepted == "" {
			t.Fatal("no publish admitted into last slot")
		}
		usage, err := store.EventStorageUsage(ctx, account.ID)
		if err != nil || usage.RetainedEvents != limit || usage.PendingEvents != limit || usage.OldestPendingAt == nil {
			t.Fatalf("usage=%+v err=%v", usage, err)
		}
		at, err := store.EventReceiptAcceptedAt(ctx, account.ID, "orders", accepted)
		if err != nil {
			t.Fatal(err)
		}
		for range 8 {
			if err := store.AppendEvent(ctx, "apid", "event.published", &account.ID, storagePayload(accepted, "orders", "{}")); err != nil {
				t.Fatal(err)
			}
		}
		afterAt, _ := store.EventReceiptAcceptedAt(ctx, account.ID, "orders", accepted)
		if !afterAt.Equal(at) {
			t.Fatal("duplicate changed acceptance")
		}
		if err := store.AppendEvent(ctx, "apid", "event.published", &account.ID, storagePayload(accepted, "orders", `{"changed":true}`)); !errors.Is(err, state.ErrConflict) {
			t.Fatalf("identity conflict=%v", err)
		}
		after, _ := store.ListEvents(ctx, account.ID, int(limit+10))
		if len(after) != len(before)+1 {
			t.Fatalf("ledger rows before=%d after=%d", len(before), len(after))
		}
		if n, err := store.PruneDeliveredPublishedEvents(ctx, time.Now().Add(60*24*time.Hour), 100); err != nil || n != 0 {
			t.Fatalf("pending receipts pruned=%d %v", n, err)
		}
		work, err := store.ClaimDuePublishedEvent(ctx, time.Now())
		if err != nil {
			t.Fatal(err)
		}
		if err := store.FinishPublishedEvent(ctx, work.ID, work.ClaimToken, nil); err != nil {
			t.Fatal(err)
		}
		settled, _ := store.EventStorageUsage(ctx, account.ID)
		if settled.RetainedEvents != limit || settled.PendingEvents != limit-1 || settled.RetainedBytes != usage.RetainedBytes {
			t.Fatalf("settled=%+v", settled)
		}
		if n, err := store.PruneDeliveredPublishedEvents(ctx, time.Now().Add(state.PublishedEventIdentityRetention+time.Minute), 1); err != nil || n != 1 {
			t.Fatalf("prune=%d %v", n, err)
		}
		if err := store.AppendEvent(ctx, "apid", "event.published", &account.ID, storagePayload("after-prune", "orders", "{}")); err != nil {
			t.Fatal(err)
		}
		if err := store.UpdateAccountPlan(ctx, account.ID, api.PlanHobby); err != nil {
			t.Fatal(err)
		}
		if err := store.AppendEvent(ctx, "apid", "event.published", &account.ID, storagePayload("after-upgrade", "orders", "{}")); err != nil {
			t.Fatal(err)
		}
		if err := store.UpdateAccountPlan(ctx, account.ID, api.PlanFree); err != nil {
			t.Fatal(err)
		}
		if err := store.AppendEvent(ctx, "apid", "event.published", &account.ID, storagePayload("after-downgrade", "orders", "{}")); !errors.Is(err, state.ErrEventStorageCapacity) {
			t.Fatalf("downgrade=%v", err)
		}
		if err := store.AppendEvent(ctx, "apid", "event.published", &account.ID, storagePayload("after-upgrade", "orders", "{}")); err != nil {
			t.Fatalf("duplicate after downgrade=%v", err)
		}
		// Reserved platform events are independent of the customer budget.
		if err := store.AppendEvent(ctx, "platform", "event.published", &account.ID, storagePayload("lifecycle", "gregale.app", "{}")); err != nil {
			t.Fatal(err)
		}
		final, _ := store.EventStorageUsage(ctx, account.ID)
		if final.RetainedEvents != limit+1 {
			t.Fatalf("platform charged=%+v", final)
		}
	})
}

func TestEventStorageBytesRollbackAndSnapshotCharge(t *testing.T) {
	forEventStorageStores(t, func(t *testing.T, store eventStorageTestStore, _ *pgxpool.Pool) {
		ctx := context.Background()
		account, err := store.CreateAccount(ctx, "storage-bytes-"+uuid.NewString()+"@example.com", api.PlanFree)
		if err != nil {
			t.Fatal(err)
		}
		limit := api.MustLimitsFor(api.PlanFree).EventStorage.RetainedBytes
		large, _ := json.Marshal(strings.Repeat("x", int(limit/2)))
		payload := storagePayload("too-large", "orders", string(large))
		err = store.AppendEvent(ctx, "apid", "event.published", &account.ID, payload)
		var capacity *state.EventStorageCapacityError
		if !errors.As(err, &capacity) || capacity.Resource != "bytes" {
			t.Fatalf("byte limit=%v", err)
		}
		usage, _ := store.EventStorageUsage(ctx, account.ID)
		ledger, _ := store.ListEvents(ctx, account.ID, 10)
		if usage.RetainedEvents != 0 || usage.RetainedBytes != 0 || len(ledger) != 0 {
			t.Fatalf("partial acceptance=%+v ledger=%d", usage, len(ledger))
		}
		if err := store.AppendEvent(ctx, "apid", "event.published", &account.ID, storagePayload("before-snapshot", "orders", "{}")); err != nil {
			t.Fatal(err)
		}
		baseline, _ := store.EventStorageUsage(ctx, account.ID)
		app, err := store.CreateApp(ctx, state.App{ID: uuid.NewString(), AccountID: account.ID, Slug: "storage-" + uuid.NewString(), Type: state.AppTypeApp, RAMMB: 128, MaxConcurrency: 1})
		if err != nil {
			t.Fatal(err)
		}
		// Exact source and prefix wildcard subscriptions contribute snapshots.
		if _, _, err := store.UpsertEventSubscription(ctx, account.ID, app.ID, "orders", "*", nil); err != nil {
			t.Fatal(err)
		}
		if err := store.AppendEventAt(ctx, "apid", "event.published", &account.ID, storagePayload("after-snapshot-", "orders", "{}"), time.Time{}); err != nil {
			t.Fatal(err)
		}
		usage, _ = store.EventStorageUsage(ctx, account.ID)
		if usage.RetainedBytes-baseline.RetainedBytes <= baseline.RetainedBytes+100 {
			t.Fatalf("snapshot uncharged=%+v", usage)
		}
		other, err := store.CreateAccount(ctx, "storage-other-"+uuid.NewString()+"@example.com", api.PlanFree)
		if err != nil {
			t.Fatal(err)
		}
		otherUsage, err := store.EventStorageUsage(ctx, other.ID)
		if err != nil || otherUsage.RetainedEvents != 0 || otherUsage.OldestPendingAt != nil {
			t.Fatalf("other account=%+v %v", otherUsage, err)
		}
	})
}

func TestEventStorageConcurrentByteAdmission(t *testing.T) {
	forEventStorageStores(t, func(t *testing.T, store eventStorageTestStore, _ *pgxpool.Pool) {
		ctx := context.Background()
		account, err := store.CreateAccount(ctx, "storage-burst-"+uuid.NewString()+"@example.com", api.PlanFree)
		if err != nil {
			t.Fatal(err)
		}
		data, _ := json.Marshal(strings.Repeat("x", 256<<10))
		var wg sync.WaitGroup
		results := make(chan error, 24)
		for n := 0; n < 24; n++ {
			wg.Add(1)
			go func(n int) {
				defer wg.Done()
				results <- store.AppendEvent(ctx, "apid", "event.published", &account.ID, storagePayload(fmt.Sprint(n), "orders", string(data)))
			}(n)
		}
		wg.Wait()
		close(results)
		var accepted, rejected int64
		for err := range results {
			if err == nil {
				accepted++
			} else if errors.Is(err, state.ErrEventStorageCapacity) {
				rejected++
			} else {
				t.Fatal(err)
			}
		}
		usage, err := store.EventStorageUsage(ctx, account.ID)
		if err != nil || accepted == 0 || rejected == 0 || usage.RetainedEvents != accepted || usage.RetainedBytes > usage.Limits.RetainedBytes {
			t.Fatalf("burst accepted=%d rejected=%d usage=%+v err=%v", accepted, rejected, usage, err)
		}
		ledger, _ := store.ListEvents(ctx, account.ID, 100)
		if int64(len(ledger)) != accepted {
			t.Fatalf("partial ledger=%d accepted=%d", len(ledger), accepted)
		}
	})
}

func TestPgEventStorageIdentityJSONBEquality(t *testing.T) {
	store, pool, ctx := pgStoreWithPool(t)
	account, err := store.CreateAccount(ctx, "storage-jsonb-"+uuid.NewString()+"@example.com", api.PlanFree)
	if err != nil {
		t.Fatal(err)
	}
	_, err = pool.Exec(ctx, `INSERT INTO events(actor,kind,subject,data) VALUES('apid','event.published',$1::uuid,$2::jsonb)`, account.ID, storagePayload("decimal", "orders", `{"n":1.0}`))
	if err != nil {
		t.Fatal(err)
	}
	before, err := store.EventStorageUsage(ctx, account.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.AppendEvent(ctx, "apid", "event.published", &account.ID, storagePayload("decimal", "orders", `{"n":1e0}`)); err != nil {
		t.Fatalf("equivalent jsonb=%v", err)
	}
	after, _ := store.EventStorageUsage(ctx, account.ID)
	ledger, _ := store.ListEvents(ctx, account.ID, 10)
	if before.RetainedEvents != 1 || before.RetainedBytes == 0 || after.RetainedBytes != before.RetainedBytes || len(ledger) != 1 {
		t.Fatalf("before=%+v after=%+v ledger=%d", before, after, len(ledger))
	}
}
