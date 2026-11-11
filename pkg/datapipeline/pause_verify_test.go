package datapipeline

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

func TestVerificationRejectedWhilePausePending(t *testing.T) {
	s, store, _, account := testService(t)
	ctx := context.Background()
	c, err := s.CreateConnection(ctx, account, api.CreateDataConnectionRequest{Name: "source", ConnectionURL: "postgres://r:p@db.example/db?sslmode=verify-full"})
	if err != nil {
		t.Fatal(err)
	}
	fixturePassingCheck(t, store, account, c)
	p, err := s.Create(ctx, account, api.CreateDataPipelineRequest{Name: "orders", SourceConnectionID: c.ID, DatabaseID: uuid.NewString(), Tables: []string{"orders"}, ReadSubjects: []string{"alice"}})
	if err != nil {
		t.Fatal(err)
	}
	claim, err := store.Claim(ctx, uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Finish(ctx, claim, Observation{State: "live", Verification: "unknown", Initialized: true}); err != nil {
		t.Fatal(err)
	}
	paused, err := store.Control(ctx, account, p.ID, p.Revision, "pause")
	if err != nil {
		t.Fatal(err)
	}
	if paused.State != "live" || paused.DesiredState != "paused" {
		t.Fatalf("pause fixture: %+v", paused)
	}
	if _, err = store.Control(ctx, account, p.ID, paused.Revision, "verify"); !errors.Is(err, ErrConflict) {
		t.Fatalf("verification accepted while pause pending: %v", err)
	}
	current, err := store.Get(ctx, account, p.ID)
	if err != nil || current.Revision != paused.Revision || current.VerifyRequested {
		t.Fatalf("rejected verification changed intent: %+v %v", current, err)
	}
	resumed, err := store.Control(ctx, account, p.ID, current.Revision, "resume")
	if err != nil {
		t.Fatal(err)
	}
	verified, err := store.Control(ctx, account, p.ID, resumed.Revision, "verify")
	if err != nil || !verified.VerifyRequested {
		t.Fatalf("verification after resume: %+v %v", verified, err)
	}
}
