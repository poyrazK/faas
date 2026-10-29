package state_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state"
)

type alertTestSendTestStore interface {
	state.AlertTestSendStore
	CreateAccount(ctx context.Context, email string, plan api.Plan) (state.Account, error)
}

// exerciseAlertTestSends pins the relay queue (ADR 20261022111812, section
// 3): one claim at a time, a lapsed claim is claimable again, only the
// claim holder completes, and requests are scoped to their account.
func exerciseAlertTestSends(t *testing.T, s alertTestSendTestStore) {
	ctx := context.Background()
	acct, err := s.CreateAccount(ctx, "test-sends-"+uuid.NewString()+"@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateAlertTestSend(ctx, state.AlertTestSend{ID: uuid.NewString(), AccountID: acct.ID, Kind: "sms", SubjectID: uuid.NewString()}); !errors.Is(err, state.ErrInvalidArgument) {
		t.Fatalf("unknown kind = %v, want ErrInvalidArgument", err)
	}
	id := uuid.NewString()
	event := json.RawMessage(`{"id":"d1","payload":{"test":true}}`)
	created, err := s.CreateAlertTestSend(ctx, state.AlertTestSend{ID: id, AccountID: acct.ID, Kind: state.AlertTestSendRuleWebhook, SubjectID: uuid.NewString(), Event: event})
	if err != nil || created.State != state.AlertTestSendPending {
		t.Fatalf("create = %+v, %v", created, err)
	}
	if _, err := s.GetAlertTestSend(ctx, uuid.NewString(), id); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("another account read the request: %v", err)
	}

	claimed, ok, err := s.ClaimAlertTestSend(ctx, time.Minute)
	if err != nil || !ok || claimed.ID != id || claimed.ClaimToken == "" {
		t.Fatalf("claim = %+v, %v, %v", claimed, ok, err)
	}
	var got map[string]any
	if json.Unmarshal(claimed.Event, &got) != nil || got["id"] != "d1" {
		t.Fatalf("event = %s", claimed.Event)
	}
	if _, ok, _ := s.ClaimAlertTestSend(ctx, time.Minute); ok {
		t.Fatal("a held claim was claimed twice")
	}
	if err := s.CompleteAlertTestSend(ctx, id, uuid.NewString(), state.AlertTestSendResult{Delivered: true}); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("complete with a stranger's token = %v, want ErrConflict", err)
	}

	// A lapsed claim is claimable again, and only the new holder completes.
	second := uuid.NewString()
	if _, err := s.CreateAlertTestSend(ctx, state.AlertTestSend{ID: second, AccountID: acct.ID, Kind: state.AlertTestSendChannel, SubjectID: uuid.NewString()}); err != nil {
		t.Fatal(err)
	}
	short, ok, err := s.ClaimAlertTestSend(ctx, time.Millisecond)
	if err != nil || !ok || short.ID != second {
		t.Fatalf("claim second = %+v, %v, %v", short, ok, err)
	}
	time.Sleep(20 * time.Millisecond)
	again, ok, err := s.ClaimAlertTestSend(ctx, time.Minute)
	if err != nil || !ok || again.ID != second || again.ClaimToken == short.ClaimToken {
		t.Fatalf("reclaim = %+v, %v, %v; want the lapsed request under a new token", again, ok, err)
	}
	if err := s.CompleteAlertTestSend(ctx, second, short.ClaimToken, state.AlertTestSendResult{}); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("the lapsed holder completed: %v", err)
	}

	result := state.AlertTestSendResult{Delivered: true, StatusCode: 204, Attempts: 1}
	if err := s.CompleteAlertTestSend(ctx, id, claimed.ClaimToken, result); err != nil {
		t.Fatal(err)
	}
	done, err := s.GetAlertTestSend(ctx, acct.ID, id)
	if err != nil || done.State != state.AlertTestSendDone || done.Result == nil || *done.Result != result {
		t.Fatalf("done = %+v, %v", done, err)
	}
	if err := s.DeleteAlertTestSend(ctx, acct.ID, id); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetAlertTestSend(ctx, acct.ID, id); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("deleted request = %v", err)
	}
	if n, err := s.PruneAlertTestSends(ctx, time.Now().Add(time.Hour)); err != nil || n < 1 {
		t.Fatalf("prune = %d, %v; want the second request removed", n, err)
	}
}

func TestMemStore_AlertTestSends(t *testing.T) {
	exerciseAlertTestSends(t, state.NewMemStore())
}

func TestPgStore_AlertTestSends(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	ctx := context.Background()
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatalf("db.MigrateUp: %v", err)
	}
	exerciseAlertTestSends(t, state.NewPgStore(pool))
}
