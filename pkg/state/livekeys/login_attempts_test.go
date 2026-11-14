package livekeys_test

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/onebox-faas/faas/pkg/state/livekeys"
)

func TestLiveLoginRecoveryAttemptsSurviveFailuresAndRestarts(t *testing.T) {
	pool := open(t)
	store := livekeys.New(pool)
	ctx := t.Context()
	var tail string
	for n := 1; n <= 21; n++ {
		id := fmt.Sprintf("00000000-0000-0000-0000-%012d", n)
		if _, err := pool.Exec(ctx, `INSERT INTO managed_postgres_databases(id,account_id,state) VALUES($1,$2,'ready')`, id, account); err != nil {
			t.Fatal(err)
		}
		if err := store.Enable(ctx, account, id, key(fmt.Sprintf("%016x", n)), nil); err != nil {
			t.Fatal(err)
		}
		tail = id
	}
	refs, err := store.DatabasesWithoutLogin(ctx, 20)
	if err != nil || len(refs) != 20 {
		t.Fatal(refs, err)
	}
	for _, ref := range refs {
		marked, err := store.RecordLoginAttempt(ctx, ref.AccountID, ref.DatabaseID)
		if err != nil || !marked {
			t.Fatal(marked, err)
		}
		stored, err := store.IssueMissingLogin(ctx, ref.AccountID, ref.DatabaseID, func(context.Context) (*livekeys.Login, error) { return nil, errors.New("provider unavailable") })
		if err == nil || stored {
			t.Fatal("failed provider result was stored", stored, err)
		}
	}
	// A new store instance has no process cursor to inherit.
	restarted := livekeys.New(pool)
	refs, err = restarted.DatabasesWithoutLogin(ctx, 20)
	if err != nil || len(refs) != 20 || refs[0].DatabaseID != tail {
		t.Fatalf("untried tail was starved after restart: %+v %v", refs, err)
	}
	if marked, err := restarted.RecordLoginAttempt(ctx, other, tail); err != nil || marked {
		t.Fatal("foreign attempt was accepted", marked, err)
	}
	if marked, err := restarted.RecordLoginAttempt(ctx, account, tail); err != nil || !marked {
		t.Fatal(marked, err)
	}
	stored, err := restarted.IssueMissingLogin(ctx, account, tail, func(context.Context) (*livekeys.Login, error) {
		return &livekeys.Login{Sealed: []byte("fresh"), Recipient: recipient, Identity: "gregale_live_fresh", Slot: "slot"}, nil
	})
	if err != nil || !stored {
		t.Fatal("healthy tail was not repaired", stored, err)
	}
	refs, err = restarted.DatabasesWithoutLogin(ctx, 20)
	if err != nil || len(refs) != 20 {
		t.Fatal(refs, err)
	}
	for _, ref := range refs {
		if ref.DatabaseID == tail {
			t.Fatal("repaired login remained eligible")
		}
	}
	if _, err := pool.Exec(ctx, `UPDATE managed_postgres_databases SET state='deleting' WHERE id=$1`, tail); err != nil {
		t.Fatal(err)
	}
	if marked, err := restarted.RecordLoginAttempt(ctx, account, tail); err != nil || marked {
		t.Fatal("deleting database was attempted", marked, err)
	}
}
