package commit

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

// Consumers own their business-effect transaction. Record the stable source/ID
// pair in that same transaction, scoped to the intended consumer.
func TestPostgresConsumerDeduplication(t *testing.T) {
	pool := pgtest.Open(t)
	ctx := context.Background()
	if _, err := pool.Exec(ctx, `CREATE TABLE processed_events(consumer text NOT NULL,source text NOT NULL,event_id uuid NOT NULL,PRIMARY KEY(consumer,source,event_id)); CREATE TABLE business_effects(id integer PRIMARY KEY,total integer NOT NULL); INSERT INTO business_effects VALUES(1,0)`); err != nil {
		t.Fatal(err)
	}
	eventID := uuid.NewString()
	aborted := errors.New("consumer died before commit")
	consume := func(source string, failBeforeCommit bool) error {
		tx, err := pool.Begin(ctx)
		if err != nil {
			return err
		}
		defer tx.Rollback(ctx)
		inserted, err := tx.Exec(ctx, `INSERT INTO processed_events(consumer,source,event_id) VALUES('order-accounting',$1,$2::uuid) ON CONFLICT DO NOTHING`, source, eventID)
		if err != nil {
			return err
		}
		if inserted.RowsAffected() == 1 {
			if _, err := tx.Exec(ctx, `UPDATE business_effects SET total=total+1 WHERE id=1`); err != nil {
				return err
			}
		}
		if failBeforeCommit {
			return aborted
		}
		return tx.Commit(ctx)
	}
	if err := consume("gregale.commit.orders", true); !errors.Is(err, aborted) {
		t.Fatalf("rollback setup: %v", err)
	}
	var effects, markers int
	if err := pool.QueryRow(ctx, `SELECT total FROM business_effects WHERE id=1`).Scan(&effects); err != nil || effects != 0 {
		t.Fatalf("rolled-back effect: %d %v", effects, err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM processed_events`).Scan(&markers); err != nil || markers != 0 {
		t.Fatalf("rolled-back identity: %d %v", markers, err)
	}
	failures := make(chan error, 8)
	var workers sync.WaitGroup
	for range 8 {
		workers.Add(1)
		go func() { defer workers.Done(); failures <- consume("gregale.commit.orders", false) }()
	}
	workers.Wait()
	close(failures)
	for err := range failures {
		if err != nil {
			t.Fatal(err)
		}
	}
	if err := pool.QueryRow(ctx, `SELECT total FROM business_effects WHERE id=1`).Scan(&effects); err != nil || effects != 1 {
		t.Fatalf("duplicate business effects: %d %v", effects, err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM processed_events`).Scan(&markers); err != nil || markers != 1 {
		t.Fatalf("consumer identities: %d %v", markers, err)
	}
	// Different customer sources may legitimately supply the same event UUID.
	if err := consume("gregale.commit.other-orders", false); err != nil {
		t.Fatal(err)
	}
	if err := consume("gregale.commit.orders", false); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT total FROM business_effects WHERE id=1`).Scan(&effects); err != nil || effects != 2 {
		t.Fatalf("distinct source suppressed or retry duplicated effect: %d %v", effects, err)
	}
}
